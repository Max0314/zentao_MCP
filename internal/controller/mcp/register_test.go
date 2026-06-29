package mcp

import "testing"

func TestInternalAuthToolsAreHidden(t *testing.T) {
	tests := []struct {
		method string
		path   string
		want   bool
	}{
		{method: "POST", path: "/tokens", want: true},
		{method: "POST", path: "/users/login", want: true},
		{method: "GET", path: "/tokens", want: false},
		{method: "POST", path: "/executions/{executionID}/tasks", want: false},
	}

	for _, tt := range tests {
		if got := isInternalAuthTool(tt.method, tt.path); got != tt.want {
			t.Fatalf("isInternalAuthTool(%q, %q) = %v, want %v", tt.method, tt.path, got, tt.want)
		}
	}
}
