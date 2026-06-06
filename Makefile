# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
BINARY_NAME=llmsafespace
BINARY_UNIX=$(BINARY_NAME)_unix

# Build targets
.PHONY: all build clean test cover lint fmt fmt-check imports imports-check vet generate deepcopy \
        helm-lint helm-template helm-template-debug helm-install-dry-run helm-package helm-render \
        openapi-validate \
        repolint chart-sync-migrations install-hooks \
        check tools-install \
        gitleaks govulncheck trivy-fs trivy-config security-scan \
        migration-roundtrip migration-fk-cascade migration-idempotent migration-safety \
        test-full cover-floor mutation

all: test build

build:
	$(GOBUILD) -o $(BINARY_NAME) -v ./api/cmd/api

clean:
	$(GOCLEAN)
	rm -f $(BINARY_NAME)
	rm -f $(BINARY_UNIX)

test:
	$(GOTEST) -v ./...

cover:
	$(GOTEST) -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out

lint:
	golangci-lint run

fmt:
	$(GOCMD) fmt ./...

# fmt-check: verify gofmt has been run. Used by pre-commit and CI to
# block PRs that contain unformatted Go. Lists offending files and
# exits non-zero. To fix locally: `make fmt`.
fmt-check:
	@unformatted=$$(gofmt -l . | grep -v '/node_modules/' || true); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt: the following files are not formatted:"; \
		echo "$$unformatted"; \
		echo ""; \
		echo "Run 'make fmt' to fix."; \
		exit 1; \
	fi

imports:
	@which goimports >/dev/null 2>&1 || $(GOCMD) install golang.org/x/tools/cmd/goimports@latest
	goimports -w $$(find . -name '*.go' -not -path './frontend/node_modules/*' -not -path './sdks/*/node_modules/*')

# imports-check: verify goimports has been run (import grouping +
# unused-import removal). Same enforcement model as fmt-check.
imports-check:
	@which goimports >/dev/null 2>&1 || $(GOCMD) install golang.org/x/tools/cmd/goimports@latest
	@bad=$$(goimports -l . | grep -v '/node_modules/' || true); \
	if [ -n "$$bad" ]; then \
		echo "goimports: the following files have wrong imports:"; \
		echo "$$bad"; \
		echo ""; \
		echo "Run 'make imports' to fix."; \
		exit 1; \
	fi

vet:
	$(GOCMD) vet ./...

generate:
	$(GOCMD) generate ./...

deepcopy:
	chmod +x ./hack/update-deepcopy.sh
	./hack/update-deepcopy.sh

# Cross compilation
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOBUILD) -o $(BINARY_UNIX) -v ./api/cmd/api

build-linux-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GOBUILD) -o $(BINARY_UNIX)-arm64 -v ./api/cmd/api

docker-build:
	docker build -t $(BINARY_NAME):latest .

docker-run:
	docker run --rm -p 8080:8080 $(BINARY_NAME):latest

# ---------------------------------------------------------------------------
# Helm chart targets
# ---------------------------------------------------------------------------
HELM=helm
CHART_DIR=charts/llmsafespace
RELEASE_NAME?=llmsafespace
RELEASE_NS?=llmsafespace

helm-lint:
	$(HELM) lint $(CHART_DIR)

helm-template:
	$(HELM) template $(RELEASE_NAME) $(CHART_DIR) -n $(RELEASE_NS)

helm-template-debug:
	$(HELM) template $(RELEASE_NAME) $(CHART_DIR) -n $(RELEASE_NS) --debug

# Renders against the live cluster's API server. Requires kubeconfig.
helm-install-dry-run:
	$(HELM) install $(RELEASE_NAME) $(CHART_DIR) -n $(RELEASE_NS) --create-namespace --dry-run

helm-package:
	$(HELM) package $(CHART_DIR) -d dist/

# helm-render: lint + template the chart against the bundled defaults
# (values.yaml). Catches:
#   - syntax errors / missing template files
#   - undefined values referenced by templates
#   - invalid Helm chart structure (missing Chart.yaml, etc.)
# Output is discarded; we only care about the exit code. Pre-commit
# and CI use this; for debugging use helm-template or
# helm-template-debug to see the rendered manifests.
helm-render:
	$(HELM) lint $(CHART_DIR)
	$(HELM) template $(RELEASE_NAME) $(CHART_DIR) -n $(RELEASE_NS) >/dev/null

# helm-chart-test: run the Go-based chart rendering tests (chart_test.go).
# These tests render manifests via `helm template` and assert structural
# invariants that `helm-render` (lint + template) cannot catch — e.g. that
# the MCP namespace uses .Release.Namespace, that probes use tcpSocket, that
# additionalHosts includes the /api path, etc.
#
# Requires helm on PATH. Silently skips if helm is absent (see chart_test.go).
# Run by CI in both the `test` and `test-full` jobs (helm installed there).
# Also run by `make check` so local contributors catch regressions before push.
helm-chart-test:
	$(GOTEST) ./charts/llmsafespace/...

# ---------------------------------------------------------------------------
# OpenAPI validation
# ---------------------------------------------------------------------------
openapi-validate:
	$(MAKE) -C sdks validate

# ---------------------------------------------------------------------------
# Repository layout lint (migration numbering, worklog numbering, chart drift)
# ---------------------------------------------------------------------------
# repolint: lint checks invoked by .githooks/pre-commit and CI. Catches the
# failure modes that have caused production incidents:
#   - duplicate database migration version numbers (silent skip on cluster)
#   - non-sequential migration version numbers (gap = deleted migration)
#   - duplicate worklog numbers (history confusion)
#   - drift between api/migrations/ and charts/llmsafespace/migrations/
#   - drift between Go CRD struct fields and chart CRD openAPIV3Schema
#     (apiserver silently drops unknown fields; see worklog 0118-0119)
# See pkg/repolint/sequence_test.go and pkg/repolint/crd_drift_test.go for
# the regression cases and worklog 0098 for the originating incident.
repolint:
	$(GOBUILD) -o bin/repolint ./cmd/repolint
	./bin/repolint

# chart-sync-migrations: copy api/migrations/*.sql into charts/llmsafespace/migrations/.
# Run this every time you add a migration so the Helm-bundled copy stays in
# sync with the canonical one. The pre-commit hook will fail if you forget.
chart-sync-migrations:
	$(GOBUILD) -o bin/repolint ./cmd/repolint
	./bin/repolint -fix-drift

# install-hooks: wire .githooks/ into git's hook path. Run once per fresh
# clone. After this, every `git commit` runs `make repolint` and rejects the
# commit on failure.
install-hooks:
	git config core.hooksPath .githooks
	chmod +x .githooks/pre-commit
	@echo "Installed: git core.hooksPath = .githooks"
	@echo "Pre-commit hook will now run repolint on every commit."

# ---------------------------------------------------------------------------
# Quality gates (Epic 19: pre-merge automation)
# ---------------------------------------------------------------------------
# tools-install: install the developer tools the gates rely on. Run once
# per fresh clone, or after a Go-toolchain upgrade. Idempotent.
tools-install:
	$(GOCMD) install golang.org/x/tools/cmd/goimports@latest
	$(GOCMD) install github.com/client9/misspell/cmd/misspell@latest
	@which golangci-lint >/dev/null 2>&1 || \
		$(GOCMD) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	@echo "Tools installed: goimports, misspell, golangci-lint"
	@echo "Other tools (helm, gitleaks, govulncheck, trivy) are checked"
	@echo "by the relevant gates and installed on demand."

# check: run all the pre-merge quality gates locally. Mirrors what CI
# will block on. Use this before pushing to avoid CI round-trips.
#   - fmt-check     : gofmt is clean
#   - imports-check : goimports is clean
#   - vet           : go vet finds nothing
#   - lint          : golangci-lint finds nothing
#   - helm-render   : chart lints and renders
#   - repolint      : migration/worklog/chart-drift sequence checks
check: fmt-check imports-check vet lint helm-render helm-chart-test repolint
	@echo ""
	@echo "All quality gates passed."

# pre-commit-fix: auto-fix the mechanical issues that pre-commit blocks on,
# and re-stage the modified files so the next `git commit` succeeds.
#
# Use this when pre-commit fails on:
#   - gofmt           (alignment, indentation)
#   - goimports       (import grouping / unused imports)
#   - misspell        (US-locale spelling: "behaviour" → "behavior", etc)
#   - chart drift     (api/migrations/ ↔ charts/llmsafespace/migrations/)
#   - staticcheck S1016 (struct → struct conversion idiom)
#
# Does NOT auto-fix:
#   - errcheck / bodyclose / sqlclosecheck (semantic; need code changes)
#   - duplicate migration numbers (load-bearing; need human rename decision)
#   - CRD drift (need Go ↔ chart schema reconciliation)
#   - gitleaks findings (rotate the secret + remove from diff)
#
# After running, only the staged files that pre-commit had complained about
# are added back; we DO NOT touch unstaged user changes.
pre-commit-fix:
	@echo "== pre-commit-fix: snapshot staged files =="
	@staged=$$(git diff --cached --name-only --diff-filter=ACM | grep -E '\.(go|sql)$$' || true); \
	if [ -z "$$staged" ]; then \
		echo "No Go/SQL files staged; only chart-drift and worklog-fix will run."; \
	fi; \
	echo "== gofmt =="; \
	$(GOCMD) fmt ./... >/dev/null; \
	echo "== goimports =="; \
	$(MAKE) -s imports >/dev/null; \
	echo "== misspell =="; \
	which misspell >/dev/null 2>&1 || $(GOCMD) install github.com/client9/misspell/cmd/misspell@latest; \
	misspell -w -locale US $$(find . -name '*.go' -not -path './frontend/node_modules/*' -not -path './sdks/*/node_modules/*') >/dev/null 2>&1 || true; \
	echo "== chart-sync-migrations =="; \
	$(MAKE) -s chart-sync-migrations >/dev/null; \
	echo "== fix-worklogs =="; \
	$(GOBUILD) -o bin/repolint ./cmd/repolint >/dev/null 2>&1; \
	./bin/repolint -fix-worklogs; \
	echo "== restage modified files =="; \
	if [ -n "$$staged" ]; then \
		echo "$$staged" | xargs -r git add; \
	fi; \
	git add charts/llmsafespace/migrations/ 2>/dev/null || true; \
	git add worklogs/ 2>/dev/null || true; \
	echo ""; \
	echo "Auto-fixes applied and re-staged. Re-run 'git commit' to retry."

# pre-commit-fix-strict: like pre-commit-fix but ALSO runs the gates after
# fixing, so you can confirm the commit will now go through. Slower (~30s
# golangci-lint run) but no surprises.
pre-commit-fix-strict: pre-commit-fix
	@echo ""
	@echo "== verifying gates =="
	@$(MAKE) -s repolint
	@$(MAKE) -s fmt-check
	@$(MAKE) -s imports-check
	@$(MAKE) -s lint
	@echo ""
	@echo "All gates pass. 'git commit' should succeed now."

# recover-stash: dig dangling commits out of git's lost-and-found and
# print which ones contain Go/SQL/worklog/markdown files. Used when a
# rebase + stash dance lost untracked files (the failure mode in
# worklog 0123). Read-only — never modifies anything.
#
# Once you find the SHA, recover individual files with:
#   git show <sha>:path/to/file > path/to/file
recover-stash:
	@echo "Scanning git fsck --lost-found for dangling commits..."
	@for sha in $$(git fsck --lost-found 2>/dev/null | grep "dangling commit" | awk '{print $$3}'); do \
		files=$$(git show --stat $$sha 2>/dev/null | grep -E '\.(go|sql|md|tsx?|yaml)$$' | head -8 || true); \
		if [ -n "$$files" ]; then \
			echo ""; \
			echo "=== $$sha ==="; \
			git log --oneline -1 $$sha 2>/dev/null; \
			echo "$$files"; \
		fi; \
	done
	@echo ""
	@echo "To recover a file from a SHA above:"
	@echo "  git show <sha>:path/to/file > path/to/file"

# ---------------------------------------------------------------------------
# Security scanners (Epic 19, PR B)
# ---------------------------------------------------------------------------
# Three complementary scanners:
#   gitleaks    -- secrets in working tree (test fixtures allow-listed
#                  via .gitleaks.toml)
#   govulncheck -- Go vulnerability database; only fails on CALLED vulns
#   trivy fs    -- multi-language CVEs (npm, pip, mvn, go.mod, ...)
#   trivy config-- K8s manifest + Dockerfile misconfig
#
# Run individually for fast feedback or all of them via `security-scan`.

gitleaks:
	@which gitleaks >/dev/null 2>&1 || { \
		echo "gitleaks not installed; install from https://github.com/gitleaks/gitleaks"; \
		exit 1; }
	gitleaks detect --redact -c .gitleaks.toml --no-banner

govulncheck:
	@which govulncheck >/dev/null 2>&1 || $(GOCMD) install golang.org/x/vuln/cmd/govulncheck@latest
	govulncheck ./...

trivy-fs:
	@which trivy >/dev/null 2>&1 || { \
		echo "trivy not installed; install from https://github.com/aquasecurity/trivy"; \
		exit 1; }
	trivy fs --severity HIGH,CRITICAL --exit-code 1 \
		--skip-dirs frontend/node_modules \
		--skip-dirs sdks/typescript/node_modules \
		--skip-dirs sdks/vscode-llmsafespace/node_modules \
		--ignorefile .trivyignore \
		.

trivy-config:
	@which trivy >/dev/null 2>&1 || { \
		echo "trivy not installed; install from https://github.com/aquasecurity/trivy"; \
		exit 1; }
	trivy config --severity HIGH,CRITICAL --exit-code 1 \
		--skip-dirs frontend/node_modules \
		--skip-dirs sdks/typescript/node_modules \
		--skip-dirs sdks/vscode-llmsafespace/node_modules \
		--skip-dirs design/stories/epic-17-security-review \
		--skip-dirs local \
		--ignorefile .trivyignore \
		.

# security-scan: run all four scanners. Mirrors the CI security-scan
# workflow exactly. Slow (~30s); use the individual targets for tighter
# loops.
security-scan: gitleaks govulncheck trivy-fs trivy-config
	@echo ""
	@echo "All security scanners passed."

# ---------------------------------------------------------------------------
# Migration safety (Epic 19, PR C)
# ---------------------------------------------------------------------------
# Runs the migration round-trip + FK cascade + idempotency suite from
# .github/workflows/migration-safety.yml, but locally against a Postgres
# you supply via PG* env vars (PGHOST, PGUSER, PGPASSWORD, PGDATABASE).
#
# Setup:
#   docker run -d --rm --name pg-test -p 5432:5432 \
#     -e POSTGRES_USER=llmsafespace -e POSTGRES_PASSWORD=test \
#     -e POSTGRES_DB=llmsafespace postgres:16
#   export PGHOST=localhost PGUSER=llmsafespace PGPASSWORD=test PGDATABASE=llmsafespace
#   make migration-safety
#
# All three sub-targets re-create the schema from scratch in a single
# database, so they're not parallelizable. Run them in order or use
# the meta target.

migration-roundtrip:
	@command -v psql >/dev/null 2>&1 || { echo "psql not installed"; exit 1; }
	@: $${PGHOST:?must set PG* env vars}
	bash hack/migration-roundtrip.sh

migration-fk-cascade:
	@command -v psql >/dev/null 2>&1 || { echo "psql not installed"; exit 1; }
	@: $${PGHOST:?must set PG* env vars}
	bash api/migrations/test/fk_cascade.sh

migration-idempotent:
	@command -v psql >/dev/null 2>&1 || { echo "psql not installed"; exit 1; }
	@: $${PGHOST:?must set PG* env vars}
	bash hack/migration-idempotent.sh

migration-safety: migration-roundtrip migration-idempotent migration-fk-cascade
	@echo ""
	@echo "All migration safety checks passed."

# ---------------------------------------------------------------------------
# Test rigor (Epic 19, PR D)
# ---------------------------------------------------------------------------

# test-full: full test suite (no -short) with race detector. Mirrors
# the `test-full` job in ci.yml. Use this before pushing if you've
# touched anything performance-sensitive.
test-full:
	$(GOTEST) -timeout 600s -race -count=1 ./...

# cover-floor: run the coverage-instrumented test suite and assert the
# total coverage is at or above the floor (50%). Mirrors the gate in
# the CI `test` job — run locally to verify before pushing.
cover-floor:
	$(GOTEST) -timeout 300s -race -short \
		-coverprofile=coverage.out \
		-covermode=atomic \
		-coverpkg=./... \
		./...
	@total=$$($(GOCMD) tool cover -func=coverage.out | awk '/^total:/ {print $$3}' | tr -d '%'); \
	if awk -v t="$$total" 'BEGIN { exit !(t < 50) }'; then \
		echo "FAIL: total coverage $${total}% is below the 50.0% floor."; \
		exit 1; \
	fi; \
	echo "OK: total coverage $${total}% (floor 50.0%)"

# mutation: run gremlins mutation testing against the security-critical
# packages. Slow (~5-15 min per package on a laptop). Mirrors the
# nightly mutation.yml workflow. Set TARGET=path/to/pkg to scope.
mutation:
	@which gremlins >/dev/null 2>&1 || { \
		echo "gremlins not installed; install with"; \
		echo "  GOSUMDB=off GOTOOLCHAIN=local go install github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0"; \
		exit 1; }
	@target=$${TARGET:-pkg/secrets}; \
	echo "Running gremlins on ./$${target} ..."; \
	gremlins unleash ./$${target} --workers 2
