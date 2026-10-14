package main

import (
	"math"
	"testing"
	"time"
)

func TestAccessConfigValidatesRatesAndTrustedProxies(t *testing.T) {
	s := ServerConfig{Auth: &ZentaoAuthConfig{RequestRate: 20, IPRequestRate: 5, LoginRate: 2, IPLoginRate: 0.5, TrustedProxies: []string{"192.168.160.1/32"}}}
	o, err := s.accessOptions()
	if err != nil || o.IPLoginRate != 0.5 || len(o.TrustedProxies) != 1 {
		t.Fatalf("parsed options=%+v err=%v", o, err)
	}
	for _, cfg := range []*ZentaoAuthConfig{{RequestRate: -1}, {LoginRate: math.NaN()}, {IPLoginRate: math.Inf(1)}, {IPRequestBurst: -1}, {TrustedProxies: []string{"all"}}} {
		if _, err := (ServerConfig{Auth: cfg}).accessOptions(); err == nil {
			t.Fatalf("invalid protection config accepted: %+v", cfg)
		}
	}
}

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
