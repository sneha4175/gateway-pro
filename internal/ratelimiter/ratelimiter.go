// Package ratelimiter provides per-key rate limiting using two algorithms:
// token bucket (good for bursty traffic) and sliding window (precise).
// Both algorithms work in-process (zero deps) or with Redis for distributed use.
package ratelimiter

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sneha4175/gateway-pro/internal/config"
)

// ErrRateLimited is returned when a key has exceeded its limit.
type ErrRateLimited struct {
	RetryAfter time.Duration
}

func (e *ErrRateLimited) Error() string {
	return fmt.Sprintf("rate limit exceeded; retry after %s", e.RetryAfter)
}

// Limiter checks whether a request should be allowed.
type Limiter interface {
	Allow(r *http.Request) error
}

// New constructs the appropriate limiter from config.
// If cfg is nil, a no-op limiter is returned.
func New(cfg *config.RateLimitConfig) (Limiter, error) {
	if cfg == nil {
		return noopLimiter{}, nil
	}

	keyFn := buildKeyFn(cfg.KeyBy)

	if cfg.RedisURL != "" {
		return newRedisLimiter(cfg, keyFn)
	}

	switch cfg.Algorithm {
	case "sliding_window":
		window, err := time.ParseDuration(cfg.Window)
		if err != nil {
			return nil, fmt.Errorf("invalid window %q: %w", cfg.Window, err)
		}
		return &localSlidingWindow{
			rate:   cfg.Rate,
			window: window,
			keyFn:  keyFn,
			buckets: make(map[string]*swBucket),
		}, nil
	default: // token_bucket
		return &localTokenBucket{
			rate:    float64(cfg.Rate),
			burst:   cfg.Burst,
			keyFn:   keyFn,
			buckets: make(map[string]*tbBucket),
		}, nil
	}
}

// ---------------------------------------------------------------------------
// No-op
// ---------------------------------------------------------------------------

type noopLimiter struct{}

func (noopLimiter) Allow(_ *http.Request) error { return nil }

// ---------------------------------------------------------------------------
// Key extraction
// ---------------------------------------------------------------------------

func buildKeyFn(keyBy string) func(r *http.Request) string {
	switch keyBy {
	case "api_key":
		return func(r *http.Request) string {
			if k := r.Header.Get("X-API-Key"); k != "" {
				return "apikey:" + k
			}
			return "apikey:anonymous"
		}
	case "user":
		return func(r *http.Request) string {
			if u := r.Header.Get("X-User-ID"); u != "" {
				return "user:" + u
			}
			return "user:anonymous"
		}
	default: // ip
		return func(r *http.Request) string {
			if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
				return "ip:" + xff
			}
			return "ip:" + r.RemoteAddr
		}
	}
}

// ---------------------------------------------------------------------------
// Local Token Bucket
// ---------------------------------------------------------------------------

type tbBucket struct {
	mu       sync.Mutex
	tokens   float64
	lastFill time.Time
}

type localTokenBucket struct {
	mu      sync.RWMutex
	buckets map[string]*tbBucket
	rate    float64 // tokens per second
	burst   int
	keyFn   func(r *http.Request) string
}

func (l *localTokenBucket) Allow(r *http.Request) error {
	key := l.keyFn(r)
	bucket := l.getOrCreate(key)

	bucket.mu.Lock()
	defer bucket.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(bucket.lastFill).Seconds()
	bucket.tokens = min(float64(l.burst), bucket.tokens+elapsed*l.rate)
	bucket.lastFill = now

	if bucket.tokens < 1 {
		wait := time.Duration((1-bucket.tokens)/l.rate*1e9) * time.Nanosecond
		return &ErrRateLimited{RetryAfter: wait}
	}
	bucket.tokens--
	return nil
}

func (l *localTokenBucket) getOrCreate(key string) *tbBucket {
	l.mu.RLock()
	b, ok := l.buckets[key]
	l.mu.RUnlock()
	if ok {
		return b
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if b, ok = l.buckets[key]; ok {
		return b
	}
	b = &tbBucket{tokens: float64(l.burst), lastFill: time.Now()}
	l.buckets[key] = b
	return b
}

// ---------------------------------------------------------------------------
// Local Sliding Window
// ---------------------------------------------------------------------------

type swBucket struct {
	mu         sync.Mutex
	timestamps []time.Time
}

type localSlidingWindow struct {
	mu      sync.RWMutex
	buckets map[string]*swBucket
	rate    int
	window  time.Duration
	keyFn   func(r *http.Request) string
}

func (l *localSlidingWindow) Allow(r *http.Request) error {
	key := l.keyFn(r)
	bucket := l.swGetOrCreate(key)

	bucket.mu.Lock()
	defer bucket.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-l.window)

	// Evict old entries
	i := 0
	for i < len(bucket.timestamps) && bucket.timestamps[i].Before(cutoff) {
		i++
	}
	bucket.timestamps = bucket.timestamps[i:]

	if len(bucket.timestamps) >= l.rate {
		oldest := bucket.timestamps[0]
		retryAfter := oldest.Add(l.window).Sub(now)
		return &ErrRateLimited{RetryAfter: retryAfter}
	}
	bucket.timestamps = append(bucket.timestamps, now)
	return nil
}

func (l *localSlidingWindow) swGetOrCreate(key string) *swBucket {
	l.mu.RLock()
	b, ok := l.buckets[key]
	l.mu.RUnlock()
	if ok {
		return b
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if b, ok = l.buckets[key]; ok {
		return b
	}
	b = &swBucket{}
	l.buckets[key] = b
	return b
}

// ---------------------------------------------------------------------------
// Redis-backed (distributed) limiter — uses Lua script for atomicity
// ---------------------------------------------------------------------------

// Sliding window in Redis using a sorted set.
// Each request adds current timestamp; expired entries are pruned atomically.
const slidingWindowLua = `
local key    = KEYS[1]
local now    = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local limit  = tonumber(ARGV[3])
local cutoff = now - window

redis.call('ZREMRANGEBYSCORE', key, '-inf', cutoff)
local count = redis.call('ZCARD', key)
if count >= limit then
  local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
  return {0, oldest[2]}
end
redis.call('ZADD', key, now, now)
redis.call('EXPIRE', key, math.ceil(window/1000))
return {1, 0}
`

type redisLimiter struct {
	client *redis.Client
	script *redis.Script
	cfg    *config.RateLimitConfig
	window time.Duration
	keyFn  func(r *http.Request) string
}

func newRedisLimiter(cfg *config.RateLimitConfig, keyFn func(r *http.Request) string) (*redisLimiter, error) {
	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	client := redis.NewClient(opts)

	window, _ := time.ParseDuration(cfg.Window)
	if window == 0 {
		window = time.Second
	}

	return &redisLimiter{
		client: client,
		script: redis.NewScript(slidingWindowLua),
		cfg:    cfg,
		window: window,
		keyFn:  keyFn,
	}, nil
}

func (rl *redisLimiter) Allow(r *http.Request) error {
	key := "rl:" + rl.keyFn(r)
	nowMs := time.Now().UnixMilli()
	windowMs := rl.window.Milliseconds()

	ctx, cancel := context.WithTimeout(r.Context(), 50*time.Millisecond)
	defer cancel()

	res, err := rl.script.Run(ctx, rl.client, []string{key},
		nowMs, windowMs, rl.cfg.Rate).Int64Slice()
	if err != nil {
		// Redis unavailable — fail open (allow the request)
		return nil
	}

	if res[0] == 0 {
		oldestMs := res[1]
		retryAfter := time.Duration(oldestMs+windowMs-nowMs) * time.Millisecond
		return &ErrRateLimited{RetryAfter: retryAfter}
	}
	return nil
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
