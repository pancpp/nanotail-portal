package maintenance

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestGateOwnership(t *testing.T) {
	var gate Gate
	if gate.Pending() || gate.Operation() != "" || gate.TryAcquire("") {
		t.Fatal("new gate must be idle and reject an empty owner")
	}
	if !gate.TryAcquire("reset") || !gate.Pending() || gate.Operation() != "reset" {
		t.Fatal("reset did not acquire gate")
	}
	if gate.TryAcquire("install") || gate.TryAcquire("reset") {
		t.Fatal("overlapping maintenance was admitted")
	}
	gate.Release("install")
	if !gate.Pending() {
		t.Fatal("a non-owner released the gate")
	}
	gate.Release("reset")
	if !gate.TryAcquire("install") {
		t.Fatal("released gate could not be reused")
	}
	gate.Release("reset")
	if gate.Operation() != "install" {
		t.Fatal("reset released a later install")
	}
}

func TestGateAdmitsExactlyOneConcurrentOperation(t *testing.T) {
	var gate Gate
	var accepted atomic.Int64
	var workers sync.WaitGroup
	start := make(chan struct{})
	for index := range 64 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			operation := "reset"
			if index%2 == 0 {
				operation = "install"
			}
			if gate.TryAcquire(operation) {
				accepted.Add(1)
			}
		}()
	}
	close(start)
	workers.Wait()
	if accepted.Load() != 1 || !gate.Pending() {
		t.Fatalf("accepted %d concurrent operations", accepted.Load())
	}
}
