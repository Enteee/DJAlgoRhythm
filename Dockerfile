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
    -o whatdj \
    ./cmd/whatdj

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
RUN adduser -D -s /bin/sh whatdj

# Set working directory
WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/whatdj .

# Create directories for data
RUN mkdir -p /app/data && chown -R whatdj:whatdj /app

# Switch to non-root user
USER whatdj

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8080/healthz || exit 1

# Expose port
EXPOSE 8080

# Set default environment
ENV WHATDJ_SERVER_HOST=0.0.0.0 \
    WHATDJ_SERVER_PORT=8080 \
    WHATDJ_LOG_LEVEL=info \
    WHATDJ_WHATSAPP_SESSION_PATH=/app/data/whatsapp_session.db \
    WHATDJ_SPOTIFY_TOKEN_PATH=/app/data/spotify_token.json

# Run the application
ENTRYPOINT ["./whatdj"]