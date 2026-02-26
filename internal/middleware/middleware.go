// Package middleware provides composable HTTP middleware for the gateway.
package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.uber.org/zap"
)

// ---------------------------------------------------------------------------
// Metrics (registered once at startup via promauto)
// ---------------------------------------------------------------------------

var (
	requestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "gateway",
		Name:      "requests_total",
		Help:      "Total HTTP requests processed by the gateway.",
	}, []string{"route", "method", "status"})

	requestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "gateway",
		Name:      "request_duration_seconds",
		Help:      "Histogram of HTTP request latencies.",
		Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
	}, []string{"route", "method"})

	activeConnections = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "gateway",
		Name:      "active_connections",
		Help:      "Number of currently active proxy connections.",
	})
)

// ---------------------------------------------------------------------------
// responseWriter wrapper to capture status code
// ---------------------------------------------------------------------------

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

func (sw *statusWriter) Write(b []byte) (int, error) {
	n, err := sw.ResponseWriter.Write(b)
	sw.bytes += n
	return n, err
}

// ---------------------------------------------------------------------------
// Recovery — catches panics so one bad request can't crash the server
// ---------------------------------------------------------------------------

func Recovery(log *zap.SugaredLogger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Errorw("recovered from panic",
						"panic", rec,
						"stack", string(debug.Stack()),
						"path", r.URL.Path,
					)
					http.Error(w, "internal server error", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// ---------------------------------------------------------------------------
// RequestID — injects/forwards a unique request ID
// ---------------------------------------------------------------------------

const headerRequestID = "X-Request-ID"

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(headerRequestID)
		if id == "" {
			id = uuid.New().String()
		}
		w.Header().Set(headerRequestID, id)
		r.Header.Set(headerRequestID, id)
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------------------
// Logger — structured access log
// ---------------------------------------------------------------------------

func Logger(log *zap.SugaredLogger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(sw, r)
			log.Infow("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.status,
				"bytes", sw.bytes,
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", r.Header.Get(headerRequestID),
				"remote_addr", r.RemoteAddr,
			)
		})
	}
}

// ---------------------------------------------------------------------------
// Metrics — Prometheus instrumentation
// ---------------------------------------------------------------------------

func Metrics(route string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			timer := prometheus.NewTimer(requestDuration.WithLabelValues(route, r.Method))
			activeConnections.Inc()
			defer func() {
				activeConnections.Dec()
				timer.ObserveDuration()
				requestsTotal.WithLabelValues(route, r.Method, fmt.Sprintf("%d", sw.status)).Inc()
			}()
			next.ServeHTTP(sw, r)
		})
	}
}

// Chain applies middlewares in order (first listed = outermost).
func Chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}
