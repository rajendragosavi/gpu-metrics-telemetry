package storage

import (
	"context"
	"fmt"
	"sort"
	"time"

	"gpu-metric-collector/internal/model"

	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	"github.com/influxdata/influxdb-client-go/v2/api"
)

// InfluxStore implements Store backed by InfluxDB v2.
type InfluxStore struct {
	client influxdb2.Client
	org    string
	bucket string
	wapi   api.WriteAPIBlocking
	qapi   api.QueryAPI
}

// NewInfluxStore builds a Store using InfluxDB v2 client.
// url example: http://localhost:8086
// org: your org name
// bucket: your bucket name
// token: auth token (PAT)
func NewInfluxStore(url, org, bucket, token string) (Store, error) {
	if url == "" || org == "" || bucket == "" || token == "" {
		return nil, fmt.Errorf("influx: missing url/org/bucket/token")
	}
	client := influxdb2.NewClient(url, token)
	st := &InfluxStore{
		client: client,
		org:    org,
		bucket: bucket,
		wapi:   client.WriteAPIBlocking(org, bucket),
		qapi:   client.QueryAPI(org),
	}
	return st, nil
}

func (s *InfluxStore) SaveTelemetry(t model.Telemetry) error {
	// measurement: telemetry
	// tag: gpu_id
	// fields: metrics map
	if len(t.Metrics) == 0 {
		// still write a heartbeat point so GPU is discoverable
		fields := map[string]interface{}{"_heartbeat": 1}
		p := influxdb2.NewPoint("telemetry", map[string]string{"gpu_id": t.GPUId}, fields, t.Timestamp)
		return s.wapi.WritePoint(context.Background(), p)
	}
	fields := make(map[string]interface{}, len(t.Metrics))
	for k, v := range t.Metrics {
		fields[k] = v
	}
	p := influxdb2.NewPoint("telemetry", map[string]string{"gpu_id": t.GPUId}, fields, t.Timestamp)
	return s.wapi.WritePoint(context.Background(), p)
}

func (s *InfluxStore) ListGPUs() ([]string, error) {
	// Query distinct tag values for gpu_id across data in bucket
	// Flux: from |> range(start: 0) |> filter(m == "telemetry") |> group(columns: ["gpu_id"]) |> distinct(column: "gpu_id")
	q := `from(bucket: "` + s.bucket + `")
  |> range(start: 0)
  |> filter(fn: (r) => r._measurement == "telemetry")
  |> keep(columns: ["gpu_id"]) 
  |> group()
  |> distinct(column: "gpu_id")`
	res, err := s.qapi.Query(context.Background(), q)
	if err != nil {
		return nil, fmt.Errorf("influx list gpus: %w", err)
	}
	defer res.Close()
	set := map[string]struct{}{}
	for res.Next() {
		// After distinct(column: "gpu_id"), the distinct value appears in _value
		v := res.Record().Value()
		if v == nil {
			continue
		}
		id, ok := v.(string)
		if !ok || id == "" {
			continue
		}
		set[id] = struct{}{}
	}
	if res.Err() != nil {
		return nil, fmt.Errorf("influx list gpus: %w", res.Err())
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

// timeLiteral returns a Flux time literal suitable for range(), e.g., time(v: "2026-01-27T00:00:00Z").
func timeLiteral(t time.Time) string {
	return fmt.Sprintf("time(v: %q)", t.UTC().Format(time.RFC3339))
}

func (s *InfluxStore) QueryTelemetry(gpuID string, start, end *time.Time) ([]model.Telemetry, error) {
	if gpuID == "" {
		return nil, fmt.Errorf("gpuID required")
	}
	startExpr := "0"
	if start != nil {
		startExpr = timeLiteral(*start)
	}
	stopExpr := ""
	if end != nil {
		stopExpr = ", stop: " + timeLiteral(*end)
	}
	// Pivot fields so each timestamp becomes one row with all metric columns
	q := fmt.Sprintf(`from(bucket: "%s")
  |> range(start: %s%s)
  |> filter(fn: (r) => r._measurement == "telemetry" and r.gpu_id == "%s")
  |> pivot(rowKey:["_time"], columnKey:["_field"], valueColumn:"_value")
  |> sort(columns: ["_time"], desc: false)
`, s.bucket, startExpr, stopExpr, gpuID)
	res, err := s.qapi.Query(context.Background(), q)
	if err != nil {
		return nil, fmt.Errorf("influx query: %w; flux=%s", err, q)
	}
	defer res.Close()
	var out []model.Telemetry
	for res.Next() {
		rec := res.Record()
		ts := rec.Time().UTC()
		metrics := map[string]float64{}
		// Collect all columns except metadata
		for k, v := range rec.Values() {
			if k == "_time" || k == "_measurement" || k == "result" || k == "table" || k == "gpu_id" {
				continue
			}
			switch val := v.(type) {
			case int64:
				metrics[k] = float64(val)
			case float64:
				metrics[k] = val
			case int32:
				metrics[k] = float64(val)
			case uint64:
				metrics[k] = float64(val)
			case uint32:
				metrics[k] = float64(val)
			}
		}
		out = append(out, model.Telemetry{GPUId: gpuID, Timestamp: ts, Metrics: metrics})
	}
	if err := res.Err(); err != nil {
		return nil, fmt.Errorf("influx query: %w", err)
	}
	return out, nil
}

func timeToRFC3339(t time.Time) string {
	return fmt.Sprintf("%q", t.UTC().Format(time.RFC3339))
}
