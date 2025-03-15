# Middleware Tests

This directory contains comprehensive tests for all middleware components in the LLMSafeSpace API.

## Test Structure

Each middleware component has its own test file:

- `auth_test.go` - Tests for authentication and authorization middleware
- `cors_test.go` - Tests for CORS handling
- `error_handler_test.go` - Tests for error handling middleware
- `logging_test.go` - Tests for request/response logging
- `metrics_test.go` - Tests for metrics collection
- `rate_limit_test.go` - Tests for rate limiting strategies
- `recovery_test.go` - Tests for panic recovery
- `request_id_test.go` - Tests for request ID generation
- `security_test.go` - Tests for security headers and policies
- `tracing_test.go` - Tests for request tracing
- `validation_test.go` - Tests for request validation

## Running Tests

To run all middleware tests:

```bash
cd src/api/internal/middleware/tests
go test -v
```

To run a specific test file:

```bash
go test -v security_test.go middleware_test.go
```

To run tests with coverage:

```bash
go test -v -cover ./...
```

## Mock Objects

The tests use mock implementations of various interfaces:

- `MockLogger` - Mocks the logger interface
- `MockAuthService` - Mocks the authentication service
- `MockCacheService` - Mocks the cache service
- `MockMetricsService` - Mocks the metrics service

## Test Patterns

The tests follow these patterns:

1. **Setup** - Create mock objects and configure the middleware
2. **Execution** - Send requests through the middleware
3. **Assertion** - Verify the expected behavior

## Adding New Tests

When adding new tests:

1. Create test functions with descriptive names
2. Set up appropriate mocks and expectations
3. Create a minimal router with the middleware under test
4. Send requests and verify responses
5. Verify that mock expectations were met

## Test Coverage

The goal is to maintain >80% test coverage for all middleware components, testing:

- Happy paths
- Error conditions
- Edge cases
- Configuration options
