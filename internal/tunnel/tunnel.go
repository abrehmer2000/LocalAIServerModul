// Package tunnel routes inference through authenticated, agent-initiated HTTPS requests.
// Payloads exist only in memory. Jobs are never retried after delivery.
package tunnel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"localaiserver/internal/config"
	"net/http"
	"sync"
	"time"
)

type Task struct {
	ID            string `json:"id"`
	Path          string `json:"path,omitempty"`
	Method        string `json:"method,omitempty"`
	TimeoutMillis int64  `json:"timeout_ms,omitempty"`
	Body          []byte `json:"body"`
}
type job struct {
	task      Task
	node      string
	ctx       context.Context
	cancel    context.CancelFunc
	response  chan *http.Response
	reader    *io.PipeReader
	writer    *io.PipeWriter
	claimed   bool
	delivered bool
}
type Broker struct {
	mu      sync.Mutex
	jobs    map[string]*job
	queues  map[string]chan *job
	seen    map[string]time.Time
	maxBody int64
}

func New(limits ...int64) *Broker {
	limit := int64(config.MaxRequestBytes)
	if len(limits) > 0 {
		limit = limits[0]
	}
	return &Broker{maxBody: limit, jobs: map[string]*job{}, queues: map[string]chan *job{}, seen: map[string]time.Time{}}
}
func (b *Broker) queue(id string) chan *job {
	q := b.queues[id]
	if q == nil {
		q = make(chan *job, 128)
		b.queues[id] = q
	}
	return q
}

type transport struct {
	broker *Broker
	node   string
}

func (b *Broker) Transport(node string) http.RoundTripper { return &transport{b, node} }
func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(io.LimitReader(req.Body, t.broker.maxBody+1))
	req.Body.Close()
	if err != nil || int64(len(body)) > t.broker.maxBody {
		return nil, errors.New("invalid tunnel request")
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(req.Context())
	j := &job{task: Task{ID: hex.EncodeToString(random[:]), Body: body, Path: req.URL.Path, Method: req.Method}, node: t.node, ctx: ctx, cancel: cancel, response: make(chan *http.Response, 1)}
	if deadline, ok := req.Context().Deadline(); ok {
		j.task.TimeoutMillis = time.Until(deadline).Milliseconds()
	}
	j.reader, j.writer = io.Pipe()
	b := t.broker
	b.mu.Lock()
	if len(b.jobs) >= 128 {
		b.mu.Unlock()
		cancel()
		return nil, errors.New("tunnel capacity exhausted")
	}
	q := b.queue(t.node)
	b.jobs[j.task.ID] = j
	select {
	case q <- j:
	default:
		delete(b.jobs, j.task.ID)
		j.task.Body = nil
		b.mu.Unlock()
		cancel()
		return nil, errors.New("tunnel capacity exhausted")
	}
	b.mu.Unlock()
	go func() {
		<-ctx.Done()
		j.reader.CloseWithError(ctx.Err())
		j.writer.CloseWithError(ctx.Err())
		b.mu.Lock()
		delete(b.jobs, j.task.ID)
		j.task.Body = nil
		b.mu.Unlock()
	}()
	select {
	case resp := <-j.response:
		resp.Request = req
		resp.Body = &bodyCloser{ReadCloser: j.reader, cancel: cancel}
		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type bodyCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *bodyCloser) Close() error { b.cancel(); return b.ReadCloser.Close() }
func (b *Broker) Poll(ctx context.Context, node string) (Task, error) {
	b.mu.Lock()
	q := b.queue(node)
	b.seen[node] = time.Now()
	b.mu.Unlock()
	for {
		select {
		case <-ctx.Done():
			return Task{}, ctx.Err()
		case j := <-q:
			b.mu.Lock()
			valid := j.ctx.Err() == nil && !j.claimed
			var task Task
			if valid {
				j.claimed = true
				task = j.task
				j.task.Body = nil
			}
			b.mu.Unlock()
			if valid {
				return task, nil
			}
		}
	}
}

// Context enforces ownership; an agent cannot read or complete another node's job.
func (b *Broker) Context(node, id string) (context.Context, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	j := b.jobs[id]
	if j == nil || j.node != node || !j.claimed {
		return nil, false
	}
	b.seen[node] = time.Now()
	return j.ctx, true
}
func (b *Broker) Deliver(ctx context.Context, node, id string, status int, contentType string, body io.Reader, retryAfter ...string) error {
	if status < 100 || status > 599 {
		return errors.New("invalid response status")
	}
	b.mu.Lock()
	j := b.jobs[id]
	if j == nil || j.node != node || !j.claimed || j.delivered || j.ctx.Err() != nil {
		b.mu.Unlock()
		return errors.New("unknown job")
	}
	j.delivered = true
	b.mu.Unlock()
	if status < 100 || status > 599 {
		return errors.New("invalid response status")
	}
	resp := &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{contentType}}}
	if len(retryAfter) > 0 && retryAfter[0] != "" {
		resp.Header.Set("Retry-After", retryAfter[0])
	}
	select {
	case j.response <- resp:
	case <-j.ctx.Done():
		return j.ctx.Err()
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			j.writer.CloseWithError(ctx.Err())
		case <-done:
		}
	}()
	_, err := io.Copy(j.writer, io.LimitReader(body, 128<<20))
	j.writer.CloseWithError(err)
	b.mu.Lock()
	b.seen[node] = time.Now()
	b.mu.Unlock()
	return err
}
func (b *Broker) CancelNode(node string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, j := range b.jobs {
		if j.node == node {
			j.cancel()
		}
	}
}
func (b *Broker) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, j := range b.jobs {
		j.cancel()
	}
}

const PollTimeout = 15 * time.Second

func (b *Broker) Online(node string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if time.Since(b.seen[node]) < 20*time.Second {
		return true
	}
	for _, j := range b.jobs {
		if j.node == node && j.claimed && j.ctx.Err() == nil {
			return true
		}
	}
	return false
}
