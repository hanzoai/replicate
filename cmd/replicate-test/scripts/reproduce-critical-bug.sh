#!/bin/bash

# Replicate v0.5.0 Critical Bug Reproduction Script
#
# This script demonstrates a CRITICAL data loss bug where restore fails
# after Replicate is interrupted and a checkpoint occurs during downtime.
#
# Requirements:
# - replicate binary (built from current main branch)
# - replicate-test binary (from PR #748 or build with: go build -o bin/replicate-test ./cmd/replicate-test)
# - SQLite3 command line tool
#
# Expected behavior: Database should restore successfully
# Actual behavior: Restore fails with "nonsequential page numbers" error

set -e

echo "============================================"
echo "Replicate v0.5.0 Critical Bug Reproduction"
echo "============================================"
echo ""
echo "This demonstrates a data loss scenario where restore fails after:"
echo "1. Replicate is killed (simulating crash)"
echo "2. Writes continue and a checkpoint occurs"
echo "3. Replicate is restarted"
echo ""

# Configuration
DB="/tmp/critical-bug-test.db"
REPLICA="/tmp/critical-bug-replica"

# Clean up any previous test
echo "[SETUP] Cleaning up previous test files..."
rm -f "$DB"*
rm -rf "$REPLICA"

# ALWAYS use local build for testing
REPLICATE="./bin/replicate"
if [ ! -f "$REPLICATE" ]; then
    echo "ERROR: Local replicate build not found at $REPLICATE"
    echo "Please build first: go build -o bin/replicate ./cmd/replicate"
    exit 1
fi
echo "Using local build: $REPLICATE"

# Check for replicate-test binary
if [ -f "./bin/replicate-test" ]; then
    REPLICATE_TEST="./bin/replicate-test"
    echo "Using local replicate-test: $REPLICATE_TEST"
else
    echo "ERROR: replicate-test not found. Please build with:"
    echo "  go build -o bin/replicate-test ./cmd/replicate-test"
    echo ""
    echo "Or get it from: https://github.com/benbjohnson/replicate/pull/748"
    exit 1
fi

# Show versions
echo "Versions:"
$REPLICATE version
echo ""

# Step 1: Create and populate initial database
echo ""
echo "[STEP 1] Creating test database (50MB)..."
$REPLICATE_TEST populate -db "$DB" -target-size 50MB -table-count 2
INITIAL_SIZE=$(ls -lh "$DB" 2>/dev/null | awk '{print $5}')
echo "✓ Database created: $INITIAL_SIZE"

# Step 2: Start Replicate replication
echo ""
echo "[STEP 2] Starting Replicate replication..."
./bin/replicate replicate "$DB" "file://$REPLICA" > /tmp/replicate.log 2>&1 &
REPLICATE_PID=$!
sleep 3

if ! kill -0 $REPLICATE_PID 2>/dev/null; then
    echo "ERROR: Replicate failed to start. Check /tmp/replicate.log"
    cat /tmp/replicate.log
    exit 1
fi
echo "✓ Replicate running (PID: $REPLICATE_PID)"

# Step 3: Start continuous writes
echo ""
echo "[STEP 3] Starting continuous writes..."
./bin/replicate-test load -db "$DB" -write-rate 100 -duration 2m -pattern constant > /tmp/writes.log 2>&1 &
WRITE_PID=$!
echo "✓ Write load started (PID: $WRITE_PID)"

# Step 4: Let it run normally for 20 seconds
echo ""
echo "[STEP 4] Running normally for 20 seconds..."
sleep 20

# Get row count before interruption
ROWS_BEFORE=$(sqlite3 "$DB" "SELECT COUNT(*) FROM load_test;" 2>/dev/null || echo "0")
echo "✓ Rows written before interruption: $ROWS_BEFORE"

# Step 5: Kill Replicate (simulate crash)
echo ""
echo "[STEP 5] Killing Replicate (simulating crash)..."
kill -9 $REPLICATE_PID 2>/dev/null || true
echo "✓ Replicate killed"

# Step 6: Let writes continue for 15 seconds without Replicate
echo ""
echo "[STEP 6] Continuing writes for 15 seconds (Replicate is down)..."
sleep 15

# Step 7: Execute non-PASSIVE checkpoint
echo ""
echo "[STEP 7] Executing FULL checkpoint while Replicate is down..."
CHECKPOINT_RESULT=$(sqlite3 "$DB" "PRAGMA wal_checkpoint(FULL);" 2>&1)
echo "✓ Checkpoint result: $CHECKPOINT_RESULT"

ROWS_AFTER_CHECKPOINT=$(sqlite3 "$DB" "SELECT COUNT(*) FROM load_test;")
echo "✓ Rows after checkpoint: $ROWS_AFTER_CHECKPOINT"

# Step 8: Resume Replicate
echo ""
echo "[STEP 8] Resuming Replicate..."
./bin/replicate replicate "$DB" "file://$REPLICA" >> /tmp/replicate.log 2>&1 &
NEW_REPLICATE_PID=$!
sleep 3

if ! kill -0 $NEW_REPLICATE_PID 2>/dev/null; then
    echo "WARNING: Replicate failed to restart"
fi
echo "✓ Replicate restarted (PID: $NEW_REPLICATE_PID)"

# Step 9: Let Replicate catch up
echo ""
echo "[STEP 9] Letting Replicate catch up for 20 seconds..."
sleep 20

# Stop writes
kill $WRITE_PID 2>/dev/null || true
echo "✓ Writes stopped"

# Wait for final sync
sleep 5

# Get final row count
FINAL_COUNT=$(sqlite3 "$DB" "SELECT COUNT(*) FROM load_test;")
echo "✓ Final row count in source database: $FINAL_COUNT"

# Kill Replicate
kill $NEW_REPLICATE_PID 2>/dev/null || true

# Step 10: Attempt to restore (THIS IS WHERE THE BUG OCCURS)
echo ""
echo "[STEP 10] Attempting to restore database..."
echo "=========================================="
echo ""

rm -f /tmp/restored.db
if ./bin/replicate restore -o /tmp/restored.db "file://$REPLICA" 2>&1 | tee /tmp/restore-output.log; then
    echo ""
    echo "✓ SUCCESS: Restore completed successfully"

    # Verify restored database
    RESTORED_COUNT=$(sqlite3 /tmp/restored.db "SELECT COUNT(*) FROM load_test;" 2>/dev/null || echo "0")
    INTEGRITY=$(sqlite3 /tmp/restored.db "PRAGMA integrity_check;" 2>/dev/null || echo "FAILED")

    echo "  - Restored row count: $RESTORED_COUNT"
    echo "  - Integrity check: $INTEGRITY"

    if [ "$RESTORED_COUNT" -eq "$FINAL_COUNT" ]; then
        echo "  - Data integrity: ✓ VERIFIED (no data loss)"
    else
        LOSS=$((FINAL_COUNT - RESTORED_COUNT))
        echo "  - Data integrity: ✗ FAILED (lost $LOSS rows)"
    fi
else
    echo ""
    echo "✗ CRITICAL BUG REPRODUCED: Restore failed!"
    echo ""
    echo "Error output:"
    echo "-------------"
    cat /tmp/restore-output.log
    echo ""
    echo "This is the critical bug. The database cannot be restored after"
    echo "Replicate was interrupted and a checkpoint occurred during downtime."
    echo ""
    echo "Original database stats:"
    echo "  - Rows before interruption: $ROWS_BEFORE"
    echo "  - Rows after checkpoint: $ROWS_AFTER_CHECKPOINT"
    echo "  - Final rows: $FINAL_COUNT"
    echo "  - DATA IS UNRECOVERABLE"
fi

echo ""
echo "=========================================="
echo "Test artifacts saved in:"
echo "  - Source database: $DB"
echo "  - Replica files: $REPLICA/"
echo "  - Replicate log: /tmp/replicate.log"
echo "  - Restore output: /tmp/restore-output.log"
echo ""

# Clean up processes
pkill -f replicate-test 2>/dev/null || true
pkill -f "replicate replicate" 2>/dev/null || true

echo "Test complete."
