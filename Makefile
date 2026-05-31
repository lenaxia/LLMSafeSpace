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
        gitleaks govulncheck trivy-fs trivy-config security-scan

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
# See pkg/repolint/sequence_test.go for the regression cases and worklog 0098
# for the originating incident.
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
	@which golangci-lint >/dev/null 2>&1 || \
		$(GOCMD) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	@echo "Tools installed: goimports, golangci-lint"
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
check: fmt-check imports-check vet lint helm-render repolint
	@echo ""
	@echo "All quality gates passed."

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
