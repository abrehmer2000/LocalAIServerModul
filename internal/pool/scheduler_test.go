package pool

import (
	"context"
	"errors"
	"localaiserver/internal/config"
	"testing"
	"time"
)

func waitQueue(t *testing.T, p *Pool, n int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for p.QueueLength("m") != n {
		if time.Now().After(deadline) {
			t.Fatalf("queue=%d want %d", p.QueueLength("m"), n)
		}
		time.Sleep(time.Millisecond)
	}
}
func testPool(t *testing.T) *Pool {
	t.Helper()
	p, err := New([]config.Node{{ID: "a", Model: "m", Capacity: 1}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func TestQueueFIFOAndImmediateReuse(t *testing.T) {
	p := testPool(t)
	_, release := p.Acquire("m")
	order := make(chan int, 3)
	for i := 0; i < 3; i++ {
		go func(i int) {
			_, done, err := p.WaitAcquire(context.Background(), "m", false, 8, time.Second)
			if err != nil {
				t.Error(err)
				return
			}
			order <- i
			done(false)
		}(i)
		waitQueue(t, p, i+1)
	}
	release(false)
	for i := 0; i < 3; i++ {
		select {
		case got := <-order:
			if got != i {
				t.Fatalf("got %d want %d", got, i)
			}
		case <-time.After(time.Second):
			t.Fatal("queue stalled")
		}
	}
	for i := 0; i < 50; i++ {
		n, done, err := p.WaitAcquire(context.Background(), "m", false, 8, time.Second)
		if err != nil || n == nil {
			t.Fatal("sequential rejection", err)
		}
		done(false)
	}
}
func TestQueueBoundCancellationAndPriority(t *testing.T) {
	p := testPool(t)
	_, release := p.Acquire("m")
	order := make(chan string, 2)
	ctx, cancel := context.WithCancel(context.Background())
	cancelled := make(chan error, 1)
	go func() { _, _, err := p.WaitAcquire(ctx, "m", false, 2, time.Second); cancelled <- err }()
	waitQueue(t, p, 1)
	go func() {
		_, done, err := p.WaitAcquire(context.Background(), "m", true, 2, time.Second)
		if err != nil {
			t.Error(err)
			return
		}
		order <- "interactive"
		done(false)
	}()
	waitQueue(t, p, 2)
	if _, _, err := p.WaitAcquire(context.Background(), "m", false, 2, time.Second); !errors.Is(err, ErrBusy) {
		t.Fatal("unbounded queue", err)
	}
	cancel()
	if err := <-cancelled; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	waitQueue(t, p, 1)
	go func() {
		_, done, err := p.WaitAcquire(context.Background(), "m", false, 2, time.Second)
		if err != nil {
			t.Error(err)
			return
		}
		order <- "normal"
		done(false)
	}()
	waitQueue(t, p, 2)
	release(false)
	if got := <-order; got != "interactive" {
		t.Fatal("priority ignored")
	}
	if got := <-order; got != "normal" {
		t.Fatal(got)
	}
}
func TestDrainWakesWaitersAndTimeoutDoesNotReserve(t *testing.T) {
	p := testPool(t)
	_, release := p.Acquire("m")
	defer release(false)
	if _, _, err := p.WaitAcquire(context.Background(), "m", false, 8, 10*time.Millisecond); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, _, err := p.WaitAcquire(context.Background(), "m", false, 8, time.Second); result <- err }()
	waitQueue(t, p, 1)
	p.Drain()
	select {
	case err := <-result:
		if !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("drain did not wake queue")
	}
	if p.QueueLength("m") != 0 {
		t.Fatal("abandoned queue entry")
	}
}

// A failed request is not evidence that all slots on its host are unavailable.
// Every release must wake the next waiter without a timed node-wide fence.
func TestFailedRequestsImmediatelyHandSlotsToConcurrentWaiters(t *testing.T) {
	p := testPool(t)
	_, release := p.Acquire("m")
	const count = 32
	results := make(chan error, count)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for i := 0; i < count; i++ {
		go func() {
			_, done, err := p.WaitAcquire(ctx, "m", false, count, 3*time.Second)
			if err == nil {
				done(true)
			}
			results <- err
		}()
	}
	waitQueue(t, p, count)
	release(true)
	for i := 0; i < count; i++ {
		select {
		case err := <-results:
			if err != nil {
				t.Fatalf("free slot rejected waiter after request failure: %v", err)
			}
		case <-ctx.Done():
			t.Fatal("released slot was not handed on promptly")
		}
	}
	c := p.Capabilities("m")
	if c.State != "ready" || c.Active != 0 || c.Free != 1 || c.Queue != 0 {
		t.Fatalf("incorrect occupancy after completed failures: %+v", c)
	}
}
