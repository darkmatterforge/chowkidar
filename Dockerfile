# --- Build Stage ---
FROM cgr.dev/chainguard/go:latest AS builder

WORKDIR /src

# Copy Go module files and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the Go binary — APP_VERSION is injected from the CI release tag
ARG TARGETARCH
ARG APP_VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-amd64} \
    go build -ldflags "-X main.appVersion=${APP_VERSION}" \
    -o /out/chowkidar ./cmd/server

# --- Runtime Stage ---
FROM cgr.dev/chainguard/wolfi-base:latest

# Upgrade existing packages and install runtime dependencies in one layer
RUN apk upgrade --no-cache && \
    apk add --no-cache ca-certificates dumb-init bash curl jq docker-cli python-3.13 py3-pip && \
    python3 -m venv /apprise && \
    /apprise/bin/pip install --quiet apprise && \
    mkdir -p /usr/local/bin && \
    ln -sf /apprise/bin/apprise /usr/local/bin/apprise

WORKDIR /app

ARG APP_VERSION=dev
LABEL org.opencontainers.image.version="${APP_VERSION}"

# Copy built binary and web assets
COPY --from=builder /out/chowkidar /app/chowkidar
COPY web /app/web

# Create config directories
RUN mkdir -p /config/data /config/logs

# Expose application port
EXPOSE 8080

# Health check endpoint
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD curl -f http://localhost:8080/api/health || exit 1

# Mount config volume
VOLUME ["/config"]

# Entrypoint and default command
ENTRYPOINT ["/usr/bin/dumb-init", "--"]
CMD ["/app/chowkidar"]
