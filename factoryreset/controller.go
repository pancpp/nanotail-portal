package factoryreset

import (
	"errors"
	"sync/atomic"

	"github.com/pancpp/nanotail-portal/maintenance"
)

var ErrBusy = errors.New("A factory reset is already in progress")

// Controller accepts one reset at a time. HTTP acknowledges the request before
// signalling the main loop, which owns shutdown, file cleanup, and re-exec.
type Controller struct {
	pending   atomic.Bool
	requests  chan struct{}
	preflight func() error
	gate      *maintenance.Gate
}

func NewController(preflight func() error, gates ...*maintenance.Gate) *Controller {
	gate := &maintenance.Gate{}
	if len(gates) > 0 && gates[0] != nil {
		gate = gates[0]
	}
	return &Controller{requests: make(chan struct{}, 1), preflight: preflight, gate: gate}
}

func (c *Controller) Accept() error {
	if !c.pending.CompareAndSwap(false, true) {
		return ErrBusy
	}
	if !c.gate.TryAcquire("reset") {
		c.pending.Store(false)
		return ErrBusy
	}
	if err := c.preflight(); err != nil {
		c.RetryAllowed()
		return err
	}
	return nil
}

func (c *Controller) Pending() bool             { return c.pending.Load() }
func (c *Controller) Requests() <-chan struct{} { return c.requests }
func (c *Controller) Schedule()                 { c.requests <- struct{}{} }
func (c *Controller) RetryAllowed() {
	c.gate.Release("reset")
	c.pending.Store(false)
}
