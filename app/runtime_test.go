package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
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
