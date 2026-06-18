#!/bin/bash

# Health check script for LLMSafeSpaces services

set -e

# Default values
DB_HOST=${LLMSAFESPACES_DATABASE_HOST:-localhost}
DB_PORT=${LLMSAFESPACES_DATABASE_PORT:-5432}
DB_USER=${LLMSAFESPACES_DATABASE_USER:-llmsafespaces}
DB_PASSWORD=${LLMSAFESPACES_DATABASE_PASSWORD:-}
DB_NAME=${LLMSAFESPACES_DATABASE_DATABASE:-llmsafespaces}

REDIS_HOST=${LLMSAFESPACES_REDIS_HOST:-localhost}
REDIS_PORT=${LLMSAFESPACES_REDIS_PORT:-6379}
REDIS_PASSWORD=${LLMSAFESPACES_REDIS_PASSWORD:-}

# Check PostgreSQL connection
echo "Checking PostgreSQL connection..."
if PGPASSWORD=${DB_PASSWORD} psql -h ${DB_HOST} -p ${DB_PORT} -U ${DB_USER} -d ${DB_NAME} -c "SELECT 1" > /dev/null 2>&1; then
    echo "✅ PostgreSQL connection successful"
else
    echo "❌ PostgreSQL connection failed"
    exit 1
fi

# Check Redis connection
echo "Checking Redis connection..."
if [ -z "${REDIS_PASSWORD}" ]; then
    REDIS_AUTH=""
else
    REDIS_AUTH="-a ${REDIS_PASSWORD}"
fi

if redis-cli -h ${REDIS_HOST} -p ${REDIS_PORT} ${REDIS_AUTH} ping | grep -q "PONG"; then
    echo "✅ Redis connection successful"
else
    echo "❌ Redis connection failed"
    exit 1
fi

echo "All services are healthy!"
