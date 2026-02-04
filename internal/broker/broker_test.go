package broker

import (
	"context"
	"sync"
	"testing"
	"time"

	telemetryv1 "gpu-metric-collector/api/gen"

	"google.golang.org/grpc/metadata"
)

// fakeStream implements telemetryv1.Telemetry_SubscribeServer with a controllable Context and Send behavior.
type fakeStream struct {
	ctx       context.Context
	sendFn    func(*telemetryv1.TelemetryData) error
	headMD    metadata.MD
	trailerMD metadata.MD
}

func (f *fakeStream) SetHeader(md metadata.MD) error          { f.headMD = md; return nil }
func (f *fakeStream) SendHeader(md metadata.MD) error         { return nil }
func (f *fakeStream) SetTrailer(md metadata.MD)               { f.trailerMD = md }
func (f *fakeStream) Context() context.Context                { return f.ctx }
func (f *fakeStream) SendMsg(m any) error                     { return nil }
func (f *fakeStream) RecvMsg(m any) error                     { return nil }
func (f *fakeStream) Send(t *telemetryv1.TelemetryData) error { return f.sendFn(t) }

func TestPublishBackpressure(t *testing.T) {
	s := NewServer(1, 1) // very small queue to trigger backpressure

	batch := &telemetryv1.TelemetryBatch{Items: []*telemetryv1.TelemetryData{{GpuId: "g0"}, {GpuId: "g1"}}}
	resp, err := s.PublishBatch(context.Background(), batch)
	if err != nil {
		t.Fatalf("PublishBatch error: %v", err)
	}
	if resp.Status != "BACKPRESSURE" {
		t.Fatalf("expected BACKPRESSURE, got %s", resp.Status)
	}
	if resp.Accepted != 1 {
		t.Fatalf("expected accepted=1, got %d", resp.Accepted)
	}
}

func TestSubscribeRoundRobinDelivery(t *testing.T) {
	s := NewServer(10, 10)

	var mu sync.Mutex
	recvA := 0
	recvB := 0

	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	fsA := &fakeStream{ctx: ctxA, sendFn: func(d *telemetryv1.TelemetryData) error {
		mu.Lock()
		recvA++
		if recvA >= 1 {
			mu.Unlock()
			cancelA()
			return nil
		}
		mu.Unlock()
		return nil
	}}
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	fsB := &fakeStream{ctx: ctxB, sendFn: func(d *telemetryv1.TelemetryData) error {
		mu.Lock()
		recvB++
		if recvB >= 1 {
			mu.Unlock()
			cancelB()
			return nil
		}
		mu.Unlock()
		return nil
	}}

	// Start subscribers
	go func() { _ = s.Subscribe(&telemetryv1.SubscriptionRequest{}, fsA) }()
	go func() { _ = s.Subscribe(&telemetryv1.SubscriptionRequest{}, fsB) }()

	// allow dispatcher to register both subscribers to avoid race with publish
	time.Sleep(20 * time.Millisecond)

	// Publish two messages; each subscriber should receive one (round-robin)
	batch := &telemetryv1.TelemetryBatch{Items: []*telemetryv1.TelemetryData{{GpuId: "g0"}, {GpuId: "g1"}}}
	if _, err := s.PublishBatch(context.Background(), batch); err != nil {
		t.Fatalf("PublishBatch error: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		a, b := recvA, recvB
		mu.Unlock()
		if a >= 1 && b >= 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("did not receive messages on both subscribers: A=%d B=%d", recvA, recvB)
}

func TestRequeueOnSendErrorToAnotherSubscriber(t *testing.T) {
	s := NewServer(10, 10)

	// First subscriber always errors; second should receive the message after requeue
	errCtx, errCancel := context.WithCancel(context.Background())
	defer errCancel()
	errStream := &fakeStream{ctx: errCtx, sendFn: func(d *telemetryv1.TelemetryData) error {
		// cause Subscribe to remove this subscriber and requeue
		return context.Canceled
	}}
	okCtx, okCancel := context.WithCancel(context.Background())
	defer okCancel()
	received := make(chan *telemetryv1.TelemetryData, 1)
	okStream := &fakeStream{ctx: okCtx, sendFn: func(d *telemetryv1.TelemetryData) error {
		select {
		case received <- d:
		default:
		}
		okCancel()
		return nil
	}}

	go func() { _ = s.Subscribe(&telemetryv1.SubscriptionRequest{}, errStream) }()
	go func() { _ = s.Subscribe(&telemetryv1.SubscriptionRequest{}, okStream) }()

	// publish a single message; first sub will error, broker should requeue and second should get it
	batch := &telemetryv1.TelemetryBatch{Items: []*telemetryv1.TelemetryData{{GpuId: "g0"}}}
	if _, err := s.PublishBatch(context.Background(), batch); err != nil {
		t.Fatalf("PublishBatch error: %v", err)
	}

	select {
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for message to be delivered to second subscriber after requeue")
	case d := <-received:
		if d.GetGpuId() != "g0" {
			t.Fatalf("unexpected gpu id: %s", d.GetGpuId())
		}
	}
}
