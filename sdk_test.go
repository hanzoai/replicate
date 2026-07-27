package replicate_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/hanzoai/sqlite"

	"github.com/hanzoai/replicate"
	_ "github.com/hanzoai/replicate/file"
	"github.com/hanzoai/replicate/internal/testingutil"
)

// TestReplicaURL pins the value the whole SDK is configured by: "" means
// replication is off, and an explicit prefix beats the process-wide env one —
// which is what lets one process replicate many files to many destinations.
func TestReplicaURL(t *testing.T) {
	t.Run("unset endpoint is off", func(t *testing.T) {
		t.Setenv("REPLICATE_S3_ENDPOINT", "")
		if got := replicate.ReplicaURL("anything"); got != "" {
			t.Fatalf("ReplicaURL = %q, want \"\" when REPLICATE_S3_ENDPOINT is unset", got)
		}
	})

	t.Run("env prefix", func(t *testing.T) {
		t.Setenv("REPLICATE_S3_ENDPOINT", "https://s3.example.com")
		t.Setenv("REPLICATE_S3_BUCKET", "tenants")
		t.Setenv("REPLICATE_S3_PATH", "hanzo")
		t.Setenv("REPLICATE_S3_REGION", "us-central1")

		const want = "s3://tenants/hanzo?endpoint=https%3A%2F%2Fs3.example.com&region=us-central1&force-path-style=true"
		if got := replicate.ReplicaURL(""); got != want {
			t.Fatalf("ReplicaURL(\"\") = %q, want %q", got, want)
		}
		if got := replicate.ReplicaURL("org/acme"); !strings.Contains(got, "org%2Facme") {
			t.Fatalf("ReplicaURL(%q) = %q, want the explicit prefix, not the env one", "org/acme", got)
		}
	})
}

// TestStreamRestoreRoundTrip is the durability proof the SDK previously could
// not make: it could push a database out but had no way to pull one back
// without reaching into Replica internals. Push, lose the node, pull it back.
//
// It is also the regression test for a handle that lives less than one
// SyncInterval: the write below happens after Stream and the handle is closed
// milliseconds later, which used to replicate nothing at all.
func TestStreamRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	t.Setenv("REPLICATE_ALLOW_PLAINTEXT", "true")

	dir := t.TempDir()
	local := filepath.Join(dir, "local")
	if err := os.MkdirAll(local, 0o700); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(local, "data.db")
	replicaURL := "file://" + filepath.Join(dir, "replica")

	// A live database with data in it.
	sqldb := testingutil.MustOpenSQLDB(t, dbPath)
	if _, err := sqldb.ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	rdb, err := replicate.Stream(dbPath, replicaURL)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if _, err := sqldb.ExecContext(ctx, `INSERT INTO t (v) VALUES ('durable')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// Close flushes a final sync, which is the only checkpoint an evicting
	// caller gets: whatever is not shipped by then is lost.
	if err := rdb.Close(ctx); err != nil {
		t.Fatalf("close stream: %v", err)
	}
	testingutil.MustCloseSQLDB(t, sqldb)

	// Lose the node: everything local is gone, the replica is all that is left.
	if err := os.RemoveAll(local); err != nil {
		t.Fatalf("drop local dir: %v", err)
	}

	restored, err := replicate.Restore(ctx, dbPath, replicaURL)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if !restored {
		t.Fatal("Restore reported nothing to restore, but a database was streamed to this URL")
	}

	back := testingutil.MustOpenSQLDB(t, dbPath)
	defer testingutil.MustCloseSQLDB(t, back)
	var v string
	if err := back.QueryRowContext(ctx, `SELECT v FROM t WHERE id = 1`).Scan(&v); err != nil {
		t.Fatalf("read restored row: %v", err)
	}
	if v != "durable" {
		t.Fatalf("restored value = %q, want %q", v, "durable")
	}
}

// TestRestoreEmptyDestination: a destination nothing has ever been replicated to
// is a cold start, not a failure. Callers branch on the bool to decide "restore
// or start empty", so an error here would make every new database an incident.
func TestRestoreEmptyDestination(t *testing.T) {
	t.Setenv("REPLICATE_ALLOW_PLAINTEXT", "true")
	dir := t.TempDir()

	restored, err := replicate.Restore(context.Background(),
		filepath.Join(dir, "data.db"), "file://"+filepath.Join(dir, "empty-replica"))
	if err != nil {
		t.Fatalf("Restore against an empty destination: %v", err)
	}
	if restored {
		t.Fatal("Restore claimed it restored something from an empty destination")
	}
	if _, err := os.Stat(filepath.Join(dir, "data.db")); !os.IsNotExist(err) {
		t.Fatalf("nothing was restored, so no file should exist: %v", err)
	}
}

// TestStreamFailsClosedWithoutRecipient: the fail-closed encryption policy is
// enforced for every entry point, not just AutoReplicate. Stream reports it
// instead of logging and no-oping, so a caller that must not serve undurable
// data can refuse.
func TestStreamFailsClosedWithoutRecipient(t *testing.T) {
	t.Setenv("REPLICATE_ALLOW_PLAINTEXT", "")
	t.Setenv("REPLICATE_AGE_RECIPIENT", "")

	dir := t.TempDir()
	_, err := replicate.Stream(filepath.Join(dir, "data.db"), "file://"+filepath.Join(dir, "replica"))
	if err == nil {
		t.Fatal("Stream started with no age recipient and no plaintext opt-out")
	}
	if !strings.Contains(err.Error(), "REPLICATE_AGE_RECIPIENT") {
		t.Fatalf("error should name the missing setting, got: %v", err)
	}

	_, err = replicate.Restore(context.Background(),
		filepath.Join(dir, "data.db"), "file://"+filepath.Join(dir, "replica"))
	if err == nil {
		t.Fatal("Restore ran under a violated encryption policy")
	}
}
