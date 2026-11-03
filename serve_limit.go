package main

import (
	"net"
	"net/http"
	"sync"
	"time"
)

const defaultRate = 10

// bucket is one client's token bucket: tokens whole tokens, refilled from
// last in whole steps of the limiter's interval.
type bucket struct {
	tokens int64
	last   time.Time
}

// limiter is a token bucket per client IP. Each bucket holds up to rate
// tokens and gains one every second/rate, so a client gets rate requests
// per second. The refill step uses integer nanoseconds, so there is no
// float rounding at whole-token boundaries.
type limiter struct {
	rate     int64
	interval time.Duration
	now      func() time.Time

	mu      sync.Mutex
	clients map[string]*bucket
}

// newLimiter returns a limiter for rate requests per second per client that
// reads the time from now.
func newLimiter(rate int, now func() time.Time) *limiter {
	return &limiter{
		rate:     int64(rate),
		interval: time.Second / time.Duration(rate),
		now:      now,
		clients:  map[string]*bucket{},
	}
}

// allow takes a token from client's bucket and reports whether there was one.
// A client seen for the first time starts with a full bucket.
func (l *limiter) allow(client string) bool {
	t := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.clients[client]
	if !ok {
		b = &bucket{tokens: l.rate, last: t}
		l.clients[client] = b
	}
	if t.After(b.last) {
		if gained := int64(t.Sub(b.last) / l.interval); gained > 0 {
			if b.tokens += gained; b.tokens >= l.rate {
				b.tokens = l.rate
				b.last = t
			} else {
				b.last = b.last.Add(time.Duration(gained) * l.interval)
			}
		}
	}
	if b.tokens == 0 {
		return false
	}
	b.tokens--
	return true
}

// clientIP is the host part of the connection's remote address. Proxy
// headers such as X-Forwarded-For are never consulted.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// limitHandler wraps h so every request except path /healthz takes a token
// from its client's bucket. Without one the request gets 429 and h never
// sees it.
func limitHandler(h http.Handler, l *limiter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" && !l.allow(clientIP(r)) {
			w.Header().Set("Retry-After", "1")
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limited"})
			return
		}
		h.ServeHTTP(w, r)
	})
}
