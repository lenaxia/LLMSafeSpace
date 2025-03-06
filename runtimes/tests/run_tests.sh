#!/bin/bash

# Configuration
TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RESULTS_DIR="${TEST_DIR}/results"
COVERAGE_DIR="${RESULTS_DIR}/coverage"
LOG_FILE="${RESULTS_DIR}/test.log"

# Ensure directories exist
mkdir -p "$RESULTS_DIR" "$COVERAGE_DIR"

# Function to log messages
log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1" | tee -a "$LOG_FILE"
}

# Function to run tests with retry
run_with_retry() {
    local max_attempts=3
    local attempt=1
    
    while [ $attempt -le $max_attempts ]; do
        log "Attempt $attempt of $max_attempts: $1"
        if eval "$1"; then
            return 0
        fi
        attempt=$((attempt + 1))
        sleep 5
    done
    
    return 1
}

# Clean up any leftover containers
log "Cleaning up existing containers..."
docker ps -a | grep 'llmsafespace' | awk '{print $1}' | xargs -r docker rm -f

# Build test images if needed
log "Building test images..."
if ! docker images | grep -q 'llmsafespace/base'; then
    run_with_retry "docker build -t llmsafespace/base:latest ../../runtimes/base"
fi

if ! docker images | grep -q 'llmsafespace/python'; then
    run_with_retry "docker build -t llmsafespace/python:latest ../../runtimes/python"
fi

if ! docker images | grep -q 'llmsafespace/nodejs'; then
    run_with_retry "docker build -t llmsafespace/nodejs:latest ../../runtimes/nodejs"
fi

if ! docker images | grep -q 'llmsafespace/go'; then
    run_with_retry "docker build -t llmsafespace/go:latest ../../runtimes/go"
fi

# Install test dependencies
log "Installing test dependencies..."
pip install -r requirements.txt

# Run tests with coverage
log "Running tests..."
pytest \
    --verbose \
    --capture=no \
    --log-cli-level=INFO \
    --junit-xml="${RESULTS_DIR}/junit.xml" \
    --cov=../../runtimes \
    --cov-report=html:"${COVERAGE_DIR}" \
    --cov-report=xml:"${COVERAGE_DIR}/coverage.xml" \
    test_runtime.py

test_exit=$?

# Generate test summary
log "Generating test summary..."
{
    echo "Test Summary"
    echo "============"
    echo
    echo "Test Results: $([ $test_exit -eq 0 ] && echo 'PASS' || echo 'FAIL')"
    echo "Coverage Report: ${COVERAGE_DIR}/index.html"
    echo "Detailed Logs: ${LOG_FILE}"
    echo
    echo "Failed Tests:"
    grep -A 1 "FAILED" "$LOG_FILE" || echo "None"
} > "${RESULTS_DIR}/summary.txt"

cat "${RESULTS_DIR}/summary.txt"

exit $test_exit
