// Package clients provides HTTP client constructors.
package clients

import (
	"net/http"
	"time"

	"github.com/merzzzl/openapi-mcp-server/internal/middleware"
)

const (
	// upstreamIdleConnsPerHost caps the idle connections kept per ZenTao host.
	// Go's default is 2, below the 8-way concurrency the bug index crawl uses
	// (indexConcurrency in internal/service/zentao/history.go), so six of every
	// eight connections were closed straight after use and the next request had
	// to dial again. Over plain HTTP on the LAN that cost a few milliseconds.
	// Reaching ZenTao over its public HTTPS entry point it costs a full TLS
	// handshake: measured against that entry, 0.78s per request when every
	// request reconnects against 0.022s when the connection is reused. One full
	// index build is ~1300 requests, every refresh period. The headroom over 8
	// is for user tool calls, which share this pool with the crawl.
	upstreamIdleConnsPerHost = 32

	// upstreamResponseHeaderTimeout bounds the wait for response headers.
	// Nothing on this path had a deadline before, which was survivable on the
	// LAN. Across the internet a silently dropped connection is ordinary, and
	// one of them is enough to stop the bug index refreshing for the life of
	// the process: refreshIndex waits on every crawl worker (forEachBounded's
	// wg.Wait) and RunIndexBuilder calls it synchronously, so a single parked
	// read blocks the refresh ticker forever. The timeout covers the headers
	// only, so a large list page is still free to take its time; it is set
	// well above any healthy response so that a loaded ZenTao is not turned
	// into an error.
	upstreamResponseHeaderTimeout = 60 * time.Second
)

// NewHTTPClient creates an HTTP client with Zentao auth transport.
func NewHTTPClient(base *http.Client, baseURL string) *http.Client {
	if base == nil {
		base = http.DefaultClient
	}

	// Supply a transport only when the caller has not chosen one, and copy the
	// client rather than writing to it: base is normally http.DefaultClient,
	// whose transport the whole process shares.
	if base.Transport == nil {
		tuned := *base
		tuned.Transport = newUpstreamTransport()
		base = &tuned
	}

	return middleware.WithZentaoAuth(base, baseURL)
}

// newUpstreamTransport clones the standard transport and widens its idle
// connection pool. Cloning keeps the stdlib defaults — proxy from the
// environment, dial and TLS handshake timeouts — without touching the global
// http.DefaultTransport, which other packages also reach for.
func newUpstreamTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConnsPerHost = upstreamIdleConnsPerHost
	t.ResponseHeaderTimeout = upstreamResponseHeaderTimeout

	return t
}
