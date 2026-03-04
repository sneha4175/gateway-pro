package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sneha4175/gateway-pro/internal/middleware"
)

func TestTraces_ReturnsValidJSON(t *testing.T) {
	store := middleware.NewTraceStore(100)

	mux := http.NewServeMux()
	RegisterTraceHandlers(mux, store)

	req := httptest.NewRequest("GET", "/traces", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected %d, got %d", http.StatusOK, rec.Code)
	}

	var response []TraceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Errorf("failed to parse JSON: %v", err)
	}
}

func TestTraces_IncludeTimingBreakdown(t *testing.T) {
	store := middleware.NewTraceStore(100)

	timings := map[string]time.Duration{
		"auth":    5 * time.Millisecond,
		"tracing": 2 * time.Millisecond,
		"metrics": 1 * time.Millisecond,
	}

	store.Add(&middleware.Span{
		TraceID:    "test-trace-123",
		Path:       "/api/users",
		Method:     "GET",
		StatusCode: 200,
		Duration:   10 * time.Millisecond,
		StartTime:  time.Now(),
		Timings:    timings,
	})

	mux := http.NewServeMux()
	RegisterTraceHandlers(mux, store)

	req := httptest.NewRequest("GET", "/traces", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	var response []TraceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if len(response) != 1 {
		t.Fatalf("expected 1 trace, got %d", len(response))
	}

	if response[0].Path != "/api/users" {
		t.Errorf("expected path /api/users, got %s", response[0].Path)
	}

	if len(response[0].Timings) == 0 {
		t.Error("expected middleware timings to be present")
	}
}

func TestTraces_RingBufferEviction(t *testing.T) {
	store := middleware.NewTraceStore(100)

	for i := 0; i < 150; i++ {
		store.Add(&middleware.Span{
			TraceID:    "trace-" + string(rune('0'+i%10)),
			Path:       "/test",
			Method:     "GET",
			StatusCode: 200,
			Duration:   5 * time.Millisecond,
			StartTime:  time.Now(),
			Timings:    map[string]time.Duration{},
		})
	}

	mux := http.NewServeMux()
	RegisterTraceHandlers(mux, store)

	req := httptest.NewRequest("GET", "/traces", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	var response []TraceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if len(response) != 100 {
		t.Errorf("expected 100 traces, got %d", len(response))
	}
}

func TestTraces_EmptyStore_ReturnsEmptyArray(t *testing.T) {
	store := middleware.NewTraceStore(100)

	mux := http.NewServeMux()
	RegisterTraceHandlers(mux, store)

	req := httptest.NewRequest("GET", "/traces", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected %d, got %d", http.StatusOK, rec.Code)
	}

	var response []TraceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if len(response) != 0 {
		t.Errorf("expected empty array, got %d traces", len(response))
	}
}
