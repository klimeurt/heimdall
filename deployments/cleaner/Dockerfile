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

# Create user and group files for scratch container
RUN echo "heimdall:x:1000:1000:heimdall:/:/bin/false" > /etc/passwd.minimal && \
    echo "heimdall:x:1000:" > /etc/group.minimal

# Final stage
FROM scratch

# Copy ca-certificates for HTTPS
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy user and group files
COPY --from=builder /etc/passwd.minimal /etc/passwd
COPY --from=builder /etc/group.minimal /etc/group

# Copy the binary
COPY --from=builder /app/heimdall-cleaner /heimdall-cleaner

# Set user to non-root
USER heimdall

# Run the application
ENTRYPOINT ["/heimdall-cleaner"]