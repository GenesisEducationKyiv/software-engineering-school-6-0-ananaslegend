// Package logshipper is a non-blocking io.Writer that ships zerolog JSON events
// to a Vector HTTP source. It is fail-open by design: if the buffer is full or
// the upstream is unreachable, events are dropped silently (and counted) rather
// than blocking the caller.
package logshipper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
)

// Config configures the Writer.
type Config struct {
	URL           string
	BufferSize    int
	BatchSize     int
	FlushInterval time.Duration
	Registry      *prometheus.Registry
	Logger        zerolog.Logger
	// Client is optional; defaults to a 5s-timeout client.
	Client *http.Client
}

// Writer implements io.Writer. Each Write enqueues one event into a bounded channel;
// a background goroutine batches and POSTs to URL.
type Writer struct {
	url    string
	client *http.Client
	log    zerolog.Logger

	in        chan []byte
	batchSize int
	flushIvl  time.Duration

	bufLen atomic.Int64

	mDropped *prometheus.CounterVec // labels: reason (full|fail)
	mSent    *prometheus.CounterVec // labels: result
	mBatch   prometheus.Histogram
	mBuffer  prometheus.GaugeFunc

	done      chan struct{}
	runCtx    context.Context
	cancel    context.CancelFunc
	closeOnce sync.Once
}

// New constructs the Writer and starts its background goroutine.
// If cfg.URL == "" it returns (nil, nil) — caller should treat nil as no-op.
func New(cfg Config) (*Writer, error) {
	if cfg.URL == "" {
		return nil, nil
	}
	if cfg.BufferSize <= 0 || cfg.BatchSize <= 0 || cfg.FlushInterval <= 0 {
		return nil, fmt.Errorf("logshipper.New: invalid Config: %+v", cfg)
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := &Writer{
		url: cfg.URL, client: client, log: cfg.Logger,
		in:        make(chan []byte, cfg.BufferSize),
		batchSize: cfg.BatchSize,
		flushIvl:  cfg.FlushInterval,
		done:      make(chan struct{}),
		runCtx:    ctx,
		cancel:    cancel,
	}

	dropped := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "logshipper_dropped_total",
		Help: "Total events dropped by the log shipper.",
	}, []string{"reason"})
	sent := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "logshipper_sent_total",
		Help: "Total events sent (or attempted) by the log shipper.",
	}, []string{"result"})
	batch := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "logshipper_batch_duration_seconds",
		Help:    "Duration of one batch POST in seconds.",
		Buckets: []float64{0.005, 0.01, 0.05, 0.1, 0.5, 1, 2, 5},
	})
	bufferGauge := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "logshipper_buffer_size",
		Help: "Current number of buffered events.",
	}, func() float64 { return float64(w.bufLen.Load()) })

	if cfg.Registry != nil {
		cfg.Registry.MustRegister(dropped, sent, batch, bufferGauge)
	}
	w.mDropped, w.mSent, w.mBatch = dropped, sent, batch
	w.mBuffer = bufferGauge

	go w.run()
	return w, nil
}

// Write is non-blocking and always returns (len(p), nil) so zerolog never sees a failure.
func (w *Writer) Write(p []byte) (int, error) {
	if w == nil {
		return len(p), nil
	}
	dup := make([]byte, len(p))
	copy(dup, p)
	select {
	case w.in <- dup:
		w.bufLen.Add(1)
	default:
		w.mDropped.WithLabelValues("full").Inc()
	}
	return len(p), nil
}

// Close drains the buffer up to ctx deadline, then stops the background goroutine.
func (w *Writer) Close(ctx context.Context) error {
	if w == nil {
		return nil
	}
	w.closeOnce.Do(func() {
		close(w.in)
	})
	select {
	case <-w.done:
		w.cancel() // free resources
		return nil
	case <-ctx.Done():
		w.cancel() // abort in-flight POST
		return errors.New("logshipper.Close: deadline exceeded")
	}
}

func (w *Writer) run() {
	defer close(w.done)
	ticker := time.NewTicker(w.flushIvl)
	defer ticker.Stop()

	batch := make([][]byte, 0, w.batchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		w.bufLen.Add(int64(-len(batch)))
		w.postBatch(batch)
		batch = batch[:0]
	}

	for {
		select {
		case ev, ok := <-w.in:
			if !ok {
				flush()
				return
			}
			batch = append(batch, ev)
			if len(batch) >= w.batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (w *Writer) postBatch(batch [][]byte) {
	start := time.Now()
	defer func() { w.mBatch.Observe(time.Since(start).Seconds()) }()

	var body bytes.Buffer
	for _, ev := range batch {
		body.Write(ev)
		if len(ev) == 0 || ev[len(ev)-1] != '\n' {
			body.WriteByte('\n')
		}
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(w.runCtx, http.MethodPost, w.url, bytes.NewReader(body.Bytes()))
		if err != nil {
			lastErr = fmt.Errorf("logshipper.Writer.postBatch: %w", err)
			break
		}
		req.Header.Set("Content-Type", "application/x-ndjson")
		resp, err := w.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("logshipper.Writer.postBatch: client.Do: %w", err)
		} else {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode/100 == 2 {
				w.mSent.WithLabelValues("ok").Add(float64(len(batch)))
				return
			}
			lastErr = fmt.Errorf("logshipper.Writer.postBatch: non-2xx: %s", resp.Status)
		}
		if attempt < 2 {
			time.Sleep(time.Duration(50<<attempt) * time.Millisecond)
		}
	}
	w.mSent.WithLabelValues("error").Add(float64(len(batch)))
	w.mDropped.WithLabelValues("fail").Add(float64(len(batch)))
	w.log.Debug().Err(lastErr).Int("batch", len(batch)).Msg("logshipper: batch failed")
}
