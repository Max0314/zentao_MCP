package middleware

import (
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"
)

// ZentaoAccessOptions limits incoming traffic and uncached password logins.
// Zero values use defaults; configuration validation rejects negative values.
type ZentaoAccessOptions struct {
	RequestRate, IPRequestRate   float64
	RequestBurst, IPRequestBurst int
	LoginRate, IPLoginRate       float64
	LoginBurst, IPLoginBurst     int
	TrustedProxies               []netip.Prefix
}

func (o ZentaoAccessOptions) normalized() ZentaoAccessOptions {
	if o.RequestRate == 0 {
		o.RequestRate = 60
	}
	if o.RequestBurst == 0 {
		o.RequestBurst = 120
	}
	if o.IPRequestRate == 0 {
		o.IPRequestRate = 30
	}
	if o.IPRequestBurst == 0 {
		o.IPRequestBurst = 60
	}
	if o.LoginRate == 0 {
		o.LoginRate = 4
	}
	if o.LoginBurst == 0 {
		o.LoginBurst = 8
	}
	if o.IPLoginRate == 0 {
		o.IPLoginRate = 1
	}
	if o.IPLoginBurst == 0 {
		o.IPLoginBurst = 3
	}
	return o
}

type accessBucket struct {
	tokens float64
	last   time.Time
}

func (b *accessBucket) refill(now time.Time, rate float64, burst int) {
	if b.last.IsZero() {
		b.tokens = float64(burst)
	} else if now.After(b.last) {
		b.tokens = min(float64(burst), b.tokens+now.Sub(b.last).Seconds()*rate)
	}
	b.last = now
}

func (b *accessBucket) wait(rate float64) time.Duration {
	if b.tokens >= 1 {
		return 0
	}
	// Very small configured rates must not overflow Duration and accidentally
	// turn a positive wait into permission to proceed.
	seconds := min((1-b.tokens)/rate, float64(time.Hour/time.Second))
	return time.Duration(seconds*float64(time.Second)) + time.Nanosecond
}

// One limiter is shared by /mcp and /sse for an upstream. Active IP buckets
// are not evicted: eviction would let a flood reset its own rate budget.
type accessLimiter struct {
	mu             sync.Mutex
	rate, ipRate   float64
	burst, ipBurst int
	global         accessBucket
	ips            map[string]accessBucket
	maxEntries     int
	idleTTL        time.Duration
	nextCleanup    time.Time
	now            func() time.Time
}

func newAccessLimiter(rate float64, burst int, ipRate float64, ipBurst int) *accessLimiter {
	return &accessLimiter{rate: rate, burst: burst, ipRate: ipRate, ipBurst: ipBurst, ips: make(map[string]accessBucket), maxEntries: 2048, idleTTL: 10 * time.Minute, now: time.Now}
}

func (l *accessLimiter) allow(ip string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if !now.Before(l.nextCleanup) {
		for key, bucket := range l.ips {
			if now.Sub(bucket.last) >= l.idleTTL {
				delete(l.ips, key)
			}
		}
		l.nextCleanup = now.Add(time.Minute)
	}
	l.global.refill(now, l.rate, l.burst)
	bucket, exists := l.ips[ip]
	if !exists && len(l.ips) >= l.maxEntries {
		return authThrottleError("来源保护容量已满，请稍后重试", time.Minute)
	}
	bucket.refill(now, l.ipRate, l.ipBurst)
	l.ips[ip] = bucket
	if wait := max(l.global.wait(l.rate), bucket.wait(l.ipRate)); wait > 0 {
		return authThrottleError("请求过于频繁，请稍后重试", wait)
	}
	l.global.tokens--
	bucket.tokens--
	l.ips[ip] = bucket
	return nil
}

// X-Real-IP is honored only for explicitly trusted direct peers. The nginx
// template overwrites it with $remote_addr, rather than forwarding client input.
func clientIP(r *http.Request, trusted []netip.Prefix) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return "unknown"
	}
	addr = addr.Unmap()
	for _, prefix := range trusted {
		if prefix.Contains(addr) {
			if forwarded, err := netip.ParseAddr(r.Header.Get("X-Real-IP")); err == nil {
				return forwarded.Unmap().String()
			}
			break
		}
	}
	return addr.String()
}
