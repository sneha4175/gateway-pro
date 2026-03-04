package middleware

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeCollector stands in for the Python FastAPI collector.
// It records all span batches it receives so tests can assert on them.
type fakeCollector struct {
	server *httptest.Server
	spans  []Span
}

func newFakeCollector(t *testing.T) *fakeCollector {
	t.Helper()
	fc := &fakeCollector{}
	fc.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/spans" || r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var batch []Span
		if err := json.Unmarshal(body, &batch); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		fc.spans = append(fc.spans, batch...)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(fc.server.Close)
	return fc
}

// flush stops the exporter and waits for the batch to drain.
func flushExporter(exp *spanExporter) {
	if exp != nil {
		exp.Stop()
	}
}

func TestTracingMiddleware_Disabled(t *testing.T) {
	mw, exp := NewTracingMiddleware(TracingConfig{Enabled: false})
	if exp != nil {
		t.Error("expected nil exporter when tracing disabled")
	}

	called := false
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	rr := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rr, req)

	if !called {
		t.Error("next handler should be called when tracing disabled")
	}
}

func TestTracingMiddleware_InjectsHeaders(t *testing.T) {
	fc := newFakeCollector(t)
	mw, exp := NewTracingMiddleware(TracingConfig{
		Enabled:      true,
		CollectorURL: fc.server.URL,
		ServiceName:  "test-gateway",
		SampleRate:   1.0,
	})
	defer flushExporter(exp)

	var gotTraceID, gotTraceparent string
	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	rr := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTraceID = r.Header.Get("X-Trace-ID")
		gotTraceparent = r.Header.Get("traceparent")
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rr, req)

	if gotTraceID == "" {
		t.Error("expected X-Trace-ID to be injected")
	}
	if len(gotTraceID) != 32 {
		t.Errorf("X-Trace-ID should be 32 chars, got %d: %q", len(gotTraceID), gotTraceID)
	}
	if !strings.HasPrefix(gotTraceparent, "00-") {
		t.Errorf("traceparent should start with '00-', got %q", gotTraceparent)
	}
	// Response must also carry X-Trace-ID for caller correlation.
	if rr.Header().Get("X-Trace-ID") == "" {
		t.Error("expected X-Trace-ID in response headers")
	}
}

func TestTracingMiddleware_PropagatesIncomingTrace(t *testing.T) {
	fc := newFakeCollector(t)
	mw, exp := NewTracingMiddleware(TracingConfig{
		Enabled:      true,
		CollectorURL: fc.server.URL,
		ServiceName:  "test-gateway",
		SampleRate:   1.0,
	})
	defer flushExporter(exp)

	incomingTraceID := strings.Repeat("a", 32)
	incomingSpanID := strings.Repeat("b", 16)
	traceparent := "00-" + incomingTraceID + "-" + incomingSpanID + "-01"

	var gotTraceID string
	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	req.Header.Set("traceparent", traceparent)
	rr := httptest.NewRecorder()

	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTraceID = r.Header.Get("X-Trace-ID")
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rr, req)

	if gotTraceID != incomingTraceID {
		t.Errorf("want trace ID %q propagated, got %q", incomingTraceID, gotTraceID)
	}
}

func TestTracingMiddleware_ExportsSpan(t *testing.T) {
	fc := newFakeCollector(t)
	mw, exp := NewTracingMiddleware(TracingConfig{
		Enabled:      true,
		CollectorURL: fc.server.URL,
		ServiceName:  "gateway-pro",
		SampleRate:   1.0,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/orders", nil)
	rr := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})).ServeHTTP(rr, req)

	// Stop the exporter to flush the batch synchronously.
	flushExporter(exp)

	// Give the fake collector a moment to process.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(fc.spans) == 0 {
		time.Sleep(50 * time.Millisecond)
	}

	if len(fc.spans) == 0 {
		t.Fatal("expected at least one span to be exported")
	}

	s := fc.spans[0]
	if s.Service != "gateway-pro" {
		t.Errorf("want service=gateway-pro, got %q", s.Service)
	}
	if s.Status != http.StatusCreated {
		t.Errorf("want status=201, got %d", s.Status)
	}
	if s.Error {
		t.Error("want error=false for 2xx")
	}
	if s.Tags["http.method"] != "GET" {
		t.Errorf("want tag http.method=GET, got %q", s.Tags["http.method"])
	}
}

func TestTracingMiddleware_MarksErrorSpan(t *testing.T) {
	fc := newFakeCollector(t)
	mw, exp := NewTracingMiddleware(TracingConfig{
		Enabled:      true,
		CollectorURL: fc.server.URL,
		ServiceName:  "gateway-pro",
		SampleRate:   1.0,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/broken", nil)
	rr := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})).ServeHTTP(rr, req)

	flushExporter(exp)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(fc.spans) == 0 {
		time.Sleep(50 * time.Millisecond)
	}

	if len(fc.spans) == 0 {
		t.Fatal("expected span")
	}
	if !fc.spans[0].Error {
		t.Error("want error=true for 5xx response")
	}
}

func TestNewHex(t *testing.T) {
	for _, n := range []int{16, 32} {
		h := newHex(n)
		if len(h) != n {
			t.Errorf("newHex(%d) returned %d chars: %q", n, len(h), h)
		}
		for _, c := range h {
			if !strings.ContainsRune("0123456789abcdef", c) {
				t.Errorf("newHex returned non-hex char %q in %q", c, h)
			}
		}
	}
}
