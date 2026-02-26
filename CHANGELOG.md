# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2024-04-01

### Added
- Initial release
- Round-robin, least-connections, weighted, and IP-hash load balancing
- Token bucket and sliding window rate limiting (in-process + Redis)
- Three-state circuit breaker per backend
- Active HTTP health checks
- Prometheus metrics on admin port
- Structured JSON access logs via zap
- Hot-reload config via fsnotify
- Graceful shutdown on SIGTERM
- Docker image (multi-stage, scratch final layer)
- Kubernetes manifests + HPA
- Docker Compose example with Prometheus and Grafana
