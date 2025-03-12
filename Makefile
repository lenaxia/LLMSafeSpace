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
.PHONY: all build clean test cover lint fmt vet generate deepcopy

all: test build

build:
	$(GOBUILD) -o $(BINARY_NAME) -v ./cmd/api

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
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOBUILD) -o $(BINARY_UNIX) -v ./cmd/api

docker-build:
	docker build -t $(BINARY_NAME):latest .

docker-run:
	docker run --rm -p 8080:8080 $(BINARY_NAME):latest
