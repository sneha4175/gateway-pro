// Package proxy wires all the internal packages together into a single
// http.Handler that routes, load-balances, rate-limits, and circuit-breaks
// every incoming request.
package proxy

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sneha4175/gateway-pro/internal/circuitbreaker"
	"github.com/sneha4175/gateway-pro/internal/config"
	"github.com/sneha4175/gateway-pro/internal/health"
	"github.com/sneha4175/gateway-pro/internal/loadbalancer"
	"github.com/sneha4175/gateway-pro/internal/middleware"
	"github.com/sneha4175/gateway-pro/internal/ratelimiter"
	"go.uber.org/zap"
)

// Gateway is the main http.Handler.
type Gateway struct {
	mu     sync.RWMutex
	routes []*route
	log    *zap.SugaredLogger
}

type route struct {
	prefix  string
	strip   bool
	timeout time.Duration
	lb      loadbalancer.Balancer
	rl      ratelimiter.Limiter
	breakers map[string]*circuitbreaker.Breaker // keyed by backend URL
	checker  *health.Checker
	handler  http.Handler
}

// NewGateway builds a Gateway from the given config.
func NewGateway(cfg *config.Config, log *zap.SugaredLogger) (*Gateway, error) {
	gw := &Gateway{log: log}
	routes, err := buildRoutes(cfg.Routes, log)
	if err != nil {
		return nil, err
	}
	gw.routes = routes
	return gw, nil
}

// Reload swaps in a new set of routes without downtime.
// Existing health-checkers for unchanged backends are preserved.
func (gw *Gateway) Reload(cfg *config.Config) error {
	routes, err := buildRoutes(cfg.Routes, gw.log)
	if err != nil {
		return err
	}

	gw.mu.Lock()
	old := gw.routes
	gw.routes = routes
	gw.mu.Unlock()

	// Stop health-checkers for routes that were removed
	newPrefixes := make(map[string]bool)
	for _, r := range routes {
		newPrefixes[r.prefix] = true
	}
	for _, r := range old {
		if !newPrefixes[r.prefix] && r.checker != nil {
			r.checker.Stop()
		}
	}
	return nil
}

// ServeHTTP dispatches to the matching route.
func (gw *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	gw.mu.RLock()
	routes := gw.routes
	gw.mu.RUnlock()

	// Longest prefix match
	var matched *route
	for _, rt := range routes {
		if strings.HasPrefix(r.URL.Path, rt.prefix) {
			if matched == nil || len(rt.prefix) > len(matched.prefix) {
				matched = rt
			}
		}
	}

	if matched == nil {
		http.Error(w, "no route matched", http.StatusNotFound)
		return
	}

	matched.handler.ServeHTTP(w, r)
}

// RegisterAdminHandlers mounts /metrics and /healthz on the admin mux.
func (gw *Gateway) RegisterAdminHandlers(mux *http.ServeMux) {
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/readyz", gw.readyzHandler)
	mux.HandleFunc("/backends", gw.backendsHandler)
}

func (gw *Gateway) readyzHandler(w http.ResponseWriter, _ *http.Request) {
	gw.mu.RLock()
	routes := gw.routes
	gw.mu.RUnlock()

	for _, rt := range routes {
		for _, b := range rt.lb.Backends() {
			if b.IsAlive() {
				goto ok
			}
		}
	}
	http.Error(w, `{"status":"not_ready","reason":"no healthy backends"}`, http.StatusServiceUnavailable)
	return
ok:
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ready"}`))
}

func (gw *Gateway) backendsHandler(w http.ResponseWriter, _ *http.Request) {
	gw.mu.RLock()
	routes := gw.routes
	gw.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, "[")
	for i, rt := range routes {
		if i > 0 {
			fmt.Fprint(w, ",")
		}
		fmt.Fprintf(w, `{"route":%q,"backends":[`, rt.prefix)
		for j, b := range rt.lb.Backends() {
			if j > 0 {
				fmt.Fprint(w, ",")
			}
			cbState := "disabled"
			if cb, ok := rt.breakers[b.URL]; ok {
				cbState = cb.State()
			}
			fmt.Fprintf(w, `{"url":%q,"alive":%v,"inflight":%d,"circuit_breaker":%q}`,
				b.URL, b.IsAlive(), b.Inflight(), cbState)
		}
		fmt.Fprint(w, "]}")
	}
	fmt.Fprint(w, "]")
}

// ---------------------------------------------------------------------------
// Route construction
// ---------------------------------------------------------------------------

func buildRoutes(cfgs []config.RouteConfig, log *zap.SugaredLogger) ([]*route, error) {
	routes := make([]*route, 0, len(cfgs))
	for i, cfg := range cfgs {
		r, err := buildRoute(cfg, log)
		if err != nil {
			return nil, fmt.Errorf("route[%d] %q: %w", i, cfg.PathPrefix, err)
		}
		routes = append(routes, r)
	}
	return routes, nil
}

func buildRoute(cfg config.RouteConfig, log *zap.SugaredLogger) (*route, error) {
	lb := loadbalancer.New(cfg.LBAlgorithm, cfg.Backends)

	rl, err := ratelimiter.New(cfg.RateLimit)
	if err != nil {
		return nil, err
	}

	// One circuit breaker per backend URL
	breakers := make(map[string]*circuitbreaker.Breaker, len(cfg.Backends))
	for _, b := range cfg.Backends {
		breakers[b.URL] = circuitbreaker.New(cfg.CircuitBreaker)
	}

	checker := health.New(lb.Backends(), log)

	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second

	rt := &route{
		prefix:   cfg.PathPrefix,
		strip:    cfg.StripPrefix,
		timeout:  timeout,
		lb:       lb,
		rl:       rl,
		breakers: breakers,
		checker:  checker,
	}

	// Build the per-route handler chain
	core := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rt.serveProxy(w, r, log)
	})

	rt.handler = middleware.Chain(core,
		middleware.RequestID,
		middleware.Logger(log),
		middleware.Metrics(cfg.PathPrefix),
	)

	return rt, nil
}

// serveProxy is the core proxy logic for one route.
func (rt *route) serveProxy(w http.ResponseWriter, r *http.Request, log *zap.SugaredLogger) {
	// Rate limiting
	if err := rt.rl.Allow(r); err != nil {
		var rlErr *ratelimiter.ErrRateLimited
		if errors.As(err, &rlErr) {
			w.Header().Set("Retry-After", fmt.Sprintf("%.0f", rlErr.RetryAfter.Seconds()))
			w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", time.Now().Add(rlErr.RetryAfter).Unix()))
		}
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}

	// Pick backend
	backend, err := rt.lb.Next(r)
	if err != nil {
		log.Errorw("no healthy backend", "route", rt.prefix)
		http.Error(w, "service unavailable — no healthy backends", http.StatusServiceUnavailable)
		return
	}

	// Circuit breaker check
	cb := rt.breakers[backend.URL]
	if cbErr := cb.Allow(); cbErr != nil {
		http.Error(w, "service unavailable — circuit open", http.StatusServiceUnavailable)
		return
	}

	// Track inflight for least_conn
	backend.Inc()
	defer backend.Dec()

	// Build target URL
	targetURL, err := url.Parse(backend.URL)
	if err != nil {
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}

	// Create a fresh reverse proxy per request (so we can set a per-request timeout)
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = targetURL.Scheme
			req.URL.Host = targetURL.Host
			if rt.strip {
				req.URL.Path = strings.TrimPrefix(req.URL.Path, rt.prefix)
				if req.URL.Path == "" {
					req.URL.Path = "/"
				}
			}
			// Propagate X-Forwarded-For
			if clientIP, _, err := net.SplitHostPort(req.RemoteAddr); err == nil {
				if prior := req.Header.Get("X-Forwarded-For"); prior != "" {
					clientIP = prior + ", " + clientIP
				}
				req.Header.Set("X-Forwarded-For", clientIP)
			}
			req.Header.Set("X-Forwarded-Host", req.Host)
			req.Header.Set("X-Forwarded-Proto", scheme(req))
		},
		ModifyResponse: func(resp *http.Response) error {
			// Record success / failure for circuit breaker based on HTTP status
			if resp.StatusCode >= 500 {
				cb.RecordFailure()
				backend.SetAlive(false) // will be recovered by health checker
			} else {
				cb.RecordSuccess()
				backend.SetAlive(true)
			}
			resp.Header.Set("X-Gateway-Backend", backend.URL)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Errorw("upstream error", "backend", backend.URL, "err", err)
			cb.RecordFailure()
			backend.SetAlive(false)
			http.Error(w, "bad gateway", http.StatusBadGateway)
		},
		// Per-request transport with configurable timeout
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   rt.timeout,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: rt.timeout,
			MaxIdleConns:          200,
			MaxIdleConnsPerHost:   20,
			IdleConnTimeout:       90 * time.Second,
		},
	}

	proxy.ServeHTTP(w, r)
}

func scheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}
