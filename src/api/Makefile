.PHONY: build test clean run docker-build docker-push lint generate

# Variables
APP_NAME := agent-api
VERSION := $(shell git describe --tags --always --dirty)
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS := -ldflags "-X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)"
DOCKER_REPO := llmsafespace

# Go commands
build:
	go build $(LDFLAGS) -o bin/$(APP_NAME) ./cmd/api

test:
	go test -v -race -cover ./...

clean:
	rm -rf bin/
	rm -rf dist/

run:
	go run $(LDFLAGS) ./cmd/api

# Docker commands
docker-build:
	docker build -t $(DOCKER_REPO)/$(APP_NAME):$(VERSION) -t $(DOCKER_REPO)/$(APP_NAME):latest .

docker-push:
	docker push $(DOCKER_REPO)/$(APP_NAME):$(VERSION)
	docker push $(DOCKER_REPO)/$(APP_NAME):latest

# Development tools
lint:
	golangci-lint run ./...

generate:
	go generate ./...

# Database migrations
migrate-up:
	migrate -path ./migrations -database "postgres://$(DB_USER):$(DB_PASSWORD)@$(DB_HOST):$(DB_PORT)/$(DB_NAME)?sslmode=disable" up

migrate-down:
	migrate -path ./migrations -database "postgres://$(DB_USER):$(DB_PASSWORD)@$(DB_HOST):$(DB_PORT)/$(DB_NAME)?sslmode=disable" down

# Help
help:
	@echo "Available targets:"
	@echo "  build         - Build the application"
	@echo "  test          - Run tests"
	@echo "  clean         - Remove build artifacts"
	@echo "  run           - Run the application locally"
	@echo "  docker-build  - Build Docker image"
	@echo "  docker-push   - Push Docker image to registry"
	@echo "  lint          - Run linters"
	@echo "  generate      - Run code generation"
	@echo "  migrate-up    - Run database migrations up"
	@echo "  migrate-down  - Run database migrations down"
