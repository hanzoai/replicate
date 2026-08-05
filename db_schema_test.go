package replicate

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/hanzoai/sqlite"
)

// TestDB_Checkpoint_AfterApplicationDropsOurTables reproduces the dataroom
// outage of 2026-08-02.
//
// dataroom boots `prisma db push --accept-data-loss`, which reconciles the
// whole file against the application's schema and drops every table that schema
// does not declare -- including _replicate_seq and _replicate_lock, which we
// had created one second earlier. The drop itself is harmless and replicates
// like any other transaction, but the next checkpoint releases the read lock,
// checkpoints, and then cannot reacquire:
//
//	checkpoint: reacquire read lock: SQL logic error: no such table: _replicate_seq (1)
//
// init() had already run, so nothing recreated the table and every sync from
// then on failed the same way -- 776 consecutive errors over three days with a
// dead replica and no other symptom.
func TestDB_Checkpoint_AfterApplicationDropsOurTables(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db")

	db := NewDB(dbPath)
	db.MonitorInterval = 0 // disable background goroutine
	db.Replica = NewReplica(db)
	db.Replica.Client = &testReplicaClient{dir: t.TempDir()}
	db.Replica.MonitorEnabled = false
	if err := db.Open(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	}()

	// The application, on its own connection, as dataroom's Next.js process is.
	app, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	if _, err := app.Exec(`PRAGMA journal_mode = wal;`); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Exec(`CREATE TABLE t (id INT, data TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Exec(`INSERT INTO t VALUES (1, 'before')`); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := db.Sync(ctx); err != nil {
		t.Fatalf("sync before drop: %v", err)
	}

	// `prisma db push` drops what its schema does not declare.
	for _, table := range []string{"_replicate_seq", "_replicate_lock"} {
		if _, err := app.Exec(`DROP TABLE ` + table); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}

	// The checkpoint that used to wedge replication for good.
	if err := db.Checkpoint(ctx, CheckpointModeRestart); err != nil {
		t.Fatalf("checkpoint after application dropped our tables: %v", err)
	}

	// Both tables are back, so the next checkpoint works too.
	for _, table := range []string{"_replicate_seq", "_replicate_lock"} {
		var n int
		if err := app.QueryRow(`SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil {
			t.Fatal(err)
		} else if n != 1 {
			t.Fatalf("%s was not recreated", table)
		}
	}

	// And replication carries on: writes made after the drop still sync.
	if _, err := app.Exec(`INSERT INTO t VALUES (2, 'after')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Sync(ctx); err != nil {
		t.Fatalf("sync after drop: %v", err)
	}
	if err := db.Checkpoint(ctx, CheckpointModeRestart); err != nil {
		t.Fatalf("second checkpoint after drop: %v", err)
	}
}
