#!/bin/bash

# Database initialization script for LLMSafeSpace

set -e

# Default values
DB_HOST=${LLMSAFESPACE_DATABASE_HOST:-localhost}
DB_PORT=${LLMSAFESPACE_DATABASE_PORT:-5432}
DB_USER=${LLMSAFESPACE_DATABASE_USER:-llmsafespace}
DB_PASSWORD=${LLMSAFESPACE_DATABASE_PASSWORD:-}
DB_NAME=${LLMSAFESPACE_DATABASE_DATABASE:-llmsafespace}

# Check if psql is installed
if ! command -v psql &> /dev/null; then
    echo "Error: psql is not installed"
    echo "Please install PostgreSQL client tools"
    exit 1
fi

# Create database if it doesn't exist
echo "Creating database ${DB_NAME} if it doesn't exist..."
PGPASSWORD=${DB_PASSWORD} psql -h ${DB_HOST} -p ${DB_PORT} -U ${DB_USER} -d postgres -tc "SELECT 1 FROM pg_database WHERE datname = '${DB_NAME}'" | grep -q 1 || PGPASSWORD=${DB_PASSWORD} psql -h ${DB_HOST} -p ${DB_PORT} -U ${DB_USER} -d postgres -c "CREATE DATABASE ${DB_NAME}"

echo "Database initialization completed successfully"
echo "Run ./migrate.sh to apply migrations"
