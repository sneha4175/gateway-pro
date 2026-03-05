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
)

// Chain applies middlewares around final, returning a ready http.Handler.
// Usage: Chain(coreHandler, mw1, mw2, mw3)  — mw1 runs outermost.
func Chain(final http.Handler, middlewares ...func(http.Handler) http.Handler) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		final = middlewares[i](final)
	}
	return final
}

// RequestID injects a unique X-Request-ID header into every request.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = uuid.NewString()
		}
		r.Header.Set("X-Request-ID", id)
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r)
	})
}

// Logger logs method, path, status, and duration for every request.
func Logger(log *zap.SugaredLogger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cw := &captureStatus{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(cw, r)
			log.Infow("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", cw.status,
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", r.Header.Get("X-Request-ID"),
			)
		})
	}
}

// Metrics records Prometheus counters and histograms, labelled by route.
// Usage: middleware.Metrics("/api/users")
func Metrics(route string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cw := &captureStatus{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(cw, r)
			requestsTotal.WithLabelValues(route, r.Method, fmt.Sprintf("%d", cw.status)).Inc()
			requestDuration.WithLabelValues(route, r.Method).Observe(time.Since(start).Seconds())
		})
	}
}

// Recovery catches panics and returns 500 instead of crashing the server.
func Recovery(log *zap.SugaredLogger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Errorw("panic recovered",
						"panic", rec,
						"stack", string(debug.Stack()),
					)
					http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
