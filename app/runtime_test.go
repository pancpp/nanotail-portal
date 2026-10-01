package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"
)

func TestRuntimeShutdownDrainsActiveRequestAndCollector(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	requestDone := make(chan error, 1)
	go func() {
		response, err := server.Client().Get(server.URL)
		if err == nil {
			_, err = io.Copy(io.Discard, response.Body)
			response.Body.Close()
		}
		requestDone <- err
	}()
	<-entered
	ctx, cancel := context.WithCancel(t.Context())
	collectorDone := make(chan struct{})
	go func() { <-ctx.Done(); close(collectorDone) }()
	runtime := &Runtime{server: server.Config, stopCollector: cancel, collectorDone: collectorDone}
	stopped := make(chan error, 1)
	shutdown, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	go func() { stopped <- runtime.Shutdown(shutdown) }()
	<-collectorDone
	select {
	case err := <-stopped:
		close(release)
		t.Fatalf("shutdown returned before active request drained: %v", err)
	default:
	}
	close(release)
	if err := <-requestDone; err != nil {
		t.Fatal(err)
	}
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeShutdownWaitsForLED(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		workerCtx, cancel := context.WithCancel(t.Context())
		defer cancel()
		collectorDone, ledDone := make(chan struct{}), make(chan struct{})
		close(collectorDone)
		runtime := &Runtime{server: &http.Server{}, stopCollector: cancel, collectorDone: collectorDone, ledDone: ledDone}
		done := make(chan error, 1)
		go func() { done <- runtime.Shutdown(t.Context()) }()
		<-workerCtx.Done()
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("shutdown returned before LED restoration: %v", err)
		default:
		}
		close(ledDone)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestRuntimeShutdownLEDDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		collectorDone := make(chan struct{})
		close(collectorDone)
		runtime := &Runtime{server: &http.Server{}, stopCollector: func() {}, collectorDone: collectorDone, ledDone: make(chan struct{})}
		if err := runtime.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("shutdown = %v", err)
		}
	})
}

func TestRuntimeShutdownWaitsForRouting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		workerCtx, cancel := context.WithCancel(t.Context())
		defer cancel()
		collectorDone, routingDone := make(chan struct{}), make(chan struct{})
		close(collectorDone)
		runtime := &Runtime{server: &http.Server{}, stopCollector: cancel, collectorDone: collectorDone, routingDone: routingDone}
		done := make(chan error, 1)
		go func() { done <- runtime.Shutdown(t.Context()) }()
		<-workerCtx.Done()
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("shutdown returned before routing worker stopped: %v", err)
		default:
		}
		close(routingDone)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestRuntimeShutdownWaitsForAccess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		workerCtx, cancel := context.WithCancel(t.Context())
		defer cancel()
		collectorDone, accessDone := make(chan struct{}), make(chan struct{})
		close(collectorDone)
		runtime := &Runtime{server: &http.Server{}, stopCollector: cancel, collectorDone: collectorDone, accessDone: accessDone}
		done := make(chan error, 1)
		go func() { done <- runtime.Shutdown(t.Context()) }()
		<-workerCtx.Done()
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("shutdown returned before IP reporting stopped: %v", err)
		default:
		}
		close(accessDone)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestRuntimeShutdownAccessDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		collectorDone := make(chan struct{})
		close(collectorDone)
		runtime := &Runtime{server: &http.Server{}, stopCollector: func() {}, collectorDone: collectorDone, accessDone: make(chan struct{})}
		if err := runtime.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("shutdown = %v, want context deadline exceeded", err)
		}
	})
}
