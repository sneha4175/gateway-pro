package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

type Span struct {
	TraceID      string
	SpanID       string
	ParentSpanID string
	Name         string
	StartTime    time.Time
	EndTime      time.Time
	Duration     time.Duration
	Timings      map[string]time.Duration
	Path         string
	Method       string
	StatusCode   int
}

type TraceStore struct {
	mu    sync.RWMutex
	spans []*Span
	max   int
}

func NewTraceStore(max int) *TraceStore {
	return &TraceStore{
		spans: make([]*Span, 0, max),
		max:   max,
	}
}

func (s *TraceStore) Add(span *Span) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.spans) >= s.max {
		s.spans = s.spans[1:]
	}

	s.spans = append(s.spans, span)
}

func (s *TraceStore) GetAll() []*Span {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*Span, len(s.spans))
	copy(result, s.spans)
	return result
}

func Tracing(serviceName string, store *TraceStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			traceID := r.Header.Get("traceparent")
			if traceID == "" {
				traceID = generateTraceID()
			} else {
				parts := parseTraceparent(traceID)
				if parts != nil {
					traceID = parts[1]
				} else {
					traceID = generateTraceID()
				}
			}

			spanID := generateSpanID()

			r.Header.Set("X-Trace-ID", traceID)
			r.Header.Set("traceparent", formatTraceparent(traceID, spanID))

			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}

			timings := make(map[string]time.Duration)

			next.ServeHTTP(sw, r)

			duration := time.Since(start)

			span := &Span{
				TraceID:    traceID,
				SpanID:     spanID,
				Name:       serviceName + " " + r.Method + " " + r.URL.Path,
				StartTime:  start,
				EndTime:    time.Now(),
				Duration:   duration,
				Timings:    timings,
				Path:       r.URL.Path,
				Method:     r.Method,
				StatusCode: sw.status,
			}

			if store != nil {
				store.Add(span)
			}
		})
	}
}

func generateTraceID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func generateSpanID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func formatTraceparent(traceID, spanID string) string {
	return "00-" + traceID + "-" + spanID + "-01"
}

func parseTraceparent(tp string) []string {
	if len(tp) < 55 {
		return nil
	}

	if tp[0:2] != "00" {
		return nil
	}

	parts := make([]string, 4)
	parts[0] = tp[0:2]
	parts[1] = tp[3:35]
	parts[2] = tp[36:52]
	parts[3] = tp[53:55]

	return parts
}
