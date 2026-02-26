// Package loadbalancer implements multiple load-balancing strategies.
// All implementations are goroutine-safe.
package loadbalancer

import (
	"errors"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/sneha4175/gateway-pro/internal/config"
)

// ErrNoHealthyBackend is returned when every backend is unhealthy.
var ErrNoHealthyBackend = errors.New("no healthy backend available")

// Backend represents a single upstream server.
type Backend struct {
	URL    string
	Weight int

	// alive is written by the health-checker and read by the LB; use atomic.
	alive atomic.Bool

	// inflight tracks active connections for least_conn
	inflight atomic.Int64
}

func (b *Backend) IsAlive() bool       { return b.alive.Load() }
func (b *Backend) SetAlive(v bool)     { b.alive.Store(v) }
func (b *Backend) Inflight() int64     { return b.inflight.Load() }
func (b *Backend) Inc()                { b.inflight.Add(1) }
func (b *Backend) Dec()                { b.inflight.Add(-1) }

// Balancer selects the next backend for a given request.
type Balancer interface {
	Next(r *http.Request) (*Backend, error)
	Backends() []*Backend
	Update(cfgs []config.BackendConfig)
}

// ---------------------------------------------------------------------------
// Factory
// ---------------------------------------------------------------------------

func New(algorithm string, cfgs []config.BackendConfig) Balancer {
	backends := buildBackends(cfgs)
	switch algorithm {
	case "least_conn":
		return &leastConn{backends: backends}
	case "weighted":
		return newWeighted(backends)
	case "ip_hash":
		return &ipHash{backends: backends}
	default: // round_robin
		return &roundRobin{backends: backends}
	}
}

func buildBackends(cfgs []config.BackendConfig) []*Backend {
	bs := make([]*Backend, len(cfgs))
	for i, c := range cfgs {
		b := &Backend{URL: c.URL, Weight: c.Weight}
		b.alive.Store(true)
		bs[i] = b
	}
	return bs
}

// ---------------------------------------------------------------------------
// Round-Robin
// ---------------------------------------------------------------------------

type roundRobin struct {
	mu       sync.RWMutex
	backends []*Backend
	counter  atomic.Uint64
}

func (rr *roundRobin) Next(_ *http.Request) (*Backend, error) {
	rr.mu.RLock()
	bs := rr.backends
	rr.mu.RUnlock()

	alive := healthy(bs)
	if len(alive) == 0 {
		return nil, ErrNoHealthyBackend
	}
	idx := rr.counter.Add(1) - 1
	return alive[idx%uint64(len(alive))], nil
}

func (rr *roundRobin) Backends() []*Backend {
	rr.mu.RLock()
	defer rr.mu.RUnlock()
	return rr.backends
}

func (rr *roundRobin) Update(cfgs []config.BackendConfig) {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	rr.backends = mergeBackends(rr.backends, cfgs)
}

// ---------------------------------------------------------------------------
// Least Connections
// ---------------------------------------------------------------------------

type leastConn struct {
	mu       sync.RWMutex
	backends []*Backend
}

func (lc *leastConn) Next(_ *http.Request) (*Backend, error) {
	lc.mu.RLock()
	bs := lc.backends
	lc.mu.RUnlock()

	alive := healthy(bs)
	if len(alive) == 0 {
		return nil, ErrNoHealthyBackend
	}
	best := alive[0]
	for _, b := range alive[1:] {
		if b.Inflight() < best.Inflight() {
			best = b
		}
	}
	return best, nil
}

func (lc *leastConn) Backends() []*Backend {
	lc.mu.RLock()
	defer lc.mu.RUnlock()
	return lc.backends
}

func (lc *leastConn) Update(cfgs []config.BackendConfig) {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	lc.backends = mergeBackends(lc.backends, cfgs)
}

// ---------------------------------------------------------------------------
// Weighted Round-Robin  (smooth weighted, same algo nginx uses)
// ---------------------------------------------------------------------------

type weighted struct {
	mu       sync.Mutex
	backends []*wBackend
}

type wBackend struct {
	*Backend
	current int
}

func newWeighted(bs []*Backend) *weighted {
	wb := make([]*wBackend, len(bs))
	for i, b := range bs {
		wb[i] = &wBackend{Backend: b}
	}
	return &weighted{backends: wb}
}

func (w *weighted) Next(_ *http.Request) (*Backend, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	total := 0
	var best *wBackend
	for _, b := range w.backends {
		if !b.IsAlive() {
			continue
		}
		b.current += b.Weight
		total += b.Weight
		if best == nil || b.current > best.current {
			best = b
		}
	}
	if best == nil {
		return nil, ErrNoHealthyBackend
	}
	best.current -= total
	return best.Backend, nil
}

func (w *weighted) Backends() []*Backend {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]*Backend, len(w.backends))
	for i, b := range w.backends {
		out[i] = b.Backend
	}
	return out
}

func (w *weighted) Update(cfgs []config.BackendConfig) {
	w.mu.Lock()
	defer w.mu.Unlock()
	merged := mergeBackends(backendSlice(w.backends), cfgs)
	wb := make([]*wBackend, len(merged))
	for i, b := range merged {
		wb[i] = &wBackend{Backend: b}
	}
	w.backends = wb
}

// ---------------------------------------------------------------------------
// IP Hash (sticky sessions)
// ---------------------------------------------------------------------------

type ipHash struct {
	mu       sync.RWMutex
	backends []*Backend
}

func (ih *ipHash) Next(r *http.Request) (*Backend, error) {
	ih.mu.RLock()
	bs := ih.backends
	ih.mu.RUnlock()

	alive := healthy(bs)
	if len(alive) == 0 {
		return nil, ErrNoHealthyBackend
	}
	ip := clientIP(r)
	h := fnv1a(ip)
	return alive[h%uint32(len(alive))], nil
}

func (ih *ipHash) Backends() []*Backend {
	ih.mu.RLock()
	defer ih.mu.RUnlock()
	return ih.backends
}

func (ih *ipHash) Update(cfgs []config.BackendConfig) {
	ih.mu.Lock()
	defer ih.mu.Unlock()
	ih.backends = mergeBackends(ih.backends, cfgs)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func healthy(bs []*Backend) []*Backend {
	out := bs[:0:0]
	for _, b := range bs {
		if b.IsAlive() {
			out = append(out, b)
		}
	}
	return out
}

// mergeBackends preserves existing backend objects (keeping their atomic state)
// and only adds/removes entries that changed in the new config.
func mergeBackends(existing []*Backend, cfgs []config.BackendConfig) []*Backend {
	byURL := make(map[string]*Backend, len(existing))
	for _, b := range existing {
		byURL[b.URL] = b
	}
	result := make([]*Backend, 0, len(cfgs))
	for _, c := range cfgs {
		if b, ok := byURL[c.URL]; ok {
			b.Weight = c.Weight
			result = append(result, b)
		} else {
			nb := &Backend{URL: c.URL, Weight: c.Weight}
			nb.alive.Store(true)
			result = append(result, nb)
		}
	}
	return result
}

func backendSlice(wb []*wBackend) []*Backend {
	out := make([]*Backend, len(wb))
	for i, b := range wb {
		out[i] = b.Backend
	}
	return out
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	return r.RemoteAddr
}

// Simple FNV-1a 32-bit hash — no allocations, no imports.
func fnv1a(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}
