// Package health provides active health-checking of upstream backends.
// It periodically probes each backend's health endpoint and updates the
// backend's alive flag so the load balancer skips unhealthy nodes.
package health

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/sneha4175/gateway-pro/internal/loadbalancer"
	"go.uber.org/zap"
)

const (
	defaultCheckInterval = 10 * time.Second
	defaultTimeout       = 3 * time.Second
	defaultHealthPath    = "/health"
)

// Checker continuously polls backends and flips their alive flag.
type Checker struct {
	mu       sync.Mutex
	backends []*loadbalancer.Backend
	client   *http.Client
	interval time.Duration
	path     string
	log      *zap.SugaredLogger
	cancel   context.CancelFunc
}

// New creates and immediately starts a Checker.
func New(backends []*loadbalancer.Backend, log *zap.SugaredLogger) *Checker {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Checker{
		backends: backends,
		client: &http.Client{
			Timeout: defaultTimeout,
			// Don't follow redirects on health checks
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		interval: defaultCheckInterval,
		path:     defaultHealthPath,
		log:      log,
		cancel:   cancel,
	}
	go c.run(ctx)
	return c
}

// Update swaps in a new backend list without restarting the loop.
func (c *Checker) Update(backends []*loadbalancer.Backend) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.backends = backends
}

// Stop cancels the background goroutine.
func (c *Checker) Stop() { c.cancel() }

func (c *Checker) run(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	// Do one immediate check on startup
	c.checkAll(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.checkAll(ctx)
		}
	}
}

func (c *Checker) checkAll(ctx context.Context) {
	c.mu.Lock()
	bs := make([]*loadbalancer.Backend, len(c.backends))
	copy(bs, c.backends)
	c.mu.Unlock()

	var wg sync.WaitGroup
	for _, b := range bs {
		wg.Add(1)
		go func(backend *loadbalancer.Backend) {
			defer wg.Done()
			c.checkOne(ctx, backend)
		}(b)
	}
	wg.Wait()
}

func (c *Checker) checkOne(ctx context.Context, b *loadbalancer.Backend) {
	url := b.URL + c.path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		c.setAlive(b, false)
		return
	}

	resp, err := c.client.Do(req)
	if err != nil {
		if b.IsAlive() {
			c.log.Warnw("backend unhealthy", "url", b.URL, "err", err)
		}
		c.setAlive(b, false)
		return
	}
	resp.Body.Close()

	alive := resp.StatusCode < 500
	if !b.IsAlive() && alive {
		c.log.Infow("backend recovered", "url", b.URL, "status", resp.StatusCode)
	}
	c.setAlive(b, alive)
}

func (c *Checker) setAlive(b *loadbalancer.Backend, alive bool) {
	b.SetAlive(alive)
}
