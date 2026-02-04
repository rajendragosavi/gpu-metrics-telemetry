package main

import (
	"context"
	"errors"
	"testing"
	"time"

	telemetryv1 "gpu-metric-collector/api/gen"

	"google.golang.org/grpc"
)

// fakeTelemetryClient is a controllable fake for TelemetryClient used to simulate
// OK responses, backpressure (partial accepts), and errors.
type fakeTelemetryClient struct {
	resp *telemetryv1.PublishResponse
	err  error
	// optional scripted responses for multiple calls
	script    []*telemetryv1.PublishResponse
	scriptErr []error
	calls     int
}

func (f *fakeTelemetryClient) PublishBatch(ctx context.Context, req *telemetryv1.TelemetryBatch, opts ...grpc.CallOption) (*telemetryv1.PublishResponse, error) {
	if f.script != nil && f.calls < len(f.script) {
		r := f.script[f.calls]
		e := error(nil)
		if f.scriptErr != nil && f.calls < len(f.scriptErr) {
			e = f.scriptErr[f.calls]
		}
		f.calls++
		return r, e
	}
	f.calls++
	return f.resp, f.err
}

// minimal Subscribe to satisfy TelemetryClient in tests
type fakeSubStream struct{ grpc.ClientStream }

func (s *fakeSubStream) Recv() (*telemetryv1.TelemetryData, error) { return nil, context.Canceled }

func (f *fakeTelemetryClient) Subscribe(ctx context.Context, in *telemetryv1.SubscriptionRequest, opts ...grpc.CallOption) (telemetryv1.Telemetry_SubscribeClient, error) {
	return &fakeSubStream{}, nil
}

func TestPublishBatch_OK(t *testing.T) {
	// Scenario: broker accepts all items with status OK
	// Input: batch of 3, response Accepted=3, Status=OK
	// Expect: accepted=3, backpressure=false, err=nil
	fc := &fakeTelemetryClient{resp: &telemetryv1.PublishResponse{Accepted: 3, Status: "OK"}}
	batch := []*telemetryv1.TelemetryData{{}, {}, {}}
	acc, bp, err := publishBatch(context.Background(), fc, batch)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if bp {
		t.Fatalf("expected no backpressure")
	}
	if acc != 3 {
		t.Fatalf("expected accepted=3 got %d", acc)
	}
}

func TestPublishBatch_BackpressurePartial(t *testing.T) {
	// Scenario: broker returns BACKPRESSURE after partially accepting some items
	// Input: batch of 5, response Accepted=2, Status=BACKPRESSURE
	// Expect: accepted=2, backpressure=true, err=nil
	fc := &fakeTelemetryClient{resp: &telemetryv1.PublishResponse{Accepted: 2, Status: "BACKPRESSURE"}}
	batch := []*telemetryv1.TelemetryData{{}, {}, {}, {}, {}}
	acc, bp, err := publishBatch(context.Background(), fc, batch)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !bp {
		t.Fatalf("expected backpressure=true")
	}
	if acc != 2 {
		t.Fatalf("expected accepted=2 got %d", acc)
	}
}

func TestPublishBatch_Error(t *testing.T) {
	// Scenario: broker call returns an error
	// Input: any batch, client error
	// Expect: err != nil
	fc := &fakeTelemetryClient{err: errors.New("network error")}
	batch := []*telemetryv1.TelemetryData{{}}
	_, _, err := publishBatch(context.Background(), fc, batch)
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestDrainRemaining_RetriesUntilAccepted(t *testing.T) {
	// Scenario: first call backpressures with partial accept; second call OK
	// Input: remaining of 3 items; script: [BACKPRESSURE acc=1, OK acc=2]
	// Expect: function returns after accepting all; total calls=2
	fc := &fakeTelemetryClient{script: []*telemetryv1.PublishResponse{
		{Accepted: 1, Status: "BACKPRESSURE"},
		{Accepted: 2, Status: "OK"},
	}}
	backoff := 1 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	remaining := []*telemetryv1.TelemetryData{{}, {}, {}}
	drainRemaining(ctx, fc, remaining, &backoff, 4*time.Millisecond)
	if fc.calls != 2 {
		t.Fatalf("expected 2 calls, got %d", fc.calls)
	}
}

func TestDrainRemaining_ResetsBackoffOnSuccess(t *testing.T) {
	// Scenario: backoff grows due to backpressure, then should reset to 100ms after success
	// Input: script BACKPRESSURE then OK; backoff starts at 200ms
	// Expect: backoff set to 100ms after drain completes
	fc := &fakeTelemetryClient{script: []*telemetryv1.PublishResponse{
		{Accepted: 0, Status: "BACKPRESSURE"},
		{Accepted: 2, Status: "OK"},
	}}
	backoff := 200 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	remaining := []*telemetryv1.TelemetryData{{}, {}}
	drainRemaining(ctx, fc, remaining, &backoff, 1*time.Second)
	if backoff != 100*time.Millisecond {
		t.Fatalf("expected backoff reset to 100ms, got %s", backoff)
	}
}

func TestToTelemetry_Mapping(t *testing.T) {
	// Scenario: CSV row with GPU id and two numeric metrics
	// Input: headers [gpu_id, temp, power], rec [gpu-1, 85.5, 250]
	// Expect: TelemetryData with GpuId=gpu-1 and metrics["temp"]=85.5, ["power"]=250
	headers := []string{"gpu_id", "temp", "power"}
	rec := []string{"gpu-1", "85.5", "250"}
	out := toTelemetry(headers, rec, "host-a", "producer-x")
	if out.GetGpuId() != "gpu-1" {
		t.Fatalf("gpu id mismatch: %s", out.GetGpuId())
	}
	if got := out.GetMetrics()["temp"]; got != 85.5 {
		t.Fatalf("temp metric mismatch: %v", got)
	}
	if got := out.GetMetrics()["power"]; got != 250 {
		t.Fatalf("power metric mismatch: %v", got)
	}
}
