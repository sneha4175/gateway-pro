// tracing.go — W3C traceparent propagation + async span export for gateway-pro.
// Also provides TraceStore (in-memory ring buffer) used by /traces admin endpoint.
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
	CollectorURL string  `yaml:"collector_url"`
	ServiceName  string  `yaml:"service_name"`
	SampleRate   float64 `yaml:"sample_rate"`
}

// TraceSpan is one recorded request stored in TraceStore and served at /traces.
type TraceSpan struct {
	TraceID    string
	Path       string
	Method     string
	StatusCode int
	Duration   time.Duration
	StartTime  time.Time
	Timings    map[string]time.Duration
}

// TraceStore is a bounded ring buffer of recent TraceSpans.
// gateway.go passes *TraceStore to buildRoute so Tracing middleware can record into it.
type TraceStore struct {
	mu    sync.RWMutex
	spans []TraceSpan
	max   int
}

func NewTraceStore(max int) *TraceStore {
	if max <= 0 {
		max = 100
	}
	return &TraceStore{spans: make([]TraceSpan, 0, max), max: max}
}

func (ts *TraceStore) Add(s TraceSpan) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.spans) >= ts.max {
		ts.spans = ts.spans[1:]
	}
	ts.spans = append(ts.spans, s)
}

// GetAll returns a copy of all stored spans. Used by admin/traces.go.
func (ts *TraceStore) GetAll() []TraceSpan {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	out := make([]TraceSpan, len(ts.spans))
	copy(out, ts.spans)
	return out
}

// Span is what we ship to the external FastAPI collector.
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
		ch:           make(chan Span, 2048),
	}
	e.wg.Add(1)
	go e.loop()
	return e
}

func (e *spanExporter) record(s Span) {
	select {
	case e.ch <- s:
	default:
	}
}

func (e *spanExporter) loop() {
	defer e.wg.Done()
	batch := make([]Span, 0, 100)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case s, ok := <-e.ch:
			if !ok {
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

// Stop drains remaining spans. Call during graceful shutdown.
func (e *spanExporter) Stop() {
	close(e.ch)
	e.wg.Wait()
}

type captureStatus struct {
	http.ResponseWriter
	status int
}

func (c *captureStatus) WriteHeader(code int) {
	c.status = code
	c.ResponseWriter.WriteHeader(code)
}

// Tracing is the middleware used by gateway.go's buildRoute().
// It injects W3C traceparent headers and records a TraceSpan into store.
func Tracing(serviceName string, store *TraceStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			traceID, spanID, _ := extractOrGenerateIDs(r)

			w3c := fmt.Sprintf("00-%s-%s-01", traceID, spanID)
			r.Header.Set("traceparent", w3c)
			r.Header.Set("X-Trace-ID", traceID)
			r.Header.Set("X-Span-ID", spanID)
			w.Header().Set("X-Trace-ID", traceID)

			cw := &captureStatus{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(cw, r)
			dur := time.Since(start)

			if store != nil {
				store.Add(TraceSpan{
					TraceID:    traceID,
					Path:       r.URL.Path,
					Method:     r.Method,
					StatusCode: cw.status,
					Duration:   dur,
					StartTime:  start,
					Timings:    map[string]time.Duration{"total": dur},
				})
			}
		})
	}
}

// NewTracingMiddleware is used when external collector export is also needed.
// Returns the middleware + exporter (call exporter.Stop() on graceful shutdown).
func NewTracingMiddleware(cfg TracingConfig) (func(http.Handler) http.Handler, *spanExporter) {
	if !cfg.Enabled {
		return func(next http.Handler) http.Handler { return next }, nil
	}
	if cfg.SampleRate <= 0 {
		cfg.SampleRate = 1.0
	}

	store := NewTraceStore(100)
	exp := newSpanExporter(cfg.CollectorURL, cfg.ServiceName)

	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if cfg.SampleRate < 1.0 && rand.Float64() > cfg.SampleRate {
				next.ServeHTTP(w, r)
				return
			}
			traceID, spanID, parentSpanID := extractOrGenerateIDs(r)

			w3c := fmt.Sprintf("00-%s-%s-01", traceID, spanID)
			r.Header.Set("traceparent", w3c)
			r.Header.Set("X-Trace-ID", traceID)
			r.Header.Set("X-Span-ID", spanID)
			w.Header().Set("X-Trace-ID", traceID)

			cw := &captureStatus{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(cw, r)
			dur := time.Since(start)

			store.Add(TraceSpan{
				TraceID:    traceID,
				Path:       r.URL.Path,
				Method:     r.Method,
				StatusCode: cw.status,
				Duration:   dur,
				StartTime:  start,
				Timings:    map[string]time.Duration{"total": dur},
			})

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
					"http.method": r.Method,
					"http.path":   r.URL.Path,
					"http.status": fmt.Sprintf("%d", cw.status),
					"remote.addr": r.RemoteAddr,
					"user.agent":  r.UserAgent(),
				},
			})
		})
	}

	return mw, exp
}

func extractOrGenerateIDs(r *http.Request) (traceID, spanID, parentSpanID string) {
	if tp := r.Header.Get("traceparent"); tp != "" {
		parts := strings.Split(tp, "-")
		if len(parts) == 4 {
			return parts[1], newHex(16), parts[2]
		}
	}
	if id := r.Header.Get("X-Trace-ID"); id != "" {
		return id, newHex(16), r.Header.Get("X-Span-ID")
	}
	return newHex(32), newHex(16), ""
}

func newHex(n int) string {
	const chars = "0123456789abcdef"
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[rand.Intn(16)]
	}
	return string(b)
}
