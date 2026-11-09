// Package httpbound limits accepted sockets before TLS allocates connection state.
package httpbound

import (
	"net"
	"sync"
)

type Listener struct {
	net.Listener
	slots chan struct{}
	done  chan struct{}
	once  sync.Once
}

func New(inner net.Listener, limit int) *Listener {
	if limit < 1 {
		limit = 256
	}
	return &Listener{Listener: inner, slots: make(chan struct{}, limit), done: make(chan struct{})}
}
func (l *Listener) Accept() (net.Conn, error) {
	select {
	case l.slots <- struct{}{}:
	case <-l.done:
		return nil, net.ErrClosed
	}
	conn, err := l.Listener.Accept()
	if err != nil {
		<-l.slots
		return nil, err
	}
	return &connection{Conn: conn, release: func() { <-l.slots }}, nil
}
func (l *Listener) Close() error { l.once.Do(func() { close(l.done) }); return l.Listener.Close() }

type connection struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *connection) Close() error { err := c.Conn.Close(); c.once.Do(c.release); return err }
