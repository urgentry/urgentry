package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// MetricBucket represents a single custom metric data point.
type MetricBucket struct {
	ID        string
	ProjectID string
	Name      string
	Type      string // "c" (counter), "d" (distribution), "g" (gauge), "s" (set)
	Value     float64
	Unit      string
	Tags      map[string]string
	Timestamp time.Time
}

// MetricBucketStore persists custom metric buckets in SQLite.
type MetricBucketStore struct {
	db *sql.DB
}

// NewMetricBucketStore creates a SQLite-backed metric bucket store.
func NewMetricBucketStore(db *sql.DB) *MetricBucketStore {
	return &MetricBucketStore{db: db}
}

// SaveMetricBucket persists a single metric bucket.
func (s *MetricBucketStore) SaveMetricBucket(ctx context.Context, bucket *MetricBucket) error {
	if bucket.ID == "" {
		bucket.ID = generateID()
	}
	tagsJSON, _ := json.Marshal(bucket.Tags)
	ts := bucket.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO metric_buckets
			(id, project_id, name, type, value, unit, tags_json, timestamp)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		bucket.ID,
		bucket.ProjectID,
		bucket.Name,
		bucket.Type,
		bucket.Value,
		bucket.Unit,
		string(tagsJSON),
		ts.UTC().Format(time.RFC3339),
	)
	return err
}

// SaveMetricBuckets persists multiple metric buckets in a single transaction.
func (s *MetricBucketStore) SaveMetricBuckets(ctx context.Context, buckets []*MetricBucket) error {
	if len(buckets) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx,
		`INSERT OR IGNORE INTO metric_buckets
			(id, project_id, name, type, value, unit, tags_json, timestamp)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()

	for _, bucket := range buckets {
		if bucket.ID == "" {
			bucket.ID = generateID()
		}
		tagsJSON, _ := json.Marshal(bucket.Tags)
		ts := bucket.Timestamp
		if ts.IsZero() {
			ts = time.Now().UTC()
		}
		if _, err := stmt.ExecContext(ctx,
			bucket.ID,
			bucket.ProjectID,
			bucket.Name,
			bucket.Type,
			bucket.Value,
			bucket.Unit,
			string(tagsJSON),
			ts.UTC().Format(time.RFC3339),
		); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}
