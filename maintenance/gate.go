// Package maintenance coordinates operations that stop or replace the portal.
package maintenance

import "sync"

// Gate admits one maintenance operation at a time. Its zero value is ready to
// use. The owner keeps the gate until its operation finishes or is abandoned.
type Gate struct {
	mu        sync.Mutex
	operation string
}

func (g *Gate) TryAcquire(operation string) bool {
	if operation == "" {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.operation != "" {
		return false
	}
	g.operation = operation
	return true
}

// Release cannot clear an operation owned by another maintenance subsystem.
func (g *Gate) Release(operation string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.operation == operation {
		g.operation = ""
	}
}

func (g *Gate) Pending() bool { return g.Operation() != "" }

func (g *Gate) Operation() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.operation
}
