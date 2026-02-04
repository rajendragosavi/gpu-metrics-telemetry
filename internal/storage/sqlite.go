package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"gpu-metric-collector/internal/model"

	_ "modernc.org/sqlite"
)

// SQLiteStore implements Store backed by a single table with JSON metrics.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore opens (and initializes) an SQLite database.
// Example DSN: file:gpu-telemetry.db?_busy_timeout=5000
func NewSQLiteStore(dsn string) (Store, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := initSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &SQLiteStore{db: db}, nil
}

func initSchema(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS telemetry (
  gpu_id TEXT NOT NULL,
  ts INTEGER NOT NULL,
  metrics TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_telemetry_gpu_ts ON telemetry(gpu_id, ts);
`)
	if err != nil {
		return fmt.Errorf("init schema: %w", err)
	}
	return nil
}

func (s *SQLiteStore) SaveTelemetry(t model.Telemetry) error {
	b, err := json.Marshal(t.Metrics)
	if err != nil {
		return fmt.Errorf("marshal metrics: %w", err)
	}
	_, err = s.db.Exec(`INSERT INTO telemetry(gpu_id, ts, metrics) VALUES(?, ?, ?)`, t.GPUId, t.Timestamp.Unix(), string(b))
	if err != nil {
		return fmt.Errorf("insert telemetry: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ListGPUs() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT gpu_id FROM telemetry ORDER BY gpu_id`)
	if err != nil {
		return nil, fmt.Errorf("list gpus: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) QueryTelemetry(gpuID string, start, end *time.Time) ([]model.Telemetry, error) {
	q := `SELECT ts, metrics FROM telemetry WHERE gpu_id = ?`
	args := []any{gpuID}
	if start != nil {
		q += ` AND ts >= ?`
		args = append(args, start.Unix())
	}
	if end != nil {
		q += ` AND ts <= ?`
		args = append(args, end.Unix())
	}
	q += ` ORDER BY ts ASC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("query telemetry: %w", err)
	}
	defer rows.Close()
	var out []model.Telemetry
	for rows.Next() {
		var ts int64
		var mjson string
		if err := rows.Scan(&ts, &mjson); err != nil {
			return nil, err
		}
		m := map[string]float64{}
		if err := json.Unmarshal([]byte(mjson), &m); err != nil {
			return nil, fmt.Errorf("unmarshal metrics: %w", err)
		}
		out = append(out, model.Telemetry{GPUId: gpuID, Timestamp: time.Unix(ts, 0).UTC(), Metrics: m})
	}
	return out, rows.Err()
}
