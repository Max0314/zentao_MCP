// Package main is the entry point for the openapi-mcp-server.
package main

import (
	"errors"
	"fmt"
	"math"
	"net/netip"
	"os"
	"regexp"
	"time"

	"github.com/merzzzl/openapi-mcp-server/internal/middleware"
	"github.com/merzzzl/openapi-mcp-server/internal/models"
	"gopkg.in/yaml.v3"
)

var (
	errNoServers         = errors.New("no servers configured")
	errServerNameEmpty   = errors.New("name is required")
	errServerSchemaEmpty = errors.New("schema_url is required")
	errServerBaseEmpty   = errors.New("base_url is required")
)

type MatchRule struct {
	Methods []string `yaml:"methods"`
	Regex   string   `yaml:"regex"`
}

type ServerConfig struct {
	Name      string      `yaml:"name"`
	SchemaURL string      `yaml:"schema_url"`
	BaseURL   string      `yaml:"base_url"`
	Allow     []MatchRule `yaml:"allow"`
	Block     []MatchRule `yaml:"block"`
	// SkipDeprecated hides operations the OpenAPI document marks deprecated.
	// The bundled ZenTao document flags routes that 12.3 v1 does not serve.
	SkipDeprecated bool `yaml:"skip_deprecated"`
	// ZentaoExtensions registers the composite ZenTao tools (bug search,
	// AI score, problem analysis) for this upstream.
	ZentaoExtensions bool `yaml:"zentao_extensions"`
	// BugIndex enables the historical symptom lookup tool for this upstream.
	BugIndex *BugIndexConfig   `yaml:"bug_index"`
	Auth     *ZentaoAuthConfig `yaml:"auth"`
	// PublishOutputSchema declares generated output schemas. Defaults to true.
	// On the bundled ZenTao document these schemas are ~90% of the tools/list
	// payload, so setting it to false cuts the per-session tool cost sharply
	// at the price of losing structured content.
	PublishOutputSchema *bool `yaml:"publish_output_schema"`
}

// ZentaoAuthConfig tunes login protection to the upstream account-lock policy.
type ZentaoAuthConfig struct {
	FailureLimit    int      `yaml:"failure_limit"`
	FailureWindow   string   `yaml:"failure_window"`
	TokenTTL        string   `yaml:"token_ttl"`
	FailureTTL      string   `yaml:"failure_ttl"`
	MaxCacheEntries int      `yaml:"max_cache_entries"`
	RequestRate     float64  `yaml:"request_rate"`
	RequestBurst    int      `yaml:"request_burst"`
	IPRequestRate   float64  `yaml:"ip_request_rate"`
	IPRequestBurst  int      `yaml:"ip_request_burst"`
	LoginRate       float64  `yaml:"login_rate"`
	LoginBurst      int      `yaml:"login_burst"`
	IPLoginRate     float64  `yaml:"ip_login_rate"`
	IPLoginBurst    int      `yaml:"ip_login_burst"`
	TrustedProxies  []string `yaml:"trusted_proxies"`
}

func (s ServerConfig) accessOptions() (middleware.ZentaoAccessOptions, error) {
	var o middleware.ZentaoAccessOptions
	if s.Auth == nil {
		return o, nil
	}
	a := s.Auth
	for _, value := range []float64{a.RequestRate, a.IPRequestRate, a.LoginRate, a.IPLoginRate} {
		if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return o, errors.New("auth request/login rates must be positive or omitted")
		}
	}
	for _, value := range []int{a.RequestBurst, a.IPRequestBurst, a.LoginBurst, a.IPLoginBurst} {
		if value < 0 {
			return o, errors.New("auth request/login bursts must be positive or omitted")
		}
	}
	o.RequestRate, o.RequestBurst = a.RequestRate, a.RequestBurst
	o.IPRequestRate, o.IPRequestBurst = a.IPRequestRate, a.IPRequestBurst
	o.LoginRate, o.LoginBurst = a.LoginRate, a.LoginBurst
	o.IPLoginRate, o.IPLoginBurst = a.IPLoginRate, a.IPLoginBurst
	for _, cidr := range a.TrustedProxies {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			return o, fmt.Errorf("invalid auth.trusted_proxies CIDR: %q", cidr)
		}
		o.TrustedProxies = append(o.TrustedProxies, prefix)
	}
	return o, nil
}

func (s ServerConfig) authOptions() (middleware.ZentaoAuthOptions, error) {
	var o middleware.ZentaoAuthOptions
	if s.Auth == nil {
		return o, nil
	}
	if s.Auth.FailureLimit < 0 || s.Auth.MaxCacheEntries < 0 {
		return o, errors.New("auth failure_limit and max_cache_entries must be positive or omitted")
	}
	o.FailureLimit, o.MaxCacheEntries = s.Auth.FailureLimit, s.Auth.MaxCacheEntries
	for _, field := range []struct {
		name, value string
		target      *time.Duration
	}{
		{"failure_window", s.Auth.FailureWindow, &o.FailureWindow},
		{"token_ttl", s.Auth.TokenTTL, &o.TokenTTL},
		{"failure_ttl", s.Auth.FailureTTL, &o.FailureTTL},
	} {
		if field.value == "" {
			continue
		}
		d, err := time.ParseDuration(field.value)
		if err != nil || d <= 0 {
			return o, fmt.Errorf("auth.%s must be a positive duration", field.name)
		}
		*field.target = d
	}
	return o, nil
}

// BugIndexConfig configures the background bug corpus used by
// zentao_find_similar_bugs. ZenTao v1 has no text search, so the corpus is
// pulled once and indexed in memory.
//
// The corpus is built under one service account, which means it holds
// everything that account can read. Results are therefore never returned
// straight from the index: each candidate is re-read as the calling user and
// dropped when that read fails.
type BugIndexConfig struct {
	Enabled bool `yaml:"enabled"`
	// Account is the service account that builds the corpus.
	Account string `yaml:"account"`
	// Password sets the service account password directly. Prefer
	// PasswordEnv so the secret stays out of the config file.
	Password string `yaml:"password"`
	// PasswordEnv names an environment variable holding the password.
	PasswordEnv string `yaml:"password_env"`
	// Refresh is a Go duration such as "6h". Minimum 15m, default 6h.
	Refresh string `yaml:"refresh"`
	// BodyChars caps how much of each bug's steps text is indexed.
	BodyChars int `yaml:"body_chars"`
	// MaxProducts and MaxBugsPerProduct bound one build.
	MaxProducts       int `yaml:"max_products"`
	MaxBugsPerProduct int `yaml:"max_bugs_per_product"`
}

// resolvePassword returns the configured password, preferring the environment.
func (b *BugIndexConfig) resolvePassword() string {
	if b.PasswordEnv != "" {
		if v := os.Getenv(b.PasswordEnv); v != "" {
			return v
		}
	}

	return b.Password
}

// refreshDuration parses Refresh, falling back to zero so the service applies
// its own default.
func (b *BugIndexConfig) refreshDuration() (time.Duration, error) {
	if b.Refresh == "" {
		return 0, nil
	}

	d, err := time.ParseDuration(b.Refresh)
	if err != nil {
		return 0, fmt.Errorf("parse bug_index.refresh %q: %w", b.Refresh, err)
	}

	return d, nil
}

// publishOutputSchema reports the effective setting, defaulting to true.
func (s ServerConfig) publishOutputSchema() bool {
	if s.PublishOutputSchema == nil {
		return true
	}

	return *s.PublishOutputSchema
}

const serviceName = "openapi-mcp-server"

type TelemetryConfig struct {
	Endpoint string `yaml:"endpoint"`
	Insecure bool   `yaml:"insecure"`
}

type Config struct {
	Port       string `yaml:"port"`
	EnableTOON bool   `yaml:"enable_toon"`
	// Timezone is the ZenTao server's wall-clock zone, used to turn the UTC
	// timestamps the v1 API returns into calendar dates. Defaults to
	// Asia/Shanghai. It applies to the whole process.
	Timezone  string          `yaml:"timezone"`
	Telemetry TelemetryConfig `yaml:"telemetry"`
	Servers   []ServerConfig  `yaml:"servers"`
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	if cfg.Port == "" {
		cfg.Port = ":8080"
	}

	if len(cfg.Servers) == 0 {
		return nil, errNoServers
	}

	for i, s := range cfg.Servers {
		if s.Name == "" {
			return nil, fmt.Errorf("server[%d]: %w", i, errServerNameEmpty)
		}

		if s.SchemaURL == "" {
			return nil, fmt.Errorf("server[%d] %q: %w", i, s.Name, errServerSchemaEmpty)
		}

		if s.BaseURL == "" {
			return nil, fmt.Errorf("server[%d] %q: %w", i, s.Name, errServerBaseEmpty)
		}
	}

	return &cfg, nil
}

func compileMatchRules(rules []MatchRule) ([]models.CompiledRule, error) {
	compiled := make([]models.CompiledRule, 0, len(rules))

	for _, r := range rules {
		rx, err := regexp.Compile(r.Regex)
		if err != nil {
			return nil, fmt.Errorf("compile regex %q: %w", r.Regex, err)
		}

		compiled = append(compiled, models.CompiledRule{
			Methods: r.Methods,
			Regex:   rx,
		})
	}

	return compiled, nil
}
