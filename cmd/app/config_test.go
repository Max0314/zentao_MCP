package main

import (
	"testing"
	"time"
)

func TestAuthConfigDefaultsAndOverrides(t *testing.T) {
	if _, err := (ServerConfig{}).authOptions(); err != nil {
		t.Fatal(err)
	}
	s := ServerConfig{Auth: &ZentaoAuthConfig{FailureLimit: 2, FailureWindow: "15m", TokenTTL: "12h", FailureTTL: "30m", MaxCacheEntries: 100}}
	o, err := s.authOptions()
	if err != nil || o.FailureWindow != 15*time.Minute || o.TokenTTL != 12*time.Hour || o.MaxCacheEntries != 100 {
		t.Fatalf("invalid parsed auth options: %+v %v", o, err)
	}
	for _, c := range []*ZentaoAuthConfig{{FailureLimit: -1}, {MaxCacheEntries: -1}, {FailureWindow: "bad"}, {TokenTTL: "0s"}, {FailureTTL: "-1h"}} {
		if _, err := (ServerConfig{Auth: c}).authOptions(); err == nil {
			t.Fatalf("invalid auth config accepted: %+v", c)
		}
	}
}
