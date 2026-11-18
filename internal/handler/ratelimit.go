package handler

import (
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/rake-pro/gopaste/internal/config"
)

// rateLimiter is a per-client fixed-window limiter. Within each Every window it
// caps both request count (max) and total accepted paste bytes (maxBytes); a
// zero limit disables that dimension. Both share one window per client so they
// reset together.
type rateLimiter struct {
	max            int
	maxBytes       int
	window         time.Duration
	trustedProxies int

	mu        sync.Mutex
	windows   map[string]*window
	lastSweep time.Time
}

type window struct {
	count int
	bytes int
	reset time.Time
}

// newRateLimiter builds the limiter from rl. It is always returned (even when
// both dimensions are disabled) so the handler can call chargeBytes
// unconditionally; middleware and chargeBytes each no-op when their dimension
// is off.
func newRateLimiter(rl config.RateLimit, trustedProxies int) *rateLimiter {
	return &rateLimiter{
		max:            rl.TotalRequests,
		maxBytes:       rl.MaxBytes,
		window:         time.Duration(rl.Every) * time.Millisecond,
		trustedProxies: trustedProxies,
		windows:        make(map[string]*window),
	}
}

// middleware enforces the request-count limit. With it disabled it is a
// pass-through (the byte limit is enforced in the handler via chargeBytes).
func (l *rateLimiter) middleware(next http.Handler) http.Handler {
	if l.max <= 0 || l.window <= 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.allow(clientIP(r, l.trustedProxies), time.Now()) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"message":"Rate limit exceeded."}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// limiterKey is the bucket key for a client address. IPv6 clients share one
// bucket per /64 (a single host is routinely handed a whole /64, so per-address
// buckets would let it rotate past every limit); IPv4 and unparsable values are
// used as-is.
func limiterKey(ip string) string {
	addr, err := netip.ParseAddr(strings.Trim(ip, "[]"))
	if err != nil {
		return ip
	}
	addr = addr.Unmap()
	if !addr.Is6() {
		return addr.String()
	}
	p, err := addr.WithZone("").Prefix(64)
	if err != nil {
		return ip
	}
	return p.String()
}

// getWindow returns the client's current window, rolling it over when expired.
// Caller must hold l.mu.
func (l *rateLimiter) getWindow(ip string, now time.Time) *window {
	ip = limiterKey(ip)
	win, ok := l.windows[ip]
	if !ok || now.After(win.reset) {
		win = &window{reset: now.Add(l.window)}
		l.windows[ip] = win
		l.sweep(now)
	}
	return win
}

func (l *rateLimiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	win := l.getWindow(ip, now)
	if win.count >= l.max {
		return false
	}
	win.count++
	return true
}

// chargeBytes accounts n accepted paste bytes against the client's window,
// returning false when the byte budget would be exceeded. A no-op (always true)
// when the byte limit is disabled.
func (l *rateLimiter) chargeBytes(ip string, n int, now time.Time) bool {
	if l.maxBytes <= 0 || l.window <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	win := l.getWindow(ip, now)
	if win.bytes+n > l.maxBytes {
		return false
	}
	win.bytes += n
	return true
}

// remainingBytes returns how many more paste bytes the client may send in its
// current window, or -1 when the byte limit is disabled. chargeBytes stays the
// authoritative check; this only lets the handler stop reading early.
func (l *rateLimiter) remainingBytes(ip string, now time.Time) int64 {
	if l.maxBytes <= 0 || l.window <= 0 {
		return -1
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return int64(max(l.maxBytes-l.getWindow(ip, now).bytes, 0))
}

// sweep drops expired windows. Called opportunistically on new-window creation
// to bound map growth without a background goroutine. The full scan runs at
// most once per window, so a flood of new clients does not rescan the whole map
// (under the lock every request takes) for each new address.
func (l *rateLimiter) sweep(now time.Time) {
	if len(l.windows) < 1024 || now.Sub(l.lastSweep) < l.window {
		return
	}
	l.lastSweep = now
	for ip, win := range l.windows {
		if now.After(win.reset) {
			delete(l.windows, ip)
		}
	}
}
