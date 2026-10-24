package server

import (
	"sync"

	"github.com/apernet/quic-go"
)

// ConnRegistry tracks authenticated QUIC connections by auth id.
// It allows an operator (e.g. a traffic stats API endpoint) to immediately
// close all existing connections of a user, including idle ones, without
// leaving any one-shot kick marks behind.
//
// The zero value is not usable; use NewConnRegistry.
type ConnRegistry struct {
	mu sync.Mutex
	m  map[string]map[*quic.Conn]struct{}
}

func NewConnRegistry() *ConnRegistry {
	return &ConnRegistry{m: make(map[string]map[*quic.Conn]struct{})}
}

// Register adds an authenticated connection under the given auth id.
// It is a no-op if the registry is nil.
func (r *ConnRegistry) Register(id string, conn *quic.Conn) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	conns, ok := r.m[id]
	if !ok {
		conns = make(map[*quic.Conn]struct{})
		r.m[id] = conns
	}
	conns[conn] = struct{}{}
}

// Unregister removes a connection from the registry (on disconnect).
// It is a no-op if the registry is nil.
func (r *ConnRegistry) Unregister(id string, conn *quic.Conn) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	conns, ok := r.m[id]
	if !ok {
		return
	}
	delete(conns, conn)
	if len(conns) == 0 {
		delete(r.m, id)
	}
}

// CloseAll immediately closes all existing connections of the given auth id
// with the provided application error code. It returns the number of
// connections that were closed. Closed connections are removed from the
// registry. It does NOT create any one-shot kick marks; connections
// authenticated after this call are unaffected.
// It is a no-op returning 0 if the registry is nil.
func (r *ConnRegistry) CloseAll(id string, code quic.ApplicationErrorCode) int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	conns, ok := r.m[id]
	if !ok {
		r.mu.Unlock()
		return 0
	}
	// Take ownership of the set and remove it from the registry,
	// so that new registrations during Close are not affected.
	delete(r.m, id)
	r.mu.Unlock()

	n := 0
	for conn := range conns {
		_ = conn.CloseWithError(code, "")
		n++
	}
	return n
}

// Count returns the number of tracked connections for the given auth id.
// It is a no-op returning 0 if the registry is nil.
func (r *ConnRegistry) Count(id string) int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.m[id])
}
