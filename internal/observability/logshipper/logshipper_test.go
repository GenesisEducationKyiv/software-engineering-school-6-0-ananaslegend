package logshipper_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	"github.com/ananaslegend/reposeetory/internal/observability/logshipper"
)

func TestWriter_BatchPostedAfterFlushInterval(t *testing.T) {
	received := make(chan string, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		received <- string(b)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	reg := prometheus.NewRegistry()
	w, err := logshipper.New(logshipper.Config{
		URL:           srv.URL,
		BufferSize:    16,
		BatchSize:     2,
		FlushInterval: 50 * time.Millisecond,
		Registry:      reg,
		Logger:        zerolog.New(io.Discard),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		_ = w.Close(context.Background())
	})

	if _, err := w.Write([]byte(`{"level":"info","message":"hello"}` + "\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := w.Write([]byte(`{"level":"info","message":"world"}` + "\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	select {
	case body := <-received:
		if !strings.Contains(body, `{"level":"info","message":"hello"}`) {
			t.Fatalf("body missing hello: %s", body)
		}
		if !strings.Contains(body, `{"level":"info","message":"world"}`) {
			t.Fatalf("body missing world: %s", body)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("no POST received within timeout")
	}
}

func TestWriter_DropsOnFullBuffer(t *testing.T) {
	// Block the server so the goroutine can't drain the channel.
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	t.Cleanup(func() { close(block); srv.Close() })

	reg := prometheus.NewRegistry()
	w, _ := logshipper.New(logshipper.Config{
		URL: srv.URL, BufferSize: 2, BatchSize: 100,
		FlushInterval: 5 * time.Second, Registry: reg, Logger: zerolog.New(io.Discard),
	})
	t.Cleanup(func() {
		// Short deadline: the server is still blocked at this point in
		// the LIFO cleanup chain; without a deadline Close waits for the
		// in-flight POST to finish (~15s via http client timeout × retries).
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_ = w.Close(ctx)
	})

	// First two fill the buffer; subsequent writes drop.
	w.Write([]byte("a\n"))
	w.Write([]byte("b\n"))
	w.Write([]byte("c\n"))
	w.Write([]byte("d\n"))

	mf, _ := reg.Gather()
	var dropped float64
	for _, f := range mf {
		if f.GetName() == "logshipper_dropped_total" {
			for _, m := range f.GetMetric() {
				dropped += m.GetCounter().GetValue()
			}
		}
	}
	if dropped < 2 {
		t.Fatalf("expected at least 2 drops, got %v", dropped)
	}
}

func TestWriter_NilURLReturnsNoopWriter(t *testing.T) {
	w, err := logshipper.New(logshipper.Config{URL: ""})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if w != nil {
		t.Fatalf("expected nil writer for empty URL")
	}
	// nil writer should still accept Write
	if _, err := w.Write([]byte("anything")); err != nil {
		t.Fatalf("nil Write: %v", err)
	}
}
