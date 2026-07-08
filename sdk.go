package replicate

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/luxfi/age"
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
//	REPLICATE_S3_PATH      — key prefix (default: hostname)
//	REPLICATE_S3_REGION    — S3 region (default: "us-central1")
//	REPLICATE_AGE_RECIPIENT — age public key for encryption (REQUIRED unless
//	                          REPLICATE_ALLOW_PLAINTEXT=true; sourced from KMS)
//	REPLICATE_AGE_IDENTITY  — age private key for restore/decrypt
//	REPLICATE_ALLOW_PLAINTEXT — opt out of mandatory encryption for
//	                          non-sensitive local/dev targets (default: false)
//	REPLICATE_SYNC_INTERVAL — WAL sync interval (default: "1s")
//
// Encryption is fail-closed: when REPLICATE_S3_ENDPOINT is set, replication
// REFUSES to start (loud log, no-op) unless a valid age recipient is configured
// or REPLICATE_ALLOW_PLAINTEXT=true is explicitly set. It never silently streams
// plaintext LTX to S3.
//
// Returns a stop function that gracefully shuts down replication.
// Returns a no-op function if REPLICATE_S3_ENDPOINT is not set or if the
// fail-closed encryption policy is violated.
func AutoReplicate(dbPath string) func() {
	endpoint := os.Getenv("REPLICATE_S3_ENDPOINT")
	if endpoint == "" {
		return func() {} // no-op
	}

	bucket := envOr("REPLICATE_S3_BUCKET", "replicate")
	prefix := os.Getenv("REPLICATE_S3_PATH")
	if prefix == "" {
		prefix, _ = os.Hostname()
	}
	region := envOr("REPLICATE_S3_REGION", "us-central1")
	syncInterval := parseDur("REPLICATE_SYNC_INTERVAL", DefaultSyncInterval)

	// Propagate REPLICATE_S3_* to AWS_* for the S3 client.
	if ak := os.Getenv("REPLICATE_S3_ACCESS_KEY"); ak != "" {
		os.Setenv("AWS_ACCESS_KEY_ID", ak)
	}
	if sk := os.Getenv("REPLICATE_S3_SECRET_KEY"); sk != "" {
		os.Setenv("AWS_SECRET_ACCESS_KEY", sk)
	}

	// Build S3 replica URL: s3://bucket/path?endpoint=X&region=Y&force-path-style=true
	replicaURL := fmt.Sprintf("s3://%s/%s?endpoint=%s&region=%s&force-path-style=true",
		url.PathEscape(bucket),
		url.PathEscape(prefix),
		url.QueryEscape(endpoint),
		url.QueryEscape(region),
	)

	client, err := NewReplicaClientFromURL(replicaURL)
	if err != nil {
		slog.Error("replicate: invalid S3 config", "url", replicaURL, "error", err)
		return func() {}
	}

	// Build the DB + Replica.
	db := NewDB(dbPath)

	replica := NewReplicaWithClient(db, client)
	replica.SyncInterval = syncInterval

	// Fail-closed encryption policy. By default replicate REFUSES to stream
	// plaintext to S3: an age recipient (public key, sourced from the
	// KMS-synced REPLICATE_AGE_RECIPIENT) is mandatory. Set
	// REPLICATE_ALLOW_PLAINTEXT=true ONLY for non-sensitive local/dev targets.
	allowPlaintext := boolEnv("REPLICATE_ALLOW_PLAINTEXT")
	replica.RequireEncryption = !allowPlaintext

	// Parse age recipients for encryption.
	if recipientStr := os.Getenv("REPLICATE_AGE_RECIPIENT"); recipientStr != "" {
		rcs, err := age.ParseRecipients(strings.NewReader(recipientStr))
		if err != nil {
			// Fail closed: a malformed recipient must never silently downgrade
			// to plaintext. Refuse to start replication so the misconfiguration
			// is loud, not a clear-text money/identity backup.
			slog.Error("replicate: invalid REPLICATE_AGE_RECIPIENT — refusing to start (fix the KMS age recipient, or set REPLICATE_ALLOW_PLAINTEXT=true for non-sensitive data)", "error", err)
			return func() {}
		}
		replica.AgeRecipients = rcs
	}

	// Parse age identity for decryption (restore).
	if identityStr := os.Getenv("REPLICATE_AGE_IDENTITY"); identityStr != "" {
		ids, err := age.ParseIdentities(strings.NewReader(identityStr))
		if err != nil {
			slog.Error("replicate: invalid REPLICATE_AGE_IDENTITY — refusing to start (fix the KMS age identity)", "error", err)
			return func() {}
		}
		replica.AgeIdentities = ids
	}

	// Enforce the fail-closed policy before opening: no recipient while
	// encryption is required => refuse to stream rather than emit plaintext.
	if replica.RequireEncryption && len(replica.AgeRecipients) == 0 {
		slog.Error("replicate: REPLICATE_AGE_RECIPIENT is not set — refusing to stream plaintext",
			"s3", "s3://"+bucket+"/"+prefix,
			"hint", "wire the KMS-synced age recipient, or set REPLICATE_ALLOW_PLAINTEXT=true for non-sensitive data")
		return func() {}
	}

	db.Replica = replica

	if err := db.Open(); err != nil {
		slog.Error("replicate: failed to open", "db", dbPath, "error", err)
		return func() {}
	}

	slog.Info("replicate: streaming",
		"db", dbPath,
		"s3", "s3://"+bucket+"/"+prefix,
		"sync", syncInterval,
		"encrypted", len(replica.AgeRecipients) > 0,
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

// boolEnv parses a boolean environment variable, returning false when unset or
// unparseable. Used for the fail-closed REPLICATE_ALLOW_PLAINTEXT opt-out.
func boolEnv(key string) bool {
	v, _ := strconv.ParseBool(os.Getenv(key))
	return v
}

func parseDur(envKey string, fallback time.Duration) time.Duration {
	if v := os.Getenv(envKey); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
