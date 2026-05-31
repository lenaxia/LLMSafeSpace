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
.PHONY: all build clean test cover lint fmt vet generate deepcopy \
        helm-lint helm-template helm-template-debug helm-install-dry-run helm-package \
        openapi-validate \
        repolint chart-sync-migrations install-hooks

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
