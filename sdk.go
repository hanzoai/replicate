package replicate

import (
	"context"
	"log/slog"
	"os"
	"time"
)

// AutoReplicate is the one-line SDK entry point for any Go app.
// Starts streaming E2E PQ-encrypted replication of a SQLite database to S3.
//
// Usage:
//
//	stop := replicate.AutoReplicate("/app/data/data.db")
//	defer stop()
//
// Reads all config from env vars:
//
//	REPLICATE_S3_ENDPOINT  — S3 endpoint (required, no-op if empty)
//	REPLICATE_S3_BUCKET    — bucket name (default: "replicate")
//	REPLICATE_S3_PATH      — key prefix (default: hostname/dbname)
//	REPLICATE_AGE_RECIPIENT — age public key for PQ encryption
//	REPLICATE_SYNC_INTERVAL — WAL sync interval (default: "1s")
//	REPLICATE_SNAPSHOT_INTERVAL — full snapshot interval (default: "1h")
//
// Returns a stop function that gracefully shuts down replication.
// Returns a no-op function if REPLICATE_S3_ENDPOINT is not set.
func AutoReplicate(dbPath string) func() {
	endpoint := os.Getenv("REPLICATE_S3_ENDPOINT")
	if endpoint == "" {
		return func() {} // no-op
	}

	bucket := envOr("REPLICATE_S3_BUCKET", "replicate")
	prefix := os.Getenv("REPLICATE_S3_PATH")
	if prefix == "" {
		hostname, _ := os.Hostname()
		prefix = hostname
	}

	syncInterval := parseDur("REPLICATE_SYNC_INTERVAL", time.Second)
	snapInterval := parseDur("REPLICATE_SNAPSHOT_INTERVAL", time.Hour)

	// S3 credentials
	if ak := os.Getenv("REPLICATE_S3_ACCESS_KEY"); ak != "" {
		os.Setenv("AWS_ACCESS_KEY_ID", ak)
	}
	if sk := os.Getenv("REPLICATE_S3_SECRET_KEY"); sk != "" {
		os.Setenv("AWS_SECRET_ACCESS_KEY", sk)
	}

	db := NewDB(dbPath)

	replica := NewReplica(db, "s3")
	replica.Bucket = bucket
	replica.Endpoint = endpoint
	replica.Region = envOr("REPLICATE_S3_REGION", "us-central1")
	replica.Path = prefix + "/" + dbPath
	replica.ForcePathStyle = true
	replica.SyncInterval = syncInterval
	replica.SnapshotInterval = snapInterval

	if recipient := os.Getenv("REPLICATE_AGE_RECIPIENT"); recipient != "" {
		replica.AgeRecipients = []string{recipient}
	}

	db.Replicas = append(db.Replicas, replica)

	if err := db.Open(); err != nil {
		slog.Error("replicate: failed", "db", dbPath, "error", err)
		return func() {}
	}

	slog.Info("replicate: streaming",
		"db", dbPath,
		"s3", "s3://"+bucket+"/"+prefix,
		"sync", syncInterval,
	)

	return func() {
		_ = db.Close(context.Background())
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseDur(envKey string, fallback time.Duration) time.Duration {
	if v := os.Getenv(envKey); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
