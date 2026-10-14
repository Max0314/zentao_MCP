package middleware

import (
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

func TestAccessLimiterPerIPAndGlobalBudgets(t *testing.T) {
	l := newAccessLimiter(1, 3, 1, 2)
	now := time.Unix(1_000_000, 0)
	l.now = func() time.Time { return now }
	for _, ip := range []string{"a", "a", "b"} {
		if err := l.allow(ip); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.allow("a"); err == nil {
		t.Fatal("IP exceeded its burst")
	}
	if err := l.allow("c"); err == nil {
		t.Fatal("rotating IP bypassed global limit")
	}
	now = now.Add(time.Second)
	if err := l.allow("a"); err != nil {
		t.Fatal("budget did not replenish", err)
	}
}

func TestAccessLimiterTinyRateCannotOverflowWait(t *testing.T) {
	l := newAccessLimiter(1e-300, 1, 1e-300, 1)
	if err := l.allow("a"); err != nil {
		t.Fatal(err)
	}
	if err := l.allow("a"); err == nil {
		t.Fatal("duration overflow bypassed rate limit")
	}
}

func TestAccessLimiterBoundedAndExpired(t *testing.T) {
	l := newAccessLimiter(100, 100, 1, 1)
	l.maxEntries = 2
	now := time.Unix(1_000_000, 0)
	l.now = func() time.Time { return now }
	for _, ip := range []string{"a", "b"} {
		if err := l.allow(ip); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.allow("c"); err == nil {
		t.Fatal("active protection entry was evicted")
	}
	if err := l.allow("a"); err == nil {
		t.Fatal("capacity flood reset an existing IP budget")
	}
	now = now.Add(11 * time.Minute)
	if err := l.allow("c"); err != nil {
		t.Fatal("idle entries were not cleaned", err)
	}
	if len(l.ips) != 1 {
		t.Fatal("expired IP entries retained")
	}
}

func TestClientIPTrustsOnlyConfiguredDirectProxy(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("192.168.160.1/32")}
	for _, test := range []struct{ peer, header, want string }{
		{"203.0.113.2:1000", "198.51.100.8", "203.0.113.2"},
		{"192.168.160.1:1000", "198.51.100.8", "198.51.100.8"},
		{"192.168.160.1:1000", "198.51.100.8, 1.2.3.4", "192.168.160.1"},
		{"[::ffff:203.0.113.2]:1000", "198.51.100.8", "203.0.113.2"},
	} {
		r := httptest.NewRequest("POST", "/zentao/mcp", nil)
		r.RemoteAddr = test.peer
		r.Header.Set("X-Real-IP", test.header)
		if got := clientIP(r, trusted); got != test.want {
			t.Fatalf("peer %s: got %s want %s", test.peer, got, test.want)
		}
	}
}
