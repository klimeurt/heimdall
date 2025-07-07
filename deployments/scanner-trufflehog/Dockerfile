# Build stage
FROM golang:1.24-alpine AS builder

# Install git, ca-certificates, and curl for downloading TruffleHog
RUN apk add --no-cache git ca-certificates curl

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
    -o scanner-trufflehog \
    ./cmd/scanner-trufflehog

# Download TruffleHog binary in builder stage
RUN curl -sSfL https://raw.githubusercontent.com/trufflesecurity/trufflehog/main/scripts/install.sh | sh -s -- -b /usr/local/bin && \
    chmod +x /usr/local/bin/trufflehog

# Final stage
FROM alpine:3.22

# Install only required runtime packages
RUN apk add --no-cache \
    ca-certificates \
    git && \
    addgroup -g 1000 scanner && \
    adduser -D -s /bin/sh -u 1000 -G scanner scanner && \
    mkdir -p /tmp/scanner-trufflehog && \
    chown -R scanner:scanner /tmp/scanner-trufflehog

# Copy the binaries from builder
COPY --from=builder /app/scanner-trufflehog /usr/local/bin/heimdall-scanner-trufflehog
COPY --from=builder /usr/local/bin/trufflehog /usr/local/bin/trufflehog

# Switch to non-root user
USER scanner:scanner

# Set working directory
WORKDIR /home/scanner

# Run the application
ENTRYPOINT ["/usr/local/bin/heimdall-scanner-trufflehog"]