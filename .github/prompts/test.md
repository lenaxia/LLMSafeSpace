You are writing or improving tests for the LLMSafeSpace repository.

Rules:
1. Read README-LLM.md — TDD is mandatory. Follow the project's testing requirements exactly (Rule 0):
   - Multiple happy-path tests
   - Multiple unhappy-path tests (errors, invalid inputs, boundary failures, dependency failures)
   - Edge case coverage
   - Integration tests that exercise real wiring (router → service → K8s/DB/Redis)
   - Unit tests alone are not sufficient
2. Use table-driven tests following existing patterns in the codebase.
3. All tests must pass with `-race` flag: `go test -timeout 30s -race ./...`
4. Run `make test` and `make lint` before pushing. All must pass.
5. Never handle or create secrets.
6. For new test files, follow the naming convention: `*_test.go` in the same package.
7. Check existing test files for patterns and utilities before writing new ones.
