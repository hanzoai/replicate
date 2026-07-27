package replicate

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
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
//
// AutoReplicate is [Stream] at the env-configured destination. An app that
// replicates MANY files — one per tenant, say — cannot use one env-wide
// destination for all of them and calls [Stream] and [Restore] with a per-file
// URL from [ReplicaURL] instead.
func AutoReplicate(dbPath string) func() {
	// The env prefix is the whole destination for this process, so an unset one
	// falls back to the hostname to keep two hosts from writing one history.
	prefix := os.Getenv("REPLICATE_S3_PATH")
	if prefix == "" {
		prefix, _ = os.Hostname()
	}

	replicaURL := ReplicaURL(prefix)
	if replicaURL == "" {
		return func() {} // replication not configured
	}

	db, err := Stream(dbPath, replicaURL)
	if err != nil {
		// Never fatal here: AutoReplicate is a side-car capability bolted onto a
		// running app, and killing the app is not its call. Callers that must
		// refuse to serve without durability use Stream directly and see the error.
		slog.Error("replicate: not streaming", "db", dbPath, "error", err)
		return func() {}
	}

	slog.Info("replicate: streaming",
		"db", dbPath,
		"url", replicaURL,
		"sync", db.Replica.SyncInterval,
		"encrypted", len(db.Replica.AgeRecipients) > 0,
	)

	return func() {
		_ = db.Close(context.Background())
	}
}

// ReplicaURL returns the replica URL for a remote key prefix, built from the
// REPLICATE_S3_* environment. It returns "" when REPLICATE_S3_ENDPOINT is unset
// — the one way every entry point spells "replication is not configured".
//
// An empty prefix means REPLICATE_S3_PATH. A caller that needs a sub-tree — one
// prefix per tenant file, say — passes it explicitly rather than rewriting the
// env, because the env is process-wide and a per-file destination is not.
func ReplicaURL(prefix string) string {
	endpoint := os.Getenv("REPLICATE_S3_ENDPOINT")
	if endpoint == "" {
		return ""
	}
	if prefix == "" {
		prefix = os.Getenv("REPLICATE_S3_PATH")
	}
	return fmt.Sprintf("s3://%s/%s?endpoint=%s&region=%s&force-path-style=true",
		url.PathEscape(envOr("REPLICATE_S3_BUCKET", "replicate")),
		url.PathEscape(prefix),
		url.QueryEscape(endpoint),
		url.QueryEscape(envOr("REPLICATE_S3_REGION", "us-central1")),
	)
}

// Stream starts streaming the SQLite database at dbPath to replicaURL and
// returns the running handle; Close flushes a final sync and stops it.
//
// It is AutoReplicate with an explicit destination: everything except WHERE the
// data goes still comes from the REPLICATE_* env, so the fail-closed encryption
// policy is applied in one place for every caller. Unlike AutoReplicate it
// returns the reason it did not start, because a caller that picked its own
// destination is in a position to refuse to serve without it.
//
// One Stream per database FILE. A replica URL names a key prefix and its LTX
// history belongs to a single database; two databases pointed at one prefix
// overwrite each other's history.
func Stream(dbPath, replicaURL string) (*DB, error) {
	r, err := newReplica(dbPath, replicaURL)
	if err != nil {
		return nil, err
	}
	db := r.DB()
	db.Replica = r
	if err := db.Open(); err != nil {
		return nil, fmt.Errorf("replicate: open %s: %w", dbPath, err)
	}

	// Sync once, here, or a short-lived handle replicates NOTHING: Close's final
	// sync is guarded on the database having been initialised, and initialisation
	// otherwise happens on the monitor's first tick — a whole SyncInterval away.
	// Open, write, Close inside that window (an evicted tenant, a one-shot job)
	// silently loses everything. The first sync also makes the file durable
	// immediately rather than up to an interval later.
	if err := db.Sync(context.Background()); err != nil {
		_ = db.Close(context.Background())
		return nil, fmt.Errorf("replicate: initial sync %s: %w", dbPath, err)
	}
	return db, nil
}

// Restore pulls the newest replicated state at replicaURL into dbPath and
// reports whether there was anything to pull. (false, nil) means the replica
// holds nothing yet — a database that has never been replicated, which is a
// normal cold start, not an error. dbPath must not already exist: restore never
// overwrites a live local file.
//
// The fail-closed encryption policy is checked here too, so a process that could
// not have replicated this file does not quietly serve it: refusing at restore
// is refusing to take custody of data it cannot make durable.
func Restore(ctx context.Context, dbPath, replicaURL string) (bool, error) {
	r, err := newReplica(dbPath, replicaURL)
	if err != nil {
		return false, err
	}
	opt := NewRestoreOptions()
	opt.OutputPath = dbPath
	if err := r.Restore(ctx, opt); err != nil {
		// Three spellings of one condition: nothing has ever been replicated
		// here. ErrTxNotAvailable is what the current restore planner returns for
		// an empty destination, ErrNoSnapshots what the v3 path returns, and
		// fs.ErrNotExist what the file client returns for a prefix no one has
		// written to yet. A cold start is not an error.
		if errors.Is(err, ErrTxNotAvailable) || errors.Is(err, ErrNoSnapshots) || errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// newReplica connects dbPath to replicaURL, applying the REPLICATE_* policy: S3
// credentials, sync interval, and the fail-closed age encryption rule. Stream
// and Restore both go through it so that rule cannot drift between the write
// path and the read path — a drift whose failure mode is a plaintext copy of
// customer data sitting in a bucket.
//
// It performs no file or network I/O, so building a replica is also how a caller
// validates its configuration before it opens anything.
func newReplica(dbPath, replicaURL string) (*Replica, error) {
	if dbPath == "" {
		return nil, errors.New("replicate: database path required")
	}
	if replicaURL == "" {
		return nil, errors.New("replicate: replica URL required")
	}

	// The S3 client reads AWS_*; REPLICATE_S3_* is what hanzo services set.
	if ak := os.Getenv("REPLICATE_S3_ACCESS_KEY"); ak != "" {
		os.Setenv("AWS_ACCESS_KEY_ID", ak)
	}
	if sk := os.Getenv("REPLICATE_S3_SECRET_KEY"); sk != "" {
		os.Setenv("AWS_SECRET_ACCESS_KEY", sk)
	}

	client, err := NewReplicaClientFromURL(replicaURL)
	if err != nil {
		return nil, fmt.Errorf("replicate: bad replica URL %q: %w", replicaURL, err)
	}

	r := NewReplicaWithClient(NewDB(dbPath), client)
	r.SyncInterval = parseDur("REPLICATE_SYNC_INTERVAL", DefaultSyncInterval)

	// Fail-closed encryption policy. By default replicate REFUSES to stream
	// plaintext: an age recipient (public key, sourced from the KMS-synced
	// REPLICATE_AGE_RECIPIENT) is mandatory. Set REPLICATE_ALLOW_PLAINTEXT=true
	// ONLY for non-sensitive local/dev targets.
	r.RequireEncryption = !boolEnv("REPLICATE_ALLOW_PLAINTEXT")

	if s := os.Getenv("REPLICATE_AGE_RECIPIENT"); s != "" {
		rcs, err := age.ParseRecipients(strings.NewReader(s))
		if err != nil {
			// A malformed recipient must never silently downgrade to plaintext.
			return nil, fmt.Errorf("replicate: invalid REPLICATE_AGE_RECIPIENT (fix the KMS age recipient, or set REPLICATE_ALLOW_PLAINTEXT=true for non-sensitive data): %w", err)
		}
		r.AgeRecipients = rcs
	}
	if s := os.Getenv("REPLICATE_AGE_IDENTITY"); s != "" {
		ids, err := age.ParseIdentities(strings.NewReader(s))
		if err != nil {
			return nil, fmt.Errorf("replicate: invalid REPLICATE_AGE_IDENTITY (fix the KMS age identity): %w", err)
		}
		r.AgeIdentities = ids
	}
	if r.RequireEncryption && len(r.AgeRecipients) == 0 {
		return nil, fmt.Errorf("replicate: REPLICATE_AGE_RECIPIENT is not set — refusing to stream plaintext to %s (wire the KMS-synced age recipient, or set REPLICATE_ALLOW_PLAINTEXT=true for non-sensitive data)", replicaURL)
	}
	return r, nil
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
