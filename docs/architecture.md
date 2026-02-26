# Architecture

## Overview

```
                ┌──────────────────────────────────────────────┐
                │              gateway-pro process             │
  Client ──────▶│                                              │
   :8080        │  Recovery ─▶ RequestID ─▶ Logger ─▶ Metrics  │
                │                  │                            │
                │            Route matcher                      │
                │           (longest prefix)                    │
                │                  │                            │
                │          Rate limiter check                   │
                │                  │                            │
                │         Load balancer.Next(r)                │
                │                  │                            │
                │       Circuit breaker check                   │
                │                  │                            │
                │        httputil.ReverseProxy                  │
                │                  │                            │
                └──────────────────┼───────────────────────────┘
                                   │
               ┌───────────────────┼────────────────────┐
               ▼                   ▼                     ▼
          backend-1           backend-2             backend-N
          :8081               :8082                 :808N

  Admin port :9090
  ├── GET /metrics      Prometheus
  ├── GET /healthz      Liveness (always 200)
  ├── GET /readyz       Readiness (503 if no healthy backends)
  └── GET /backends     JSON backend status dump
```

## Request lifecycle

1. **Recovery middleware** wraps everything in a deferred recover so a panic in any downstream handler returns 500 rather than crashing the process.

2. **RequestID middleware** reads `X-Request-ID` from the incoming request. If absent, it generates a UUID v4 and sets it on both the request (so upstreams see it) and the response.

3. **Logger middleware** records method, path, status, bytes, and duration after the response is written, so it always includes the final status code.

4. **Metrics middleware** starts a Prometheus timer and increments `gateway_active_connections`. On completion it records the histogram observation and increments `gateway_requests_total`.

5. **Route matcher** does longest-prefix matching across all configured routes. If nothing matches, it returns 404.

6. **Rate limiter** applies the configured algorithm for the matched route. On rejection it sets `Retry-After` and `X-RateLimit-Reset` headers before returning 429.

7. **Load balancer** picks a backend using the configured algorithm. If all backends are unhealthy, returns 503.

8. **Circuit breaker** checks whether the selected backend's breaker is open. If open, returns 503 immediately without hitting the network.

9. **httputil.ReverseProxy** forwards the request. `ModifyResponse` records success/failure for the circuit breaker. `ErrorHandler` marks the backend unhealthy on network error.

## Hot-reload

`config.LoadAndWatch` uses `fsnotify.Watcher` to watch the config file. On a write event (debounced 200ms), it re-parses and validates the YAML and sends the new `*Config` on an unbuffered channel. `Gateway.Reload` acquires a write lock, swaps in the new route slice, and releases the lock. The old routes' health-checkers are stopped for routes that were removed.

Because `httputil.ReverseProxy` is created per-request (not shared), there is no stale-proxy-reference problem during reload.

## Health checking

Each route gets its own `health.Checker` goroutine. Every 10 seconds it concurrently probes `<backendURL>/health` for each backend. A 5xx response or a network error marks the backend unhealthy (atomic store). A subsequent successful probe marks it healthy again and logs a recovery message.

The load balancer's `healthy()` helper filters the backend slice on every call to `Next()`, so unhealthy backends are skipped without any lock contention.

## Circuit breaker state machine

```
       [requests succeed]
CLOSED ────────────────────────────────────▶ CLOSED
   │
   │ failure% ≥ threshold && total ≥ minReqs
   ▼
 OPEN ──── (wait openDuration) ────▶ HALF-OPEN
                                         │
                          probeRequests OK │ any failure
                                 ▼        ▼
                              CLOSED    OPEN
```

The rolling window is 10 seconds. Window entries older than 10 seconds are evicted on every `RecordSuccess` / `RecordFailure` call.

## Concurrency model

- **Load balancer** internals use either `atomic.Uint64` (round-robin counter) or a `sync.Mutex` (weighted, least-conn) depending on the algorithm.
- **Backend.alive** and **Backend.inflight** are `atomic.Bool` / `atomic.Int64` — no mutex needed on the hot path.
- **Circuit breaker** uses a single `sync.Mutex` to protect the window slice and state transitions.
- **Rate limiter** per-key buckets use individual `sync.Mutex` locks, so different keys never contend.
- **Gateway.routes** uses a `sync.RWMutex` — the common case (many concurrent reads) never blocks.
