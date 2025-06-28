# Build stage
FROM golang:1.21-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git make

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN go build -ldflags="-w -s" -o osv-scanner ./cmd/osv-scanner

# Runtime stage
FROM alpine:latest

# Install runtime dependencies
RUN apk add --no-cache \
    ca-certificates \
    git \
    && rm -rf /var/cache/apk/*

# Install osv-scanner
RUN wget -O osv-scanner https://github.com/google/osv-scanner/releases/latest/download/osv-scanner_linux_amd64 && \
    chmod +x osv-scanner && \
    mv osv-scanner /usr/local/bin/

# Create non-root user
RUN adduser -D -u 1000 scanner

# Copy binary from builder
COPY --from=builder /app/osv-scanner /usr/local/bin/heimdall-osv-scanner

# Set ownership
RUN chown -R scanner:scanner /usr/local/bin/heimdall-osv-scanner

# Switch to non-root user
USER scanner

# Set entrypoint
ENTRYPOINT ["/usr/local/bin/heimdall-osv-scanner"]