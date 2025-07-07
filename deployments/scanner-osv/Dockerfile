# Build stage
FROM golang:1.24-alpine AS builder

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
RUN go build -ldflags="-w -s" -o scanner-osv ./cmd/scanner-osv

# Runtime stage
FROM alpine:3.22

# Install runtime dependencies
RUN apk add --no-cache \
    ca-certificates \
    git \
    && rm -rf /var/cache/apk/*

# Install scanner-osv
RUN wget -O scanner-osv https://github.com/google/osv-scanner/releases/latest/download/osv-scanner_linux_amd64 && \
    chmod +x scanner-osv && \
    mv scanner-osv /usr/local/bin/

# Create non-root user
RUN adduser -D -u 1000 scanner

# Copy binary from builder
COPY --from=builder /app/scanner-osv /usr/local/bin/heimdall-scanner-osv

# Set ownership
RUN chown -R scanner:scanner /usr/local/bin/heimdall-scanner-osv

# Switch to non-root user
USER scanner

# Set entrypoint
ENTRYPOINT ["/usr/local/bin/heimdall-scanner-osv"]