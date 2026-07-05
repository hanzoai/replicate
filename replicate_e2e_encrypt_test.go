package replicate_test

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/luxfi/age"
	_ "modernc.org/sqlite"

	"github.com/hanzoai/replicate"
	"github.com/hanzoai/replicate/file"
	"github.com/hanzoai/replicate/internal/testingutil"
)

// TestEndToEndEncryptedReplicationRoundTrip is the full-stack proof that a real
// replicate run writes age-encrypted objects (never LTX1 plaintext) to its
// destination and that restore decrypts them back to a valid SQLite database.
// It exercises both the L0 WAL sync path (Replica.WriteLTXFile) and the DB
// snapshot path (previously a plaintext bypass of the encrypt gate).
func TestEndToEndEncryptedReplicationRoundTrip(t *testing.T) {
	ctx := context.Background()

	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	db := testingutil.NewDB(t, filepath.Join(dir, "db"))
	db.MonitorInterval = 0
	db.ShutdownSyncTimeout = 0

	replicaPath := filepath.Join(dir, "replica")
	rep := replicate.NewReplicaWithClient(db, file.NewReplicaClient(replicaPath))
	rep.MonitorEnabled = false
	rep.RequireEncryption = true
	rep.AgeRecipients = []age.Recipient{identity.Recipient()}
	rep.AgeIdentities = []age.Identity{identity}
	db.Replica = rep

	if err := db.Open(); err != nil {
		t.Fatal(err)
	}

	sqldb := testingutil.MustOpenSQLDB(t, db.Path())
	if _, err := sqldb.ExecContext(ctx, `CREATE TABLE ledger (id INTEGER PRIMARY KEY, memo TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	const rows = 128
	for i := 0; i < rows; i++ {
		if _, err := sqldb.ExecContext(ctx, `INSERT INTO ledger (memo) VALUES (?)`, secretMemo(i)); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
		// Split across two L0 syncs so more than one encrypted object is produced.
		if i == rows/2 {
			mustSync(t, ctx, db)
		}
	}
	mustSync(t, ctx, db)

	// Snapshot exercises the previously-plaintext DB.Snapshot bypass path.
	if _, err := db.Snapshot(ctx); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if err := db.Replica.Sync(ctx); err != nil {
		t.Fatalf("post-snapshot sync: %v", err)
	}

	// Every object written to the destination must be age ciphertext, not
	// plaintext LTX (LTX1) or a raw SQLite database.
	var inspected int
	err = filepath.WalkDir(replicaPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		head, err := readHead(path, 64)
		if err != nil {
			return err
		}
		if len(head) == 0 {
			return nil // skip empty marker/metadata files
		}
		rel, _ := filepath.Rel(replicaPath, path)
		if !hasPrefix(head, ageIntro) {
			t.Errorf("object %s is not age ciphertext: head=%q", rel, string(truncate(head, 24)))
			return nil
		}
		if hasPrefix(head, ltxMagic) {
			t.Errorf("object %s begins with LTX1 plaintext magic", rel)
		}
		if hasPrefix(head, "SQLite format 3\x00") {
			t.Errorf("object %s is a raw SQLite database", rel)
		}
		inspected++
		return nil
	})
	if err != nil {
		t.Fatalf("walk replica dir: %v", err)
	}
	if inspected == 0 {
		t.Fatal("no LTX objects were written to the destination — nothing to prove")
	}
	t.Logf("verified %d destination objects are age-encrypted (not LTX1/SQLite)", inspected)

	testingutil.MustCloseDBs(t, db, sqldb)

	// Restore: age-decrypt the destination back into a fresh SQLite database and
	// verify the data survived the encrypt -> S3 -> decrypt round-trip.
	restorePath := filepath.Join(dir, "restore.db")
	if err := rep.Restore(ctx, replicate.RestoreOptions{OutputPath: restorePath}); err != nil {
		t.Fatalf("restore: %v", err)
	}

	restored, err := sql.Open("sqlite", restorePath)
	if err != nil {
		t.Fatalf("open restored db: %v", err)
	}
	defer restored.Close()

	var got int
	if err := restored.QueryRowContext(ctx, `SELECT COUNT(*) FROM ledger`).Scan(&got); err != nil {
		t.Fatalf("count restored rows: %v", err)
	}
	if got != rows {
		t.Fatalf("restored row count = %d, want %d", got, rows)
	}
	var memo string
	if err := restored.QueryRowContext(ctx, `SELECT memo FROM ledger WHERE id = 1`).Scan(&memo); err != nil {
		t.Fatalf("read restored row: %v", err)
	}
	if memo != secretMemo(0) {
		t.Fatalf("restored memo = %q, want %q", memo, secretMemo(0))
	}
}

// TestDBSyncFailsClosedWhenEncryptionRequiredWithoutRecipient proves the DB
// write path fails closed: a monitored replica that must encrypt but has no
// recipient rejects every sync (db.Sync -> db.init -> Replica.Start guard) with
// ErrEncryptionRequired. In production the sync monitor logs this loudly every
// interval and never streams a byte of plaintext.
func TestDBSyncFailsClosedWhenEncryptionRequiredWithoutRecipient(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db := testingutil.NewDB(t, filepath.Join(dir, "db"))
	db.MonitorInterval = 0
	db.ShutdownSyncTimeout = 0

	rep := replicate.NewReplicaWithClient(db, file.NewReplicaClient(filepath.Join(dir, "replica")))
	rep.MonitorEnabled = true
	rep.RequireEncryption = true
	// No AgeRecipients.
	db.Replica = rep

	if err := db.Open(); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close(ctx)

	if err := db.Sync(ctx); !errors.Is(err, replicate.ErrEncryptionRequired) {
		t.Fatalf("expected db.Sync to fail closed with ErrEncryptionRequired, got %v", err)
	}
}

const ltxMagic = "LTX1"

func secretMemo(i int) string {
	return "confidential-ledger-entry-" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func mustSync(t *testing.T, ctx context.Context, db *replicate.DB) {
	t.Helper()
	if err := db.Sync(ctx); err != nil {
		t.Fatalf("db sync: %v", err)
	}
	if err := db.Replica.Sync(ctx); err != nil {
		t.Fatalf("replica sync: %v", err)
	}
}

func readHead(path string, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, n)
	m, err := f.Read(buf)
	if err != nil && m == 0 {
		return nil, nil
	}
	return buf[:m], nil
}

func hasPrefix(b []byte, s string) bool {
	return len(b) >= len(s) && string(b[:len(s)]) == s
}

func truncate(b []byte, n int) []byte {
	if len(b) < n {
		return b
	}
	return b[:n]
}
