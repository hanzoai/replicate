#!/bin/bash

# Script to verify test environment is set up correctly
# Ensures we're using local builds, not system-installed versions

echo "=========================================="
echo "Replicate Test Environment Verification"
echo "=========================================="
echo ""

# Check for local Replicate build
echo "Checking for local Replicate build..."
if [ -f "./bin/replicate" ]; then
    echo "✓ Local replicate found: ./bin/replicate"
    echo "  Version: $($./bin/replicate version)"
    echo "  Size: $(ls -lh ./bin/replicate | awk '{print $5}')"
    echo "  Modified: $(ls -la ./bin/replicate | awk '{print $6, $7, $8}')"
else
    echo "✗ Local replicate NOT found at ./bin/replicate"
    echo "  Please build: go build -o bin/replicate ./cmd/replicate"
    exit 1
fi

# Check for system Replicate (should NOT be used)
echo ""
echo "Checking for system Replicate..."
if command -v replicate &> /dev/null; then
    SYSTEM_REPLICATE=$(which replicate)
    echo "⚠ System replicate found at: $SYSTEM_REPLICATE"
    echo "  Version: $(replicate version 2>&1 || echo "unknown")"
    echo "  WARNING: Tests should NOT use this version!"
    echo "  All test scripts use ./bin/replicate explicitly"
else
    echo "✓ No system replicate found (good - avoids confusion)"
fi

# Check for replicate-test binary
echo ""
echo "Checking for replicate-test binary..."
if [ -f "./bin/replicate-test" ]; then
    echo "✓ Local replicate-test found: ./bin/replicate-test"
    echo "  Size: $(ls -lh ./bin/replicate-test | awk '{print $5}')"
    echo "  Modified: $(ls -la ./bin/replicate-test | awk '{print $6, $7, $8}')"
else
    echo "✗ replicate-test NOT found at ./bin/replicate-test"
    echo "  Please build: go build -o bin/replicate-test ./cmd/replicate-test"
    exit 1
fi

# Verify test scripts use local builds
echo ""
echo "Verifying test scripts use local builds..."
SCRIPTS=(
    "reproduce-critical-bug.sh"
    "test-1gb-boundary.sh"
    "test-concurrent-operations.sh"
)

ALL_GOOD=true
for script in "${SCRIPTS[@]}"; do
    if [ -f "$script" ]; then
        if grep -q 'REPLICATE="./bin/replicate"' "$script"; then
            echo "✓ $script uses local build"
        else
            echo "✗ $script may not use local build!"
            grep "REPLICATE=" "$script" | head -2
            ALL_GOOD=false
        fi
    else
        echo "- $script not found (optional)"
    fi
done

# Check current git branch
echo ""
echo "Git status:"
BRANCH=$(git branch --show-current 2>/dev/null || echo "unknown")
echo "  Current branch: $BRANCH"
if [ "$BRANCH" = "main" ]; then
    echo "  ⚠ On main branch - be careful with commits!"
fi

# Summary
echo ""
echo "=========================================="
if [ "$ALL_GOOD" = true ] && [ -f "./bin/replicate" ] && [ -f "./bin/replicate-test" ]; then
    echo "✅ Test environment is properly configured!"
    echo ""
    echo "You can run tests with:"
    echo "  ./reproduce-critical-bug.sh"
    echo "  ./test-1gb-boundary.sh"
    echo "  ./test-concurrent-operations.sh"
else
    echo "❌ Test environment needs setup"
    echo ""
    echo "Required steps:"
    [ ! -f "./bin/replicate" ] && echo "  1. Build replicate: go build -o bin/replicate ./cmd/replicate"
    [ ! -f "./bin/replicate-test" ] && echo "  2. Build test harness: go build -o bin/replicate-test ./cmd/replicate-test"
    exit 1
fi
echo "=========================================="
