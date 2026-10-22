package pool

import (
	"crypto/tls"
	"localaiserver/internal/config"
	"sync"
	"testing"
	"time"
)

func TestCapacityConcurrentAcquireAndRelease(t *testing.T) {
	p, err := New([]config.Node{{ID: "cpu", Model: "m", Capacity: 1}, {ID: "gpu", Model: "m", Capacity: 3}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	var mu sync.Mutex
	var releases []func(bool)
	counts := map[string]int{}
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, release := p.Acquire("m")
			if n != nil {
				mu.Lock()
				releases = append(releases, release)
				counts[n.Config.ID]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if counts["cpu"] != 1 || counts["gpu"] != 3 {
		t.Fatalf("capacity violation: %v", counts)
	}
	for _, release := range releases {
		release(false)
		release(false)
	}
	// Release must be idempotent; four new slots, never more.
	for i := 0; i < 4; i++ {
		if n, _ := p.Acquire("m"); n == nil {
			t.Fatal("slot not released")
		}
	}
	if n, _ := p.Acquire("m"); n != nil {
		t.Fatal("double release created capacity")
	}
}
func TestModelIsolationAndFailedNode(t *testing.T) {
	p, err := New([]config.Node{{ID: "a", Model: "m1", Capacity: 1}, {ID: "b", Model: "m2", Capacity: 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	n, release := p.Acquire("m1")
	if n == nil || n.Config.ID != "a" {
		t.Fatal("wrong model selected")
	}
	release(true)
	if n, _ := p.Acquire("m1"); n != nil {
		t.Fatal("failed node selected or model substituted")
	}
	if n, _ := p.Acquire("m2"); n == nil || n.Config.ID != "b" {
		t.Fatal("healthy node unavailable")
	}
}

func TestDynamicHeartbeatLeaseAndRevocation(t *testing.T) {
	p, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	allowed := true
	n := config.Node{ID: "dynamic", URL: "https://127.0.0.1:9999", Model: "m", Revision: "r", Capacity: 1}
	if err = p.AddDynamic(n, &tls.Config{MinVersion: tls.VersionTLS12}, func() bool { return allowed }); err != nil {
		t.Fatal(err)
	}
	if got, _ := p.Acquire("m"); got != nil {
		t.Fatal("fresh enrollment routed before readiness")
	}
	p.Heartbeat(n.ID, true, time.Second)
	got, release := p.Acquire("m")
	if got == nil {
		t.Fatal("ready node unavailable")
	}
	release(false)
	p.Heartbeat(n.ID, true, -time.Second)
	if got, _ = p.Acquire("m"); got != nil {
		t.Fatal("expired lease still routed")
	}
	p.Heartbeat(n.ID, true, time.Second)
	allowed = false
	if got, _ = p.Acquire("m"); got != nil {
		t.Fatal("revoked node still routed")
	}
}
