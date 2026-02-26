package config

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// Top-level config structs
// ---------------------------------------------------------------------------

type Config struct {
	Server  ServerConfig   `yaml:"server"`
	Admin   AdminConfig    `yaml:"admin"`
	Routes  []RouteConfig  `yaml:"routes"`
	Logging LoggingConfig  `yaml:"logging"`
}

type ServerConfig struct {
	Addr                string `yaml:"addr"`
	ReadTimeoutSeconds  int    `yaml:"read_timeout_seconds"`
	WriteTimeoutSeconds int    `yaml:"write_timeout_seconds"`
}

type AdminConfig struct {
	Addr string `yaml:"addr"`
}

type LoggingConfig struct {
	Level  string `yaml:"level"`  // debug|info|warn|error
	Format string `yaml:"format"` // json|console
}

type RouteConfig struct {
	// Path prefix to match, e.g. /api/users
	PathPrefix string `yaml:"path_prefix"`

	// Upstream backends
	Backends []BackendConfig `yaml:"backends"`

	// Load-balancing algorithm: round_robin | least_conn | weighted | ip_hash
	LBAlgorithm string `yaml:"lb_algorithm"`

	// Optional per-route rate limiting
	RateLimit *RateLimitConfig `yaml:"rate_limit,omitempty"`

	// Optional circuit breaker
	CircuitBreaker *CircuitBreakerConfig `yaml:"circuit_breaker,omitempty"`

	// Request timeout
	TimeoutSeconds int `yaml:"timeout_seconds"`

	// Strip the path prefix before forwarding
	StripPrefix bool `yaml:"strip_prefix"`
}

type BackendConfig struct {
	URL    string `yaml:"url"`
	Weight int    `yaml:"weight"` // used by weighted algorithm; default 1
}

type RateLimitConfig struct {
	// Algorithm: token_bucket | sliding_window
	Algorithm string `yaml:"algorithm"`

	// Requests per second (token_bucket) or per window (sliding_window)
	Rate int `yaml:"rate"`

	// Burst size for token_bucket
	Burst int `yaml:"burst"`

	// Window duration for sliding_window, e.g. "1m"
	Window string `yaml:"window"`

	// Key: ip | user | api_key
	KeyBy string `yaml:"key_by"`

	// Optional Redis URL for distributed limiting; if empty, in-process
	RedisURL string `yaml:"redis_url,omitempty"`
}

type CircuitBreakerConfig struct {
	// Percentage of failures to trip breaker (0-100)
	FailureThreshold int `yaml:"failure_threshold"`

	// Minimum number of requests in the rolling window
	MinRequests int `yaml:"min_requests"`

	// How long to stay open before transitioning to half-open
	OpenDurationSeconds int `yaml:"open_duration_seconds"`

	// Number of probe requests in half-open state
	HalfOpenRequests int `yaml:"half_open_requests"`
}

// ---------------------------------------------------------------------------
// Loader + file watcher
// ---------------------------------------------------------------------------

// Watcher emits new configs when the file changes on disk.
type Watcher struct {
	updates chan *Config
	done    chan struct{}
	once    sync.Once
	fsw     *fsnotify.Watcher
}

func (w *Watcher) Updates() <-chan *Config { return w.updates }

func (w *Watcher) Close() {
	w.once.Do(func() {
		close(w.done)
		w.fsw.Close()
	})
}

// LoadAndWatch reads the config file, starts watching for changes, and
// returns the initial config plus a Watcher whose channel delivers reloads.
func LoadAndWatch(path string, log *zap.SugaredLogger) (*Config, *Watcher, error) {
	cfg, err := load(path)
	if err != nil {
		return nil, nil, err
	}

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, nil, fmt.Errorf("create fsnotify watcher: %w", err)
	}
	if err := fsw.Add(path); err != nil {
		return nil, nil, fmt.Errorf("watch config file: %w", err)
	}

	w := &Watcher{
		updates: make(chan *Config, 1),
		done:    make(chan struct{}),
		fsw:     fsw,
	}

	go func() {
		// debounce rapid saves
		var debounce <-chan time.Time
		for {
			select {
			case <-w.done:
				return
			case event, ok := <-fsw.Events:
				if !ok {
					return
				}
				if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
					debounce = time.After(200 * time.Millisecond)
				}
			case err, ok := <-fsw.Errors:
				if !ok {
					return
				}
				log.Warnw("fsnotify error", "err", err)
			case <-debounce:
				debounce = nil
				newCfg, err := load(path)
				if err != nil {
					log.Warnw("config reload failed, keeping old config", "err", err)
					continue
				}
				// non-blocking send; drop if nobody is consuming fast enough
				select {
				case w.updates <- newCfg:
				default:
				}
			}
		}
	}()

	return cfg, w, nil
}

func load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	// Expand environment variables
	expanded := os.ExpandEnv(string(data))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

func validate(cfg *Config) error {
	if cfg.Server.Addr == "" {
		cfg.Server.Addr = ":8080"
	}
	if cfg.Admin.Addr == "" {
		cfg.Admin.Addr = ":9090"
	}
	if cfg.Server.ReadTimeoutSeconds == 0 {
		cfg.Server.ReadTimeoutSeconds = 30
	}
	if cfg.Server.WriteTimeoutSeconds == 0 {
		cfg.Server.WriteTimeoutSeconds = 30
	}

	for i := range cfg.Routes {
		r := &cfg.Routes[i]
		if r.PathPrefix == "" {
			return fmt.Errorf("route[%d]: path_prefix is required", i)
		}
		if len(r.Backends) == 0 {
			return fmt.Errorf("route %q: at least one backend required", r.PathPrefix)
		}
		for j := range r.Backends {
			if r.Backends[j].Weight == 0 {
				r.Backends[j].Weight = 1
			}
		}
		if r.LBAlgorithm == "" {
			r.LBAlgorithm = "round_robin"
		}
		if r.TimeoutSeconds == 0 {
			r.TimeoutSeconds = 30
		}
	}
	return nil
}
