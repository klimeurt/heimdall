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
RUN go build -ldflags="-w -s" -o coordinator ./cmd/coordinator

# Runtime stage
FROM alpine:3.20

# Install runtime dependencies
RUN apk add --no-cache ca-certificates && rm -rf /var/cache/apk/*

# Create non-root user
RUN adduser -D -u 1000 coordinator

# Copy binary from builder
COPY --from=builder /app/coordinator /usr/local/bin/heimdall-coordinator

# Set ownership
RUN chown -R coordinator:coordinator /usr/local/bin/heimdall-coordinator

# Switch to non-root user
USER coordinator

# Set entrypoint
ENTRYPOINT ["/usr/local/bin/heimdall-coordinator"]