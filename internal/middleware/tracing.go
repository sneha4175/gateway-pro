// tracing.go — lightweight distributed tracing middleware for gateway-pro.
//
// Design: we avoid the full OTel SDK (heavy, ~10 transitive deps) and instead
// implement the W3C Trace Context spec (traceparent header) directly.
// Spans are exported via a simple HTTP POST to the collector we built in the
// tracing/ directory. The exporter is fire-and-forget with a channel buffer —
// it never blocks the request path.
//
// W3C traceparent format: 00-<traceid>-<spanid>-<flags>
//   traceid: 32 hex chars (128-bit)
//   spanid:  16 hex chars (64-bit)
//   flags:   "01" = sampled
package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"
)

// TracingConfig maps to the `tracing:` block in gateway.yaml.
type TracingConfig struct {
	Enabled      bool    `yaml:"enabled"`
	CollectorURL string  `yaml:"collector_url"` // e.g. http://localhost:4317
	ServiceName  string  `yaml:"service_name"`  // e.g. gateway-pro
	SampleRate   float64 `yaml:"sample_rate"`   // 0.0–1.0; 1.0 = trace everything
}

// Span is the data we ship to the collector.
// Fields match the collector's Pydantic model in collector.py.
type Span struct {
	TraceID      string            `json:"trace_id"`
	SpanID       string            `json:"span_id"`
	ParentSpanID string            `json:"parent_span_id,omitempty"`
	Name         string            `json:"name"`
	Service      string            `json:"service"`
	StartTimeUS  int64             `json:"start_time_us"`
	DurationUS   int64             `json:"duration_us"`
	Status       int               `json:"status"`
	Error        bool              `json:"error"`
	Tags         map[string]string `json:"tags,omitempty"`
}

// spanExporter batches spans and ships them to the collector asynchronously.
// The channel acts as a bounded buffer — if the collector is slow, we drop
// rather than block, to protect gateway latency.
type spanExporter struct {
	collectorURL string
	serviceName  string
	ch           chan Span
	wg           sync.WaitGroup
}

func newSpanExporter(collectorURL, serviceName string) *spanExporter {
	e := &spanExporter{
		collectorURL: collectorURL,
		serviceName:  serviceName,
		ch:           make(chan Span, 2048), // buffer 2k spans before dropping
	}
	e.wg.Add(1)
	go e.loop()
	return e
}

// record enqueues a span. Non-blocking — drops if buffer is full rather than
// impacting request latency.
func (e *spanExporter) record(s Span) {
	select {
	case e.ch <- s:
	default:
		// buffer full — drop silently. Observability data is best-effort.
	}
}

// loop drains the channel in batches every 2 seconds, or immediately when
// 100 spans have accumulated.
func (e *spanExporter) loop() {
	defer e.wg.Done()
	batch := make([]Span, 0, 100)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case s, ok := <-e.ch:
			if !ok {
				// Channel closed — flush remaining and exit.
				if len(batch) > 0 {
					e.send(batch)
				}
				return
			}
			batch = append(batch, s)
			if len(batch) >= 100 {
				e.send(batch)
				batch = batch[:0]
			}

		case <-ticker.C:
			if len(batch) > 0 {
				e.send(batch)
				batch = batch[:0]
			}
		}
	}
}

// send POSTs a batch of spans to the collector. Errors are silently dropped —
// tracing is never allowed to affect gateway availability.
func (e *spanExporter) send(spans []Span) {
	data, err := json.Marshal(spans)
	if err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		e.collectorURL+"/api/spans", bytes.NewReader(data))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

// Stop drains remaining spans and waits for the export goroutine to finish.
// Call this during graceful shutdown.
func (e *spanExporter) Stop() {
	close(e.ch)
	e.wg.Wait()
}

// ── responseWriter wrapper ─────────────────────────────────────────────────────

// captureStatus wraps http.ResponseWriter to capture the status code written
// by the next handler, so we can record it in the span.
type captureStatus struct {
	http.ResponseWriter
	status int
}

func (c *captureStatus) WriteHeader(code int) {
	c.status = code
	c.ResponseWriter.WriteHeader(code)
}

// ── middleware ────────────────────────────────────────────────────────────────

// NewTracingMiddleware returns the tracing middleware and the exporter.
// The caller (main.go) should call exporter.Stop() during graceful shutdown.
func NewTracingMiddleware(cfg TracingConfig) (func(http.Handler) http.Handler, *spanExporter) {
	if !cfg.Enabled {
		return func(next http.Handler) http.Handler { return next }, nil
	}

	if cfg.SampleRate <= 0 {
		cfg.SampleRate = 1.0
	}

	exp := newSpanExporter(cfg.CollectorURL, cfg.ServiceName)

	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Sampling: skip tracing for a fraction of requests based on rate.
			// We use a simple random check — no head-based sampling state needed
			// at this scale.
			if cfg.SampleRate < 1.0 && rand.Float64() > cfg.SampleRate {
				next.ServeHTTP(w, r)
				return
			}

			traceID, spanID, parentSpanID := extractOrGenerateIDs(r)

			// Propagate W3C traceparent downstream so backend services can
			// join the same trace without knowing about our internal headers.
			w3c := fmt.Sprintf("00-%s-%s-01", traceID, spanID)
			r.Header.Set("traceparent", w3c)
			r.Header.Set("X-Trace-ID", traceID)
			r.Header.Set("X-Span-ID", spanID)

			// Echo back so callers can correlate their request with traces in the UI.
			w.Header().Set("X-Trace-ID", traceID)

			cw := &captureStatus{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(cw, r)
			dur := time.Since(start)

			exp.record(Span{
				TraceID:      traceID,
				SpanID:       spanID,
				ParentSpanID: parentSpanID,
				Name:         r.Method + " " + r.URL.Path,
				Service:      cfg.ServiceName,
				StartTimeUS:  start.UnixMicro(),
				DurationUS:   dur.Microseconds(),
				Status:       cw.status,
				Error:        cw.status >= 500,
				Tags: map[string]string{
					"http.method":  r.Method,
					"http.path":    r.URL.Path,
					"http.status":  fmt.Sprintf("%d", cw.status),
					"remote.addr":  r.RemoteAddr,
					"user.agent":   r.UserAgent(),
				},
			})
		})
	}

	return mw, exp
}

// extractOrGenerateIDs reads W3C traceparent or our own X-Trace-ID / X-Span-ID
// headers from the incoming request. If none are present, a new root trace is
// started. The returned parentSpanID is empty for root spans.
func extractOrGenerateIDs(r *http.Request) (traceID, spanID, parentSpanID string) {
	// Prefer W3C traceparent: "00-<traceid>-<parentspanid>-<flags>"
	if tp := r.Header.Get("traceparent"); tp != "" {
		parts := strings.Split(tp, "-")
		if len(parts) == 4 {
			traceID = parts[1]
			parentSpanID = parts[2]
			spanID = newHex(16)
			return
		}
	}

	// Fall back to our own headers (useful when calling from curl or clients
	// that don't speak W3C trace context).
	if id := r.Header.Get("X-Trace-ID"); id != "" {
		traceID = id
		parentSpanID = r.Header.Get("X-Span-ID")
		spanID = newHex(16)
		return
	}

	// New root trace — no parent.
	traceID = newHex(32)
	spanID = newHex(16)
	parentSpanID = ""
	return
}

// newHex returns a random lowercase hex string of the given length.
// Used for trace and span IDs.
func newHex(n int) string {
	const chars = "0123456789abcdef"
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[rand.Intn(16)]
	}
	return string(b)
}
