FROM golang:1.26.3-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-amd64} go build -o /out/chowkidar ./cmd/server

FROM alpine:3.23
RUN apk add --no-cache ca-certificates dumb-init bash curl docker-cli python3 py3-pip && \
    python3 -m venv /apprise && \
    /apprise/bin/pip install --quiet apprise && \
    ln -sf /apprise/bin/apprise /usr/local/bin/apprise
WORKDIR /app
COPY --from=builder /out/chowkidar /app/chowkidar
COPY web /app/web
RUN mkdir -p /config/data /config/logs
EXPOSE 8080
VOLUME ["/config"]
ENTRYPOINT ["/usr/bin/dumb-init", "--"]
CMD ["/app/chowkidar"]
