# Build stage
FROM golang:1.24-alpine AS builder

# Install git and ca-certificates
RUN apk add --no-cache git ca-certificates

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s" \
    -o heimdall-scanner \
    ./cmd/scanner

# Final stage
FROM alpine:3.18

# Install required packages for git operations and TruffleHog
RUN apk add --no-cache \
    ca-certificates \
    git \
    curl

# Install TruffleHog
RUN curl -sSfL https://raw.githubusercontent.com/trufflesecurity/trufflehog/main/scripts/install.sh | sh -s -- -b /usr/local/bin

# Create non-root user
RUN addgroup -g 1000 scanner && \
    adduser -D -s /bin/sh -u 1000 -G scanner scanner

# Create temp directory for cloning
RUN mkdir -p /tmp/heimdall-scanner && \
    chown -R scanner:scanner /tmp/heimdall-scanner

# Copy the binary
COPY --from=builder /app/heimdall-scanner /usr/local/bin/heimdall-scanner

# Set permissions
RUN chmod +x /usr/local/bin/heimdall-scanner

# Switch to non-root user
USER scanner:scanner

# Set working directory
WORKDIR /home/scanner

# Run the application
ENTRYPOINT ["/usr/local/bin/heimdall-scanner"]