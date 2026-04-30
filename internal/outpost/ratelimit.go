package outpost

import (
	"sync"
	"time"
)

type RateLimiter struct {
	mu      sync.Mutex
	windows map[string]rateWindow
}

type rateWindow struct {
	start time.Time
	count int
}

func NewRateLimiter() *RateLimiter {
	return &RateLimiter{windows: map[string]rateWindow{}}
}

func (r *RateLimiter) Allow(key APIKey) bool {
	if key.RequestsPerMinute == 0 {
		return true
	}

	now := time.Now()
	windowStart := now.Truncate(time.Minute)

	r.mu.Lock()
	defer r.mu.Unlock()

	window := r.windows[key.ID]
	if !window.start.Equal(windowStart) {
		window = rateWindow{start: windowStart}
	}
	if window.count >= key.RequestsPerMinute {
		r.windows[key.ID] = window
		return false
	}
	window.count++
	r.windows[key.ID] = window
	return true
}
