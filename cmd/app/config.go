// Package main is the entry point for the openapi-mcp-server.
package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"

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
	// PublishOutputSchema declares generated output schemas. Defaults to true.
	// On the bundled ZenTao document these schemas are ~90% of the tools/list
	// payload, so setting it to false cuts the per-session tool cost sharply
	// at the price of losing structured content.
	PublishOutputSchema *bool `yaml:"publish_output_schema"`
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
