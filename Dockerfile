# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /build

# Install templ
RUN go install github.com/a-h/templ/cmd/templ@latest

# Copy dependency files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Generate templ files and build
RUN templ generate && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags='-w -s' -o durpdeploy ./cmd/server

# Runtime stage - scratch for minimal size
FROM scratch

# Copy CA certificates for HTTPS (if needed)
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy binary from builder
COPY --from=builder /build/durpdeploy /durpdeploy

# Create data directory for SQLite database
WORKDIR /data

# Expose port
EXPOSE 8080

# Run the binary
CMD ["/durpdeploy"]
