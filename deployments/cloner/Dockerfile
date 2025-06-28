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
    -o heimdall-cloner \
    ./cmd/cloner

# Final stage
FROM alpine:3.22

# Install git and ca-certificates for HTTPS and git operations
RUN apk add --no-cache git ca-certificates && \
    addgroup -g 1000 cloner && \
    adduser -D -s /bin/sh -u 1000 -G cloner cloner

# Copy the binary
COPY --from=builder /app/heimdall-cloner /heimdall-cloner

# Set user to non-root
USER cloner:cloner

# Run the application
ENTRYPOINT ["/heimdall-cloner"]