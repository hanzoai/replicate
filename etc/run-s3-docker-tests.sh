#!/bin/bash
set -e

# Script to run S3 integration tests against a local Hanzo S3 container.
# This provides a more realistic test environment than moto for testing
# S3-compatible provider compatibility.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

cd "$PROJECT_ROOT"

S3_ACCESS_KEY_ID=hanzo
S3_SECRET_ACCESS_KEY=hanzos3secret
S3_BUCKET=test-bucket
S3_ENDPOINT=http://localhost:9000

echo "Starting Hanzo S3 container..."
docker compose -f docker-compose.test.yml up -d

# Wait for the S3 API to answer its health probe.
echo "Waiting for Hanzo S3 to be ready..."
for i in {1..30}; do
    if curl -fsS "$S3_ENDPOINT/healthz" >/dev/null 2>&1; then
        echo "Hanzo S3 is ready"
        break
    fi
    if [ "$i" -eq 30 ]; then
        echo "Hanzo S3 failed to start"
        docker compose -f docker-compose.test.yml logs s3
        docker compose -f docker-compose.test.yml down
        exit 1
    fi
    sleep 1
done

# Create the test bucket with the AWS CLI.
echo "Creating test bucket..."
docker run --rm --network host \
    -e AWS_ACCESS_KEY_ID="$S3_ACCESS_KEY_ID" \
    -e AWS_SECRET_ACCESS_KEY="$S3_SECRET_ACCESS_KEY" \
    -e AWS_DEFAULT_REGION=us-east-1 \
    amazon/aws-cli --endpoint-url "$S3_ENDPOINT" s3 mb "s3://$S3_BUCKET" || true

# Set up cleanup trap
cleanup() {
    echo "Cleaning up..."
    docker compose -f docker-compose.test.yml down
}
trap cleanup EXIT

# Export environment variables for the S3 integration tests
export REPLICATE_S3_ACCESS_KEY_ID="$S3_ACCESS_KEY_ID"
export REPLICATE_S3_SECRET_ACCESS_KEY="$S3_SECRET_ACCESS_KEY"
export REPLICATE_S3_BUCKET="$S3_BUCKET"
export REPLICATE_S3_ENDPOINT="$S3_ENDPOINT"
export REPLICATE_S3_FORCE_PATH_STYLE=true
export REPLICATE_S3_REGION=us-east-1

echo "Running S3 integration tests against Hanzo S3..."
go test -v ./replica_client_test.go -integration -replica-clients=s3 "$@"

echo "Tests completed successfully!"
