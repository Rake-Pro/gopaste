package handler

import (
	"net/http"
	"sync"
	"time"

	"github.com/rake-pro/gopaste/internal/config"
)

// rateLimiter is a per-client fixed-window limiter: at most TotalRequests per
// Every window. A zero TotalRequests disables limiting.
type rateLimiter struct {
	max    int
	window time.Duration

	mu      sync.Mutex
	windows map[string]*window
}

type window struct {
	count int
	reset time.Time
}

// newRateLimiter returns middleware enforcing rl. When disabled it returns a
// pass-through.
func newRateLimiter(rl config.RateLimit) Middleware {
	if rl.TotalRequests <= 0 || rl.Every <= 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	l := &rateLimiter{
		max:     rl.TotalRequests,
		window:  time.Duration(rl.Every) * time.Millisecond,
		windows: make(map[string]*window),
	}
	return l.middleware
}

func (l *rateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.allow(clientIP(r), time.Now()) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"message":"Rate limit exceeded."}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (l *rateLimiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	win, ok := l.windows[ip]
	if !ok || now.After(win.reset) {
		l.windows[ip] = &window{count: 1, reset: now.Add(l.window)}
		l.sweep(now)
		return true
	}
	if win.count >= l.max {
		return false
	}
	win.count++
	return true
}

// sweep drops expired windows. Called opportunistically on new-window creation
// to bound map growth without a background goroutine.
func (l *rateLimiter) sweep(now time.Time) {
	if len(l.windows) < 1024 {
		return
	}
	for ip, win := range l.windows {
		if now.After(win.reset) {
			delete(l.windows, ip)
		}
	}
}
