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
    -o heimdall-cleaner \
    ./cmd/cleaner

# Final stage
FROM alpine:latest

# Install ca-certificates for HTTPS
RUN apk add --no-cache ca-certificates

# Copy the binary
COPY --from=builder /app/heimdall-cleaner /heimdall-cleaner

# Create non-root user
RUN adduser -D -u 1000 heimdall

# Set user to non-root
USER heimdall

# Run the application
ENTRYPOINT ["/heimdall-cleaner"]