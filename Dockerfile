# Dockerfile for DJAlgoRhythm - uses pre-built binaries
FROM alpine:3.20

# TARGETPLATFORM is automatically set by Docker buildx (e.g., linux/amd64, linux/arm64)
ARG TARGETPLATFORM

# Install runtime dependencies
# hadolint ignore=DL3018
RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    curl \
    && rm -rf /var/cache/apk/*

# Create non-root user
RUN adduser -D -s /bin/sh djalgorhythm

# Set working directory
WORKDIR /app

# Copy pre-built binary from platform-specific directory
COPY $TARGETPLATFORM/djalgorhythm .

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
    DJALGORHYTHM_SPOTIFY_OAUTH_BIND_HOST=0.0.0.0 \
    DJALGORHYTHM_LOG_LEVEL=info \
    DJALGORHYTHM_SPOTIFY_TOKEN_PATH=/app/data/spotify_token.json

# Run the application
ENTRYPOINT ["./djalgorhythm"]
