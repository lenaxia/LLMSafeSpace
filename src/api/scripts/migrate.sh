#!/bin/bash

# Database migration script for LLMSafeSpace

set -e

# Default values
DB_HOST=${LLMSAFESPACE_DATABASE_HOST:-localhost}
DB_PORT=${LLMSAFESPACE_DATABASE_PORT:-5432}
DB_USER=${LLMSAFESPACE_DATABASE_USER:-llmsafespace}
DB_PASSWORD=${LLMSAFESPACE_DATABASE_PASSWORD:-}
DB_NAME=${LLMSAFESPACE_DATABASE_DATABASE:-llmsafespace}
MIGRATIONS_DIR=${MIGRATIONS_DIR:-./migrations}
COMMAND=${1:-up}

# Check if migrate tool is installed
if ! command -v migrate &> /dev/null; then
    echo "Error: migrate tool is not installed"
    echo "Please install golang-migrate: https://github.com/golang-migrate/migrate/tree/master/cmd/migrate"
    exit 1
fi

# Build connection string
CONNECTION_STRING="postgres://${DB_USER}:${DB_PASSWORD}@${DB_HOST}:${DB_PORT}/${DB_NAME}?sslmode=disable"

# Run migration
echo "Running migration command: ${COMMAND}"
migrate -path ${MIGRATIONS_DIR} -database "${CONNECTION_STRING}" ${COMMAND}

echo "Migration completed successfully"
