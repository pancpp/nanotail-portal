package factoryreset

import (
	"errors"
	"testing"

	"github.com/pancpp/nanotail-portal/maintenance"
)

func TestControllerExcludesInstallation(t *testing.T) {
	var gate maintenance.Gate
	preflightCalls := 0
	controller := NewController(func() error { preflightCalls++; return nil }, &gate)
	gate.TryAcquire("install")
	if err := controller.Accept(); !errors.Is(err, ErrBusy) || controller.Pending() || preflightCalls != 0 {
		t.Fatalf("reset admitted during installation: err=%v pending=%v calls=%d", err, controller.Pending(), preflightCalls)
	}
	controller.RetryAllowed()
	if gate.Operation() != "install" {
		t.Fatal("reset cleared the install gate")
	}
	gate.Release("install")
	if err := controller.Accept(); err != nil || !controller.Pending() || gate.Operation() != "reset" {
		t.Fatalf("reset acceptance: %v", err)
	}
	if gate.TryAcquire("install") {
		t.Fatal("installation admitted during reset")
	}
	controller.RetryAllowed()
	if controller.Pending() || gate.Pending() {
		t.Fatal("aborted reset did not release maintenance")
	}
	if err := controller.Accept(); err != nil || preflightCalls != 2 {
		t.Fatalf("reset cannot be retried: %v calls=%d", err, preflightCalls)
	}
}

func TestControllerReleasesMaintenanceOnPreflightFailure(t *testing.T) {
	var gate maintenance.Gate
	want := errors.New("preflight failed")
	controller := NewController(func() error { return want }, &gate)
	if err := controller.Accept(); !errors.Is(err, want) || controller.Pending() || gate.Pending() {
		t.Fatalf("preflight failure retained maintenance: %v", err)
	}
	if !gate.TryAcquire("install") {
		t.Fatal("failed reset blocks subsequent installation")
	}
}
