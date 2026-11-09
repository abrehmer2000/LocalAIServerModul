package pool

import (
	"context"
	"errors"
	"time"
)

var ErrBusy = errors.New("model_busy")
var ErrUnavailable = errors.New("model_unavailable")
var ErrLoading = errors.New("model_loading")

type waiter struct {
	model       string
	interactive bool
	since       time.Time
}

// notifyLocked only publishes state changes. No network or audit work holds mu.
func (p *Pool) notifyLocked()    { close(p.changed); p.changed = make(chan struct{}) }
func (p *Pool) Drain()           { p.mu.Lock(); p.draining = true; p.notifyLocked(); p.mu.Unlock() }
func (p *Pool) IsDraining() bool { p.mu.Lock(); defer p.mu.Unlock(); return p.draining }
func (p *Pool) QueueLength(model string) int {
	p.waitMu.Lock()
	defer p.waitMu.Unlock()
	return p.queued(model)
}
func (p *Pool) queued(model string) int {
	n := 0
	for _, w := range p.waiters {
		if model == "" || w.model == model {
			n++
		}
	}
	return n
}
func (p *Pool) remove(w *waiter) {
	for i, v := range p.waiters {
		if v == w {
			p.waiters = append(p.waiters[:i], p.waiters[i+1:]...)
			break
		}
	}
}
func (p *Pool) first(w *waiter) bool {
	var best *waiter
	for _, v := range p.waiters {
		if v.model != w.model {
			continue
		}
		if best == nil {
			best = v
		}
		if v.interactive && !best.interactive && time.Since(best.since) < 30*time.Second {
			best = v
		}
	}
	return best == w
}

// Availability separates lifecycle from occupancy. The snapshot is read now,
// rather than treating the age of a worker's last task poll as slot occupancy.
func (p *Pool) Availability(model string) error {
	p.mu.Lock()
	if p.draining {
		p.mu.Unlock()
		return ErrUnavailable
	}
	nodes := make([]Node, 0)
	for _, n := range p.nodes {
		if n.Config.Model == model {
			nodes = append(nodes, *n)
		}
	}
	p.mu.Unlock()
	loading := false
	for _, n := range nodes {
		if n.dynamic && !n.authorized() {
			continue
		}
		if (n.dynamic || n.Config.EngineProtocol == "llama.cpp") && (!time.Now().Before(n.leaseUntil) || !n.ready) {
			if n.lifecycle == "loading" {
				loading = true
			}
			continue
		}
		return nil
	}
	if loading {
		return ErrLoading
	}
	return ErrUnavailable
}

// WaitAcquire admits at most limit waiting clients per alias. Reservation and
// queue removal are serialized, preserving FIFO within each priority class.
func (p *Pool) WaitAcquire(ctx context.Context, model string, interactive bool, limit int, wait time.Duration) (*Node, func(bool), error) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	w := &waiter{model: model, interactive: interactive, since: time.Now()}
	p.waitMu.Lock()
	if err := ctx.Err(); err != nil {
		p.waitMu.Unlock()
		return nil, nil, err
	}
	if err := p.Availability(model); err != nil {
		p.waitMu.Unlock()
		return nil, nil, err
	}
	if p.queued(model) == 0 {
		if n, release := p.Acquire(model); n != nil {
			p.waitMu.Unlock()
			return n, release, nil
		}
	}
	if p.queued(model) >= limit {
		p.waitMu.Unlock()
		return nil, nil, ErrBusy
	}
	p.waiters = append(p.waiters, w)
	p.waitMu.Unlock()
	defer func() {
		p.waitMu.Lock()
		p.remove(w)
		p.waitMu.Unlock()
		p.mu.Lock()
		p.notifyLocked()
		p.mu.Unlock()
	}()
	for {
		p.waitMu.Lock()
		p.mu.Lock()
		changed := p.changed
		p.mu.Unlock()
		if err := ctx.Err(); err != nil {
			p.waitMu.Unlock()
			return nil, nil, err
		}
		if err := p.Availability(model); err != nil {
			p.waitMu.Unlock()
			return nil, nil, err
		}
		if p.first(w) {
			if n, release := p.Acquire(model); n != nil {
				p.remove(w)
				p.waitMu.Unlock()
				return n, release, nil
			}
		}
		p.waitMu.Unlock()
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-timer.C:
			return nil, nil, ErrBusy
		case <-changed:
		}
	}
}

// Lifecycle carries the engine's state, independently of active reservations.
func (p *Pool) Lifecycle(id, state string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, n := range p.nodes {
		if n.Config.ID == id {
			n.lifecycle = state
			p.notifyLocked()
			return
		}
	}
}

type Capabilities struct {
	EngineStarts   uint64    `json:"engine_starts"`
	Model          string    `json:"model"`
	ContextPerSlot int       `json:"context_per_slot"`
	Slots          int       `json:"slots_total"`
	Free           int       `json:"slots_free"`
	Active         int       `json:"slots_active"`
	Queue          int       `json:"queue_length"`
	Vision         bool      `json:"vision"`
	Revision       string    `json:"revision"`
	State          string    `json:"state"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (p *Pool) Capabilities(model string) Capabilities {
	c := Capabilities{Model: model, State: "unavailable", UpdatedAt: time.Now().UTC()}
	p.mu.Lock()
	nodes := append([]*Node(nil), p.nodes...)
	p.mu.Unlock()
	allowed := map[*Node]bool{}
	for _, n := range nodes {
		if n.dynamic {
			allowed[n] = n.authorized()
		}
	}
	p.mu.Lock()
	for _, n := range p.nodes {
		if n.Config.Model != model {
			continue
		}
		c.EngineStarts += n.engineStarts
		c.Slots += n.Config.Capacity
		c.Active += n.active
		c.ContextPerSlot = n.Config.ContextSize()
		c.Revision = n.Config.Revision
		c.Vision = n.Config.Vision
		if n.dynamic && !allowed[n] {
			continue
		}
		if (!n.dynamic || allowed[n]) && (!(n.dynamic || n.Config.EngineProtocol == "llama.cpp") || n.ready && time.Now().Before(n.leaseUntil)) {
			c.Free += n.Config.Capacity - n.active
			c.State = "ready"
		} else if n.lifecycle == "loading" && c.State != "ready" {
			c.State = "loading"
		}
	}
	if p.draining {
		c.State = "draining"
		c.Free = 0
	}
	p.mu.Unlock()
	c.Queue = p.QueueLength(model)
	return c
}

// RetryAfter is a bounded estimate from current reservations, not a promise that
// a model will finish in exactly this many seconds. Reading it has no cache age.
func (p *Pool) RetryAfter(model string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	delay := time.Second
	for _, n := range p.nodes {
		if n.Config.Model != model {
			continue
		}
		remaining := n.meanDuration - time.Since(n.oldest)
		if n.lifecycle == "loading" && remaining < 5*time.Second {
			remaining = 5 * time.Second
		}
		if remaining > delay {
			delay = remaining
		}
	}
	sec := int((delay + time.Second - 1) / time.Second)
	if sec > 30 {
		return 30
	}
	if sec < 1 {
		return 1
	}
	return sec
}

func (p *Pool) WaitIdle(ctx context.Context) error {
	for {
		p.mu.Lock()
		active := 0
		for _, n := range p.nodes {
			active += n.active
		}
		changed := p.changed
		p.mu.Unlock()
		if active == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}
