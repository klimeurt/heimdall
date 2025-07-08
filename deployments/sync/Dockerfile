# Build stage
FROM golang:1.24-alpine AS builder

# Install git for cloning dependencies
RUN apk add --no-cache git

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the sync service
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o sync ./cmd/sync

# Final stage
FROM alpine:3.22

# Install git and ca-certificates for HTTPS
RUN apk add --no-cache git ca-certificates tzdata

# Create non-root user
RUN adduser -D -u 1000 heimdall

# Create shared volume directory
RUN mkdir -p /shared/heimdall-repos && chown -R heimdall:heimdall /shared

WORKDIR /app

# Copy the binary from builder
COPY --from=builder /app/sync .

# Switch to non-root user
USER heimdall

# Expose volume
VOLUME ["/shared/heimdall-repos"]

# Run the sync service
CMD ["./sync"]