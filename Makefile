.PHONY: build test run run-collector run-cloner run-scanner-trufflehog run-cleaner run-indexer run-monitor docker-build docker-push clean

# Variables
COLLECTOR_NAME := heimdall-collector
CLONER_NAME := heimdall-cloner
SCANNER_TRUFFLEHOG_NAME := heimdall-scanner-trufflehog
CLEANER_NAME := heimdall-cleaner
INDEXER_NAME := heimdall-indexer
SCANNER_OSV_NAME := heimdall-scanner-osv
COORDINATOR_NAME := heimdall-coordinator
DOCKER_REGISTRY := ghcr.io/klimeurt
COLLECTOR_IMAGE := $(DOCKER_REGISTRY)/$(COLLECTOR_NAME)
CLONER_IMAGE := $(DOCKER_REGISTRY)/$(CLONER_NAME)
SCANNER_TRUFFLEHOG_IMAGE := $(DOCKER_REGISTRY)/$(SCANNER_TRUFFLEHOG_NAME)
CLEANER_IMAGE := $(DOCKER_REGISTRY)/$(CLEANER_NAME)
INDEXER_IMAGE := $(DOCKER_REGISTRY)/$(INDEXER_NAME)
SCANNER_OSV_IMAGE := $(DOCKER_REGISTRY)/$(SCANNER_OSV_NAME)
COORDINATOR_IMAGE := $(DOCKER_REGISTRY)/$(COORDINATOR_NAME)
VERSION := $(shell git describe --tags --always --dirty)

# Build binaries
build:
	go build -ldflags="-w -s -X main.Version=$(VERSION)" -o $(COLLECTOR_NAME) ./cmd/collector

# Build collector binary
build-collector:
	go build -ldflags="-w -s -X main.Version=$(VERSION)" -o $(COLLECTOR_NAME) ./cmd/collector

# Build cloner binary
build-cloner:
	go build -ldflags="-w -s -X main.Version=$(VERSION)" -o $(CLONER_NAME) ./cmd/cloner

# Build scanner-trufflehog binary
build-scanner-trufflehog:
	go build -ldflags="-w -s -X main.Version=$(VERSION)" -o $(SCANNER_TRUFFLEHOG_NAME) ./cmd/scanner-trufflehog

# Build cleaner binary
build-cleaner:
	go build -ldflags="-w -s -X main.Version=$(VERSION)" -o $(CLEANER_NAME) ./cmd/cleaner

# Build indexer binary
build-indexer:
	go build -ldflags="-w -s -X main.Version=$(VERSION)" -o $(INDEXER_NAME) ./cmd/indexer

# Build scanner-osv binary
build-scanner-osv:
	go build -ldflags="-w -s -X main.Version=$(VERSION)" -o $(SCANNER_OSV_NAME) ./cmd/scanner-osv

# Build coordinator binary
build-coordinator:
	go build -ldflags="-w -s -X main.Version=$(VERSION)" -o $(COORDINATOR_NAME) ./cmd/coordinator

# Build all binaries
build-all: build-collector build-cloner build-scanner-trufflehog build-cleaner build-indexer build-scanner-osv build-coordinator

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

# Run collector locally (default)
run:
	go run ./cmd/collector

# Run collector locally
run-collector:
	go run ./cmd/collector

# Run cloner locally
run-cloner:
	go run ./cmd/cloner

# Run scanner-trufflehog locally
run-scanner-trufflehog:
	go run ./cmd/scanner-trufflehog

# Run cleaner locally
run-cleaner:
	go run ./cmd/cleaner

# Run indexer locally
run-indexer:
	go run ./cmd/indexer

# Run scanner-osv locally
run-scanner-osv:
	go run ./cmd/scanner-osv

# Run coordinator locally
run-coordinator:
	go run ./cmd/coordinator

# Run Redis queue monitor
run-monitor:
	go run ./cmd/monitor

# Lint code
lint:
	golangci-lint run

# Format code
fmt:
	go fmt ./...

# Docker build collector
docker-build-collector:
	docker build -f deployments/collector/Dockerfile -t $(COLLECTOR_IMAGE):$(VERSION) -t $(COLLECTOR_IMAGE):latest .

# Docker build cloner
docker-build-cloner:
	docker build -f deployments/cloner/Dockerfile -t $(CLONER_IMAGE):$(VERSION) -t $(CLONER_IMAGE):latest .

# Docker build scanner-trufflehog
docker-build-scanner-trufflehog:
	docker build -f deployments/scanner-trufflehog/Dockerfile -t $(SCANNER_TRUFFLEHOG_IMAGE):$(VERSION) -t $(SCANNER_TRUFFLEHOG_IMAGE):latest .

# Docker build cleaner
docker-build-cleaner:
	docker build -f deployments/cleaner/Dockerfile -t $(CLEANER_IMAGE):$(VERSION) -t $(CLEANER_IMAGE):latest .

# Docker build indexer
docker-build-indexer:
	docker build -f deployments/indexer/Dockerfile -t $(INDEXER_IMAGE):$(VERSION) -t $(INDEXER_IMAGE):latest .

# Docker build scanner-osv
docker-build-scanner-osv:
	docker build -f deployments/scanner-osv/Dockerfile -t $(SCANNER_OSV_IMAGE):$(VERSION) -t $(SCANNER_OSV_IMAGE):latest .

# Docker build coordinator
docker-build-coordinator:
	docker build -f deployments/coordinator/Dockerfile -t $(COORDINATOR_IMAGE):$(VERSION) -t $(COORDINATOR_IMAGE):latest .

# Docker build all images
docker-build: docker-build-collector docker-build-cloner docker-build-scanner-trufflehog docker-build-cleaner docker-build-indexer docker-build-scanner-osv docker-build-coordinator

# Docker push collector
docker-push-collector: docker-build-collector
	docker push $(COLLECTOR_IMAGE):$(VERSION)
	docker push $(COLLECTOR_IMAGE):latest

# Docker push cloner
docker-push-cloner: docker-build-cloner
	docker push $(CLONER_IMAGE):$(VERSION)
	docker push $(CLONER_IMAGE):latest

# Docker push scanner-trufflehog
docker-push-scanner-trufflehog: docker-build-scanner-trufflehog
	docker push $(SCANNER_TRUFFLEHOG_IMAGE):$(VERSION)
	docker push $(SCANNER_TRUFFLEHOG_IMAGE):latest

# Docker push cleaner
docker-push-cleaner: docker-build-cleaner
	docker push $(CLEANER_IMAGE):$(VERSION)
	docker push $(CLEANER_IMAGE):latest

# Docker push indexer
docker-push-indexer: docker-build-indexer
	docker push $(INDEXER_IMAGE):$(VERSION)
	docker push $(INDEXER_IMAGE):latest

# Docker push scanner-osv
docker-push-scanner-osv: docker-build-scanner-osv
	docker push $(SCANNER_OSV_IMAGE):$(VERSION)
	docker push $(SCANNER_OSV_IMAGE):latest

# Docker push coordinator
docker-push-coordinator: docker-build-coordinator
	docker push $(COORDINATOR_IMAGE):$(VERSION)
	docker push $(COORDINATOR_IMAGE):latest

# Docker push all images
docker-push: docker-push-collector docker-push-cloner docker-push-scanner-trufflehog docker-push-cleaner docker-push-indexer docker-push-scanner-osv docker-push-coordinator


# Install dependencies
deps:
	go mod download
	go mod tidy

# Clean build artifacts
clean:
	rm -f $(COLLECTOR_NAME) $(CLONER_NAME) $(SCANNER_TRUFFLEHOG_NAME) $(CLEANER_NAME) $(INDEXER_NAME) $(SCANNER_OSV_NAME) $(COORDINATOR_NAME)
	rm -f coverage.out coverage.html
	rm -f *.tgz


# Full CI pipeline (unit tests only)
ci: lint test build-all docker-build

# Full CI pipeline with integration tests
ci-integration: lint test-all build-all docker-build

