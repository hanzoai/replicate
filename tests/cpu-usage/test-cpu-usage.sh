#!/bin/bash
set -e

# Test script for measuring Replicate idle CPU usage with S3 replication

DURATION=${1:-300}  # Default 5 minutes
CONFIG_FILE="replicate-test-polling.yml"
MODE_DESC="Polling mode (1s interval)"

echo "========================================="
echo "Replicate CPU Usage Test"
echo "========================================="
echo "Mode: $MODE_DESC"
echo "Config: $CONFIG_FILE"
echo "Duration: ${DURATION}s"
echo "========================================="

# Create test database
echo "Creating test database..."
rm -f /tmp/test.db /tmp/test.db-wal /tmp/test.db-shm
sqlite3 /tmp/test.db "CREATE TABLE test (id INTEGER PRIMARY KEY, data TEXT);"
sqlite3 /tmp/test.db "INSERT INTO test (data) VALUES ('test');"

# Start Replicate in background
echo "Starting Replicate..."
# Get script directory and repo root
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

source "$REPO_ROOT/.envrc"
"$REPO_ROOT/bin/replicate" replicate -config "$SCRIPT_DIR/$CONFIG_FILE" &
REPLICATE_PID=$!

echo "Replicate PID: $REPLICATE_PID"
echo ""
echo "Monitoring CPU usage for ${DURATION}s..."
echo "Press Ctrl+C to stop early"
echo ""

# Monitor CPU usage
echo "Time,CPU%,VSZ,RSS" > /tmp/replicate-cpu-log.csv
for i in $(seq 1 $DURATION); do
    if ! kill -0 $REPLICATE_PID 2>/dev/null; then
        echo "ERROR: Replicate process died!"
        exit 1
    fi

    # Get CPU and memory stats
    CPU=$(ps -p $REPLICATE_PID -o %cpu= | xargs)
    VSZ=$(ps -p $REPLICATE_PID -o vsz= | xargs)
    RSS=$(ps -p $REPLICATE_PID -o rss= | xargs)

    echo "$i,$CPU,$VSZ,$RSS" >> /tmp/replicate-cpu-log.csv

    # Display every 10 seconds
    if [ $((i % 10)) -eq 0 ]; then
        echo "[$i/${DURATION}s] CPU: ${CPU}%  VSZ: ${VSZ}KB  RSS: ${RSS}KB"
    fi

    sleep 1
done

# Stop Replicate
echo ""
echo "Stopping Replicate..."
kill $REPLICATE_PID
wait $REPLICATE_PID 2>/dev/null || true

# Calculate average CPU
echo ""
echo "========================================="
echo "Results"
echo "========================================="
AVG_CPU=$(awk -F',' 'NR>1 {sum+=$2; count++} END {if(count>0) print sum/count; else print 0}' /tmp/replicate-cpu-log.csv)
echo "Average CPU: ${AVG_CPU}%"
echo "Detailed log: /tmp/replicate-cpu-log.csv"
echo ""

# Show sample of S3 uploads
echo "S3 Bucket Contents:"
aws s3 ls s3://sprite-replicate-debugging/test-db-${CONFIG_MODE}/ --recursive | head -10
