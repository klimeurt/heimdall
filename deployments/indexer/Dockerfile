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
    -o heimdall-indexer \
    ./cmd/indexer

# Final stage
FROM alpine:3.22

# Install only required runtime packages
RUN apk add --no-cache \
    ca-certificates && \
    addgroup -g 1000 indexer && \
    adduser -D -s /bin/sh -u 1000 -G indexer indexer

# Copy the binary from builder
COPY --from=builder /app/heimdall-indexer /usr/local/bin/heimdall-indexer

# Switch to non-root user
USER indexer:indexer

# Set working directory
WORKDIR /home/indexer

# Run the application
ENTRYPOINT ["/usr/local/bin/heimdall-indexer"]