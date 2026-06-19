#!/bin/bash

# Database initialization script for LLMSafeSpaces

set -e

# Default values
DB_HOST=${LLMSAFESPACES_DATABASE_HOST:-localhost}
DB_PORT=${LLMSAFESPACES_DATABASE_PORT:-5432}
DB_USER=${LLMSAFESPACES_DATABASE_USER:-llmsafespaces}
DB_PASSWORD=${LLMSAFESPACES_DATABASE_PASSWORD:-}
DB_NAME=${LLMSAFESPACES_DATABASE_DATABASE:-llmsafespaces}

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
