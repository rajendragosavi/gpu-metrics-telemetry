package main

import (
	"testing"
	"time"

	telemetryv1 "gpu-metric-collector/api/gen"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// validate() tests
func TestValidate_Nil(t *testing.T) {
	// Scenario: nil message
	// Input: nil
	// Expect: validate=false
	if validate(nil) {
		t.Fatalf("expected false for nil message")
	}
}

func TestValidate_EmptyGPU(t *testing.T) {
	// Scenario: empty gpu id
	// Input: TelemetryData without GpuId
	// Expect: validate=false
	m := &telemetryv1.TelemetryData{}
	if validate(m) {
		t.Fatalf("expected false for empty gpu id")
	}
}

func TestValidate_NoTimestamp(t *testing.T) {
	// Scenario: gpu id but nil timestamp
	// Input: TelemetryData{GpuId: "g1"}
	// Expect: validate=false
	m := &telemetryv1.TelemetryData{GpuId: "g1"}
	if validate(m) {
		t.Fatalf("expected false for nil timestamp")
	}
}

func TestValidate_OK(t *testing.T) {
	// Scenario: valid gpu id and timestamp
	// Input: TelemetryData with GpuId and Ts
	// Expect: validate=true
	ts := time.Now()
	m := &telemetryv1.TelemetryData{GpuId: "g1", Ts: timestamppb.New(ts)}
	if !validate(m) {
		t.Fatalf("expected true for valid message")
	}
}

// toModel() mapping
func TestToModel_Mapping(t *testing.T) {
	// Scenario: map metrics and timestamp correctly
	// Input: TelemetryData with metrics and ts
	// Expect: same gpu id, timestamp equality, metrics equality
	ts := time.Now()
	m := &telemetryv1.TelemetryData{
		GpuId: "g1",
		Ts:    timestamppb.New(ts),
		Metrics: map[string]float64{
			"temp":  85.5,
			"power": 250,
		},
	}
	got := toModel(m)
	if got.GPUId != "g1" {
		t.Fatalf("gpu id mismatch: %s", got.GPUId)
	}
	if !got.Timestamp.Equal(ts) {
		t.Fatalf("timestamp mismatch: %v vs %v", got.Timestamp, ts)
	}
	if got.Metrics["temp"] != 85.5 || got.Metrics["power"] != 250 {
		t.Fatalf("metrics mismatch: %#v", got.Metrics)
	}
}

func TestValidate_WhitespaceGPU(t *testing.T) {
	// Scenario: gpu id contains only whitespace
	// Input: TelemetryData{GpuId: "   ", Ts: now}
	// Expect: validate=false
	m := &telemetryv1.TelemetryData{GpuId: "   ", Ts: timestamppb.Now()}
	if validate(m) {
		t.Fatalf("expected false for whitespace gpu id")
	}
}

func TestToModel_DeepCopyMetrics(t *testing.T) {
	// Scenario: after toModel(), mutating source metrics should not affect model copy
	// Expect: got.Metrics remains with original values
	m := &telemetryv1.TelemetryData{
		GpuId:   "g1",
		Ts:      timestamppb.Now(),
		Metrics: map[string]float64{"temp": 70},
	}
	got := toModel(m)
	m.Metrics["temp"] = 999
	if got.Metrics["temp"] != 70 {
		t.Fatalf("expected deep copy to preserve 70, got %v", got.Metrics["temp"])
	}
}
