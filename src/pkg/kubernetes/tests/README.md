# Kubernetes Package Tests

This directory contains tests for the Kubernetes package.

## Running Tests

To run all tests:

```bash
./run_tests.sh
```

Or manually:

```bash
go test -v ./...
```

## Test Structure

- `main_test.go`: Entry point for all tests
- `client_test.go`: Tests for the Kubernetes client
- `informers_test.go`: Tests for the informer factory
- `kubernetes_operations_test.go`: Tests for Kubernetes operations
- `client_crds_test.go`: Tests for CRD client implementations
- `mocks_test.go`: Tests for mock implementations
- `test_helpers.go`: Helper functions for tests

## Adding New Tests

When adding new tests, follow these guidelines:

1. Create test files with the `_test.go` suffix
2. Use the test helpers in `test_helpers.go` when possible
3. Mock external dependencies
4. Use descriptive test names
5. Add assertions for all expected behaviors
