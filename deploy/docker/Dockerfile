# syntax=docker/dockerfile:1

# ── Build stage ────────────────────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /src

# Cache dependencies before copying source
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build a fully static binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -trimpath \
    -ldflags="-s -w \
      -X main.version=$(git describe --tags --always --dirty 2>/dev/null || echo dev) \
      -X main.commit=$(git rev-parse --short HEAD 2>/dev/null || echo none) \
      -X main.buildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o /gateway ./cmd/gateway

# ── Final image ────────────────────────────────────────────────────────────
FROM scratch

# TLS root certs + timezone data
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# The binary
COPY --from=builder /gateway /gateway

# Default config (override with -v mount)
COPY configs/gateway.yaml /configs/gateway.yaml

EXPOSE 8080 9090

HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
  CMD ["/gateway", "-version"]

ENTRYPOINT ["/gateway"]
CMD ["-config", "/configs/gateway.yaml"]
