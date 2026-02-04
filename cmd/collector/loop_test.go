package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	telemetryv1 "gpu-metric-collector/api/gen"
	"gpu-metric-collector/internal/model"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// --- fakes ---

type fakeStream struct {
	ctx    context.Context
	ch     chan *telemetryv1.TelemetryData
	once   sync.Once
	closed chan struct{}
}

func newFakeStream(ctx context.Context, buf int) *fakeStream {
	return &fakeStream{ctx: ctx, ch: make(chan *telemetryv1.TelemetryData, buf), closed: make(chan struct{})}
}

func (f *fakeStream) Recv() (*telemetryv1.TelemetryData, error) {
	// Prefer draining any queued messages before observing cancellation.
	select {
	case m, ok := <-f.ch:
		if !ok {
			return nil, errors.New("stream closed")
		}
		return m, nil
	default:
	}
	select {
	case m, ok := <-f.ch:
		if !ok {
			return nil, errors.New("stream closed")
		}
		return m, nil
	case <-f.ctx.Done():
		return nil, f.ctx.Err()
	}
}

func (f *fakeStream) Context() context.Context { return f.ctx }

func (f *fakeStream) close() { f.once.Do(func() { close(f.ch); close(f.closed) }) }

type captureStore struct {
	mu    sync.Mutex
	items []model.Telemetry
	fail  bool
}

func (s *captureStore) SaveTelemetry(t model.Telemetry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return errors.New("save failed")
	}
	s.items = append(s.items, t)
	return nil
}
func (s *captureStore) ListGPUs() ([]string, error) { return nil, nil }
func (s *captureStore) QueryTelemetry(string, *time.Time, *time.Time) ([]model.Telemetry, error) {
	return nil, nil
}

// --- tests ---

func TestCollector_FlushOnSize(t *testing.T) {
	ctx := context.Background()
	fs := newFakeStream(ctx, 10)
	st := &captureStore{}

	// speed up timer to avoid waiting
	oldTicker := tickerFn
	tickerFn = func(d time.Duration) *time.Ticker { return time.NewTicker(24 * time.Hour) }
	defer func() { tickerFn = oldTicker }()

	// run loop
	done := make(chan struct{})
	go func() {
		_ = runCollectorLoop(ctx, fs, st, 3, 1000, 1)
		close(done)
	}()

	// send exactly 3 messages to trigger size flush
	ts := timestamppb.Now()
	for i := 0; i < 3; i++ {
		fs.ch <- &telemetryv1.TelemetryData{GpuId: "g1", Ts: ts}
	}
	// let Recv block by closing stream to make loop return
	fs.close()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting loop to finish")
	}

	// expect 3 items saved
	if len(st.items) != 3 {
		t.Fatalf("expected 3 saved, got %d", len(st.items))
	}
}

func TestCollector_FlushOnTimer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fs := newFakeStream(ctx, 1)
	st := &captureStore{}

	// fast ticker for timer flush
	oldTicker := tickerFn
	tickerFn = func(d time.Duration) *time.Ticker { return time.NewTicker(10 * time.Millisecond) }
	defer func() { tickerFn = oldTicker }()

	done := make(chan struct{})
	go func() {
		_ = runCollectorLoop(ctx, fs, st, 100, 5, 1)
		close(done)
	}()

	// send one message and then let ticker trigger
	fs.ch <- &telemetryv1.TelemetryData{GpuId: "g1", Ts: timestamppb.Now()}
	time.Sleep(30 * time.Millisecond)
	cancel() // stop loop
	fs.close()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting loop to finish")
	}

	if len(st.items) != 1 {
		t.Fatalf("expected 1 saved after timer flush, got %d", len(st.items))
	}
}

func TestCollector_GracefulFlushOnStreamClose(t *testing.T) {
	ctx := context.Background()
	fs := newFakeStream(ctx, 10)
	st := &captureStore{}

	oldTicker := tickerFn
	tickerFn = func(d time.Duration) *time.Ticker { return time.NewTicker(24 * time.Hour) }
	defer func() { tickerFn = oldTicker }()

	done := make(chan struct{})
	go func() {
		_ = runCollectorLoop(ctx, fs, st, 100, 1000, 1)
		close(done)
	}()

	// enqueue some items but not enough for size flush
	for i := 0; i < 5; i++ {
		fs.ch <- &telemetryv1.TelemetryData{GpuId: "g1", Ts: timestamppb.Now()}
	}
	// close stream to trigger graceful path; ensure Recv unblocks
	fs.close()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting loop to finish")
	}

	if len(st.items) != 5 {
		t.Fatalf("expected graceful flush of 5 items, got %d", len(st.items))
	}
}
