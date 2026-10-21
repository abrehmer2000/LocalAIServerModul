package tunnel

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestStreamingOwnershipAndCancellation(t *testing.T) {
	b := New()
	defer b.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://agent.invalid/v1/chat/completions", strings.NewReader(`{"stream":true}`))
	result := make(chan *http.Response, 1)
	go func() {
		r, e := b.Transport("one").RoundTrip(req)
		if e == nil {
			result <- r
		}
	}()
	task, e := b.Poll(ctx, "one")
	if e != nil {
		t.Fatal(e)
	}
	if _, ok := b.Context("two", task.ID); ok {
		t.Fatal("other node sees job")
	}
	if err := b.Deliver(ctx, "two", task.ID, 200, "text/event-stream", strings.NewReader("wrong")); err == nil {
		t.Fatal("other node completed job")
	}
	reader, writer := io.Pipe()
	upload := make(chan error, 1)
	go func() { upload <- b.Deliver(ctx, "one", task.ID, 200, "text/event-stream", reader) }()
	var resp *http.Response
	select {
	case resp = <-result:
	case <-time.After(time.Second):
		t.Fatal("headers blocked on response body")
	}
	go func() { io.WriteString(writer, "data: first\n\n") }()
	text := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(resp.Body).ReadString('\n'); text <- line }()
	select {
	case line := <-text:
		if line != "data: first\n" {
			t.Fatal(line)
		}
	case <-time.After(time.Second):
		t.Fatal("stream buffered")
	}
	jobctx, _ := b.Context("one", task.ID)
	resp.Body.Close()
	select {
	case <-jobctx.Done():
	case <-time.After(time.Second):
		t.Fatal("disconnect not propagated")
	}
	writer.Close()
	reader.Close()
	select {
	case <-upload:
	case <-time.After(time.Second):
		t.Fatal("upload did not stop")
	}
}
