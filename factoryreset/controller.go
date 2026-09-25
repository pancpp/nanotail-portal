package factoryreset

import (
	"errors"
	"sync/atomic"
)

var ErrBusy = errors.New("A factory reset is already in progress")

// Controller accepts one reset at a time. HTTP acknowledges the request before
// signalling the main loop, which owns shutdown, file cleanup, and re-exec.
type Controller struct {
	pending   atomic.Bool
	requests  chan struct{}
	preflight func() error
}

func NewController(preflight func() error) *Controller {
	return &Controller{requests: make(chan struct{}, 1), preflight: preflight}
}

func (c *Controller) Accept() error {
	if !c.pending.CompareAndSwap(false, true) {
		return ErrBusy
	}
	if err := c.preflight(); err != nil {
		c.pending.Store(false)
		return err
	}
	return nil
}

func (c *Controller) Pending() bool             { return c.pending.Load() }
func (c *Controller) Requests() <-chan struct{} { return c.requests }
func (c *Controller) Schedule()                 { c.requests <- struct{}{} }
func (c *Controller) RetryAllowed()             { c.pending.Store(false) }
