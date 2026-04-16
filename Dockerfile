# syntax=docker/dockerfile:1.7

# ---- builder -----------------------------------------------------------------
FROM golang:1.25-alpine AS builder

WORKDIR /src

# Cache module downloads separately from source changes.
COPY go.mod go.sum ./
RUN go mod download

# Build both binaries. Static assets and locales are embedded via go:embed,
# no separate COPY into the runtime image is required.
COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
        -ldflags "-s -w -X github.com/plekt-dev/plekt/internal/version.Version=${VERSION}" \
        -o /out/plekt-core ./cmd/plekt-core/
RUN CGO_ENABLED=0 GOOS=linux go build \
        -ldflags "-s -w" \
        -o /out/plekt-updater ./cmd/plekt-updater/

# ---- runtime -----------------------------------------------------------------
FROM alpine:3.20

# ca-certificates for outbound HTTPS (registry.plekt.dev, plugin downloads).
# tzdata for scheduler plugin cron timezones.
RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 10001 -g '' plekt

COPY --from=builder /out/plekt-core    /usr/local/bin/plekt-core
COPY --from=builder /out/plekt-updater /usr/local/bin/plekt-updater

# Default mount points — override with volumes in docker-compose.
# /app/config.yaml  — server config (mount read-only)
# /app/data         — per-plugin SQLite DBs + audit logs
# /app/plugins      — installed plugin directories
WORKDIR /app
RUN mkdir -p /app/data /app/plugins && \
    chown -R plekt:plekt /app

USER plekt

# Signals to the auto-updater that self-swap is not allowed inside a container.
# The updater shows a "docker pull" notice in the UI instead of attempting
# a binary swap (which would be lost on restart anyway).
ENV PLEKT_DOCKER=1

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/health >/dev/null 2>&1 || exit 1

ENTRYPOINT ["plekt-core"]
CMD ["/app/config.yaml"]
