.PHONY: build test run run-sync run-scanner-trufflehog run-indexer run-monitor docker-build docker-push clean

# Variables
SYNC_NAME := heimdall-sync
SCANNER_TRUFFLEHOG_NAME := heimdall-scanner-trufflehog
INDEXER_NAME := heimdall-indexer
SCANNER_OSV_NAME := heimdall-scanner-osv
DOCKER_REGISTRY := ghcr.io/klimeurt
SYNC_IMAGE := $(DOCKER_REGISTRY)/$(SYNC_NAME)
SCANNER_TRUFFLEHOG_IMAGE := $(DOCKER_REGISTRY)/$(SCANNER_TRUFFLEHOG_NAME)
INDEXER_IMAGE := $(DOCKER_REGISTRY)/$(INDEXER_NAME)
SCANNER_OSV_IMAGE := $(DOCKER_REGISTRY)/$(SCANNER_OSV_NAME)
VERSION := $(shell git describe --tags --always --dirty)

# Build binaries
build:
	go build -ldflags="-w -s -X main.Version=$(VERSION)" -o $(SYNC_NAME) ./cmd/sync

# Build sync binary
build-sync:
	go build -ldflags="-w -s -X main.Version=$(VERSION)" -o $(SYNC_NAME) ./cmd/sync

# Build scanner-trufflehog binary
build-scanner-trufflehog:
	go build -ldflags="-w -s -X main.Version=$(VERSION)" -o $(SCANNER_TRUFFLEHOG_NAME) ./cmd/scanner-trufflehog

# Build indexer binary
build-indexer:
	go build -ldflags="-w -s -X main.Version=$(VERSION)" -o $(INDEXER_NAME) ./cmd/indexer

# Build scanner-osv binary
build-scanner-osv:
	go build -ldflags="-w -s -X main.Version=$(VERSION)" -o $(SCANNER_OSV_NAME) ./cmd/scanner-osv

# Build all binaries
build-all: build-sync build-scanner-trufflehog build-indexer build-scanner-osv

# Run unit tests
test:
	go test -v -race ./...

# Run unit tests with coverage
test-coverage:
	go test -v -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# Run integration tests
test-integration:
	go test -v -race -tags=integration ./...

# Run all tests (unit + integration)
test-all: test test-integration

# Run tests with coverage including integration tests
test-coverage-all:
	go test -v -race -tags=integration -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# Run sync locally (default)
run:
	go run ./cmd/sync

# Run sync locally
run-sync:
	go run ./cmd/sync

# Run scanner-trufflehog locally
run-scanner-trufflehog:
	go run ./cmd/scanner-trufflehog

# Run indexer locally
run-indexer:
	go run ./cmd/indexer

# Run scanner-osv locally
run-scanner-osv:
	go run ./cmd/scanner-osv

# Run Redis queue monitor
run-monitor:
	go run ./cmd/monitor

# Lint code
lint:
	golangci-lint run

# Format code
fmt:
	go fmt ./...

# Docker build sync
docker-build-sync:
	docker build -f deployments/sync/Dockerfile -t $(SYNC_IMAGE):$(VERSION) -t $(SYNC_IMAGE):latest .

# Docker build scanner-trufflehog
docker-build-scanner-trufflehog:
	docker build -f deployments/scanner-trufflehog/Dockerfile -t $(SCANNER_TRUFFLEHOG_IMAGE):$(VERSION) -t $(SCANNER_TRUFFLEHOG_IMAGE):latest .

# Docker build indexer
docker-build-indexer:
	docker build -f deployments/indexer/Dockerfile -t $(INDEXER_IMAGE):$(VERSION) -t $(INDEXER_IMAGE):latest .

# Docker build scanner-osv
docker-build-scanner-osv:
	docker build -f deployments/scanner-osv/Dockerfile -t $(SCANNER_OSV_IMAGE):$(VERSION) -t $(SCANNER_OSV_IMAGE):latest .

# Docker build all images
docker-build: docker-build-sync docker-build-scanner-trufflehog docker-build-indexer docker-build-scanner-osv

# Docker push sync
docker-push-sync: docker-build-sync
	docker push $(SYNC_IMAGE):$(VERSION)
	docker push $(SYNC_IMAGE):latest

# Docker push scanner-trufflehog
docker-push-scanner-trufflehog: docker-build-scanner-trufflehog
	docker push $(SCANNER_TRUFFLEHOG_IMAGE):$(VERSION)
	docker push $(SCANNER_TRUFFLEHOG_IMAGE):latest

# Docker push indexer
docker-push-indexer: docker-build-indexer
	docker push $(INDEXER_IMAGE):$(VERSION)
	docker push $(INDEXER_IMAGE):latest

# Docker push scanner-osv
docker-push-scanner-osv: docker-build-scanner-osv
	docker push $(SCANNER_OSV_IMAGE):$(VERSION)
	docker push $(SCANNER_OSV_IMAGE):latest

# Docker push all images
docker-push: docker-push-sync docker-push-scanner-trufflehog docker-push-indexer docker-push-scanner-osv


# Install dependencies
deps:
	go mod download
	go mod tidy

# Clean build artifacts
clean:
	rm -f $(SYNC_NAME) $(SCANNER_TRUFFLEHOG_NAME) $(INDEXER_NAME) $(SCANNER_OSV_NAME)
	rm -f coverage.out coverage.html
	rm -f *.tgz


# Full CI pipeline (unit tests only)
ci: lint test build-all docker-build

# Full CI pipeline with integration tests
ci-integration: lint test-all build-all docker-build

