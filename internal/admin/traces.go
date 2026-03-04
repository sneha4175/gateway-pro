package admin

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/sneha4175/gateway-pro/internal/middleware"
)

type TraceResponse struct {
	TraceID    string             `json:"trace_id"`
	Path       string             `json:"path"`
	Method     string             `json:"method"`
	StatusCode int                `json:"status_code"`
	Duration   float64            `json:"duration_ms"`
	StartTime  time.Time          `json:"start_time"`
	Timings    []MiddlewareTiming `json:"middleware_timings"`
}

type MiddlewareTiming struct {
	Name     string  `json:"name"`
	Duration float64 `json:"duration_ms"`
}

func RegisterTraceHandlers(mux *http.ServeMux, store *middleware.TraceStore) {
	mux.HandleFunc("/traces", tracesHandler(store))
}

func tracesHandler(store *middleware.TraceStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		spans := store.GetAll()

		responses := make([]TraceResponse, len(spans))
		for i, span := range spans {
			timings := make([]MiddlewareTiming, 0, len(span.Timings))
			for name, duration := range span.Timings {
				timings = append(timings, MiddlewareTiming{
					Name:     name,
					Duration: float64(duration.Nanoseconds()) / 1e6,
				})
			}

			responses[i] = TraceResponse{
				TraceID:    span.TraceID,
				Path:       span.Path,
				Method:     span.Method,
				StatusCode: span.StatusCode,
				Duration:   float64(span.Duration.Nanoseconds()) / 1e6,
				StartTime:  span.StartTime,
				Timings:    timings,
			}
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(responses); err != nil {
			http.Error(w, "failed to encode response", http.StatusInternalServerError)
		}
	}
}
