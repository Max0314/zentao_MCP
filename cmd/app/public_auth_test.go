package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/merzzzl/openapi-mcp-server/internal/middleware"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Exercise the actual stateless MCP transport, including a tool which never
// calls ZenTao. Invalid credentials must fail before any protocol result.
func TestPublicMCPAuthenticatesInitializeDiscoveryAndLocalTool(t *testing.T) {
	var logins, calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tokens" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		logins.Add(1)
		var c map[string]string
		_ = json.NewDecoder(r.Body).Decode(&c)
		if c["password"] != "good" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"token":"test-session"}`))
	}))
	defer upstream.Close()
	_, manager := middleware.NewZentaoAuthClient(upstream.Client(), upstream.URL)
	server := mcp.NewServer(&mcp.Implementation{Name: "auth-test"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "local_no_hits", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
		calls.Add(1)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `{"returned":0}`}}}, nil, nil
	})
	h := middleware.NewZentaoAuthenticator(manager, middleware.ZentaoAccessOptions{}).Wrap(newStreamableMCPHandler(server))
	for _, method := range []string{"initialize", "tools/list", "tools/call"} {
		params := map[string]any{}
		if method == "initialize" {
			params = map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "1"}}
		}
		if method == "tools/call" {
			params = map[string]any{"name": "local_no_hits", "arguments": map[string]any{}}
		}
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		for _, password := range []string{"bad", "good"} {
			r := httptest.NewRequest(http.MethodPost, "/zentao/mcp", strings.NewReader(string(body)))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Accept", "application/json, text/event-stream")
			r.Header.Set("zentao-account", "alice")
			r.Header.Set("zentao-password", password)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			want := http.StatusOK
			if password == "bad" {
				want = http.StatusUnauthorized
			}
			if w.Code != want {
				t.Fatalf("%s (%s): status=%d body=%s", method, password, w.Code, w.Body.String())
			}
			if password == "good" && strings.Contains(w.Body.String(), `"error"`) {
				t.Fatalf("MCP error: %s", w.Body.String())
			}
		}
	}
	if calls.Load() != 1 || logins.Load() != 3 {
		t.Fatalf("unexpected tool/login count: calls=%d logins=%d", calls.Load(), logins.Load())
	}
}
