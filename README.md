<div align="center">
  <h1>gateway-pro</h1>
  <p><strong>Lightweight, production-grade API gateway written in Go</strong></p>

  [![CI](https://github.com/snehakhoreja/gateway-pro/actions/workflows/ci.yml/badge.svg)](https://github.com/snehakhoreja/gateway-pro/actions/workflows/ci.yml)
  [![Go Report Card](https://goreportcard.com/badge/github.com/snehakhoreja/gateway-pro)](https://goreportcard.com/report/github.com/snehakhoreja/gateway-pro)
  [![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
  [![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go)](go.mod)
</div>

---

gateway-pro is a single-binary API gateway that sits in front of your backend services and handles the things you'd otherwise wire up yourself: load balancing, rate limiting, circuit breaking, health checking, and metrics. It's built with Go's standard `net/http/httputil`, keeps external dependencies minimal, and is designed to be read and modified by a small team.

## Features

- **Load balancing** — round-robin, least-connections, weighted, and IP-hash (sticky sessions); all with active health checks
- **Rate limiting** — token bucket or sliding window; keyed by IP, user ID, or API key; in-process (zero deps) or distributed via Redis
- **Circuit breaking** — per-backend, three-state (closed/open/half-open), configurable thresholds
- **Reverse proxy** — uses Go's battle-tested `httputil.ReverseProxy`; configurable per-route timeouts; strips or preserves path prefixes
- **Observability** — Prometheus metrics on a separate admin port; structured JSON access logs via zap; `/healthz`, `/readyz`, and `/backends` endpoints
- **Hot-reload** — edit `gateway.yaml` and changes apply without restarting; uses `fsnotify`
- **Graceful shutdown** — drains in-flight requests on SIGTERM

## Quick start

### With Docker

```bash
docker run -p 8080:8080 -p 9090:9090 \
  -v $(pwd)/configs/gateway.yaml:/configs/gateway.yaml \
  snehakhoreja/gateway-pro:latest
```

### From source

```bash
git clone https://github.com/snehakhoreja/gateway-pro.git
cd gateway-pro
make build
./bin/gateway-pro -config configs/gateway.yaml
```

Requires Go 1.22+. No C dependencies.

### With Docker Compose (includes Prometheus + Grafana)

```bash
cd examples/docker-compose
docker compose up
```

- Gateway: http://localhost:8080
- Grafana: http://localhost:3000
- Prometheus: http://localhost:9091

## Configuration

gateway-pro is configured with a single YAML file. Environment variables are expanded automatically (`${MY_VAR}`).

```yaml
server:
  addr: ":8080"
  read_timeout_seconds: 30
  write_timeout_seconds: 30

admin:
  addr: ":9090"      # Prometheus metrics, /healthz, /readyz, /backends

logging:
  level: info        # debug | info | warn | error
  format: json       # json | console

routes:
  - path_prefix: /api/users
    lb_algorithm: round_robin      # round_robin | least_conn | weighted | ip_hash
    timeout_seconds: 10
    strip_prefix: false

    backends:
      - url: http://user-svc-1:8080
      - url: http://user-svc-2:8080

    rate_limit:
      algorithm: token_bucket      # token_bucket | sliding_window
      rate: 500                    # tokens/sec
      burst: 1000
      key_by: ip                   # ip | user | api_key
      # redis_url: redis://redis:6379/0   # uncomment for distributed

    circuit_breaker:
      failure_threshold: 50        # trip if ≥50% requests fail
      min_requests: 20
      open_duration_seconds: 30
      half_open_requests: 5
```

See [`configs/gateway.yaml`](configs/gateway.yaml) for a full annotated example with all four load-balancing algorithms.

## Admin endpoints

| Endpoint | Description |
|----------|-------------|
| `GET :9090/metrics` | Prometheus metrics |
| `GET :9090/healthz` | Liveness check (always 200 if process is up) |
| `GET :9090/readyz` | Readiness check (503 if no healthy backends) |
| `GET :9090/backends` | JSON snapshot of all backends and their circuit-breaker states |

## Metrics

| Metric | Type | Labels |
|--------|------|--------|
| `gateway_requests_total` | Counter | `route`, `method`, `status` |
| `gateway_request_duration_seconds` | Histogram | `route`, `method` |
| `gateway_active_connections` | Gauge | — |

A Grafana dashboard JSON is provided in [`examples/docker-compose/grafana/`](examples/docker-compose/grafana/).

## Kubernetes

```bash
# Apply the manifests (edit the ConfigMap first)
kubectl apply -f deploy/kubernetes/deployment.yaml

# Or use the Helm chart
helm install my-gateway deploy/helm/gateway-chart \
  --set image.tag=latest
```

The deployment includes an HPA (2–10 replicas, CPU-based), liveness/readiness probes, pre-stop sleep for graceful drain, and Prometheus scrape annotations.

## Development

```bash
make test          # unit tests with race detector
make lint          # golangci-lint
make bench         # Go benchmarks
make load-test     # hey-based load test against localhost:8080
```

## Project structure

```
cmd/gateway/          Entry point
internal/
  config/             YAML loader + fsnotify hot-reload
  loadbalancer/       Round-robin, least-conn, weighted, IP-hash
  ratelimiter/        Token bucket + sliding window (local & Redis)
  circuitbreaker/     Three-state circuit breaker
  health/             Active HTTP health checks
  middleware/         Recovery, request ID, logger, Prometheus
  proxy/              Gateway wiring — routes, dispatch, admin handlers
deploy/
  docker/             Dockerfile (multi-stage, scratch final image)
  kubernetes/         Deployment, Service, ConfigMap, HPA
  helm/               Helm chart
examples/
  docker-compose/     Full stack with Redis, Prometheus, Grafana
configs/              Annotated example config
```

## Why not just use Kong / Traefik / Envoy?

Those are excellent production tools. This project exists as a learning resource and as a starting point for teams that want a gateway they can actually read, debug, and modify themselves. The codebase is intentionally small: ~1,500 lines of Go, no code generation, no proto files.

## Contributing

See [CONTRIBUTING.md](.github/CONTRIBUTING.md).

## License

Apache 2.0 — see [LICENSE](LICENSE).
