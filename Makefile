.PHONY: build test run run-collector run-cloner run-scanner run-cleaner run-indexer run-monitor docker-build docker-push clean

# Variables
COLLECTOR_NAME := heimdall-collector
CLONER_NAME := heimdall-cloner
SCANNER_NAME := heimdall-scanner
CLEANER_NAME := heimdall-cleaner
INDEXER_NAME := heimdall-indexer
OSV_SCANNER_NAME := heimdall-osv-scanner
COORDINATOR_NAME := heimdall-coordinator
DOCKER_REGISTRY := ghcr.io/klimeurt
COLLECTOR_IMAGE := $(DOCKER_REGISTRY)/$(COLLECTOR_NAME)
CLONER_IMAGE := $(DOCKER_REGISTRY)/$(CLONER_NAME)
SCANNER_IMAGE := $(DOCKER_REGISTRY)/$(SCANNER_NAME)
CLEANER_IMAGE := $(DOCKER_REGISTRY)/$(CLEANER_NAME)
INDEXER_IMAGE := $(DOCKER_REGISTRY)/$(INDEXER_NAME)
OSV_SCANNER_IMAGE := $(DOCKER_REGISTRY)/$(OSV_SCANNER_NAME)
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

# Build scanner binary
build-scanner:
	go build -ldflags="-w -s -X main.Version=$(VERSION)" -o $(SCANNER_NAME) ./cmd/scanner

# Build cleaner binary
build-cleaner:
	go build -ldflags="-w -s -X main.Version=$(VERSION)" -o $(CLEANER_NAME) ./cmd/cleaner

# Build indexer binary
build-indexer:
	go build -ldflags="-w -s -X main.Version=$(VERSION)" -o $(INDEXER_NAME) ./cmd/indexer

# Build osv-scanner binary
build-osv-scanner:
	go build -ldflags="-w -s -X main.Version=$(VERSION)" -o $(OSV_SCANNER_NAME) ./cmd/osv-scanner

# Build coordinator binary
build-coordinator:
	go build -ldflags="-w -s -X main.Version=$(VERSION)" -o $(COORDINATOR_NAME) ./cmd/coordinator

# Build all binaries
build-all: build-collector build-cloner build-scanner build-cleaner build-indexer build-osv-scanner build-coordinator

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

# Run scanner locally
run-scanner:
	go run ./cmd/scanner

# Run cleaner locally
run-cleaner:
	go run ./cmd/cleaner

# Run indexer locally
run-indexer:
	go run ./cmd/indexer

# Run osv-scanner locally
run-osv-scanner:
	go run ./cmd/osv-scanner

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

# Docker build scanner
docker-build-scanner:
	docker build -f deployments/scanner/Dockerfile -t $(SCANNER_IMAGE):$(VERSION) -t $(SCANNER_IMAGE):latest .

# Docker build cleaner
docker-build-cleaner:
	docker build -f deployments/cleaner/Dockerfile -t $(CLEANER_IMAGE):$(VERSION) -t $(CLEANER_IMAGE):latest .

# Docker build indexer
docker-build-indexer:
	docker build -f deployments/indexer/Dockerfile -t $(INDEXER_IMAGE):$(VERSION) -t $(INDEXER_IMAGE):latest .

# Docker build osv-scanner
docker-build-osv-scanner:
	docker build -f deployments/osv-scanner/Dockerfile -t $(OSV_SCANNER_IMAGE):$(VERSION) -t $(OSV_SCANNER_IMAGE):latest .

# Docker build coordinator
docker-build-coordinator:
	docker build -f deployments/coordinator/Dockerfile -t $(COORDINATOR_IMAGE):$(VERSION) -t $(COORDINATOR_IMAGE):latest .

# Docker build all images
docker-build: docker-build-collector docker-build-cloner docker-build-scanner docker-build-cleaner docker-build-indexer docker-build-osv-scanner docker-build-coordinator

# Docker push collector
docker-push-collector: docker-build-collector
	docker push $(COLLECTOR_IMAGE):$(VERSION)
	docker push $(COLLECTOR_IMAGE):latest

# Docker push cloner
docker-push-cloner: docker-build-cloner
	docker push $(CLONER_IMAGE):$(VERSION)
	docker push $(CLONER_IMAGE):latest

# Docker push scanner
docker-push-scanner: docker-build-scanner
	docker push $(SCANNER_IMAGE):$(VERSION)
	docker push $(SCANNER_IMAGE):latest

# Docker push cleaner
docker-push-cleaner: docker-build-cleaner
	docker push $(CLEANER_IMAGE):$(VERSION)
	docker push $(CLEANER_IMAGE):latest

# Docker push indexer
docker-push-indexer: docker-build-indexer
	docker push $(INDEXER_IMAGE):$(VERSION)
	docker push $(INDEXER_IMAGE):latest

# Docker push osv-scanner
docker-push-osv-scanner: docker-build-osv-scanner
	docker push $(OSV_SCANNER_IMAGE):$(VERSION)
	docker push $(OSV_SCANNER_IMAGE):latest

# Docker push coordinator
docker-push-coordinator: docker-build-coordinator
	docker push $(COORDINATOR_IMAGE):$(VERSION)
	docker push $(COORDINATOR_IMAGE):latest

# Docker push all images
docker-push: docker-push-collector docker-push-cloner docker-push-scanner docker-push-cleaner docker-push-indexer docker-push-osv-scanner docker-push-coordinator


# Install dependencies
deps:
	go mod download
	go mod tidy

# Clean build artifacts
clean:
	rm -f $(COLLECTOR_NAME) $(CLONER_NAME) $(SCANNER_NAME) $(CLEANER_NAME) $(INDEXER_NAME) $(OSV_SCANNER_NAME) $(COORDINATOR_NAME)
	rm -f coverage.out coverage.html
	rm -f *.tgz


# Full CI pipeline (unit tests only)
ci: lint test build-all docker-build

# Full CI pipeline with integration tests
ci-integration: lint test-all build-all docker-build

