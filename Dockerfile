# Build stage
FROM golang:1.24-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata gcc musl-dev sqlite-dev

# Set working directory
WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download && go mod verify

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=1 GOOS=linux go build \
    -ldflags="-w -s -extldflags '-static'" \
    -a -installsuffix cgo \
    -o djalgorhythm \
    ./cmd/djalgorhythm

# Final stage
FROM alpine:latest

# Install runtime dependencies
RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    curl \
    sqlite \
    && rm -rf /var/cache/apk/*

# Create non-root user
RUN adduser -D -s /bin/sh djalgorhythm

# Set working directory
WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/djalgorhythm .

# Create directories for data
RUN mkdir -p /app/data && chown -R djalgorhythm:djalgorhythm /app

# Switch to non-root user
USER djalgorhythm

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD curl -f http://127.0.0.1:8080/healthz || exit 1

# Expose port
EXPOSE 8080

# Set default environment
ENV DJALGORHYTHM_SERVER_HOST=0.0.0.0 \
    DJALGORHYTHM_SERVER_PORT=8080 \
    DJALGORHYTHM_LOG_LEVEL=info \
    DJALGORHYTHM_SPOTIFY_TOKEN_PATH=/app/data/spotify_token.json

# Run the application
ENTRYPOINT ["./djalgorhythm"]