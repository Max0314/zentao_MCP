package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/merzzzl/openapi-mcp-server/internal/models"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	toon "github.com/toon-format/toon-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// RegisterAllTools registers all OpenAPI operations as MCP tools.
func (c *Controller) RegisterAllTools(ctx context.Context, server *mcpsdk.Server) error {
	ctx, span := c.tracer.Start(ctx, "RegisterAllTools")
	defer span.End()

	c.logger.InfoContext(ctx, "registering tools")

	tools, err := c.schema.Tools(ctx)
	if err != nil {
		return fmt.Errorf("get tools: %w", err)
	}

	registered, skipped := 0, 0

	for i := range tools {
		td := &tools[i]

		if isInternalAuthTool(td.Method, td.Path) {
			continue
		}

		if c.skipDeprecated && td.Deprecated {
			skipped++

			c.logger.InfoContext(ctx, "skipping deprecated operation",
				"tool", td.OperationID,
				"method", td.Method,
				"path", td.Path,
			)

			continue
		}

		if !c.operationChecker(td.Method, td.Path) {
			continue
		}

		c.registerTool(ctx, server, td)

		registered++
	}

	c.logger.InfoContext(ctx, "tools registered", "count", registered, "skipped_deprecated", skipped)

	c.RegisterZentaoTools(ctx, server)

	return nil
}

func isInternalAuthTool(method, path string) bool {
	return method == "POST" && (path == "/tokens" || path == "/users/login")
}

func (c *Controller) registerTool(ctx context.Context, server *mcpsdk.Server, td *models.ToolDefinition) {
	c.logger.InfoContext(ctx, "adding tool",
		"tool", td.OperationID,
		"method", td.Method,
		"path", td.Path,
	)

	t := &mcpsdk.Tool{
		Name:        td.OperationID,
		Description: td.Description,
		InputSchema: td.InputSchema,
	}

	if td.OutputSchema != nil && c.publishOutputSchema {
		t.OutputSchema = compatibleOutputSchema(td.OutputSchema)
	}

	mcpsdk.AddTool(server, t, c.toolHandler(td))
}

func (c *Controller) toolHandler(td *models.ToolDefinition) func(context.Context, *mcpsdk.CallToolRequest, map[string]any) (*mcpsdk.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcpsdk.CallToolRequest, in map[string]any) (*mcpsdk.CallToolResult, any, error) {
		start := time.Now()

		ctx, span := c.tracer.Start(ctx, td.OperationID)
		defer span.End()

		attrs := metric.WithAttributes(attribute.String("tool", td.OperationID))

		c.metrics.ToolCalls.Add(ctx, 1, attrs)

		defer func() {
			c.metrics.ToolDuration.Record(ctx, time.Since(start).Seconds(), attrs)
		}()

		c.logger.InfoContext(ctx, "new tool call",
			"tool", td.OperationID,
			"method", td.Method,
			"path", td.Path,
		)

		status, body, err := c.tool.Execute(ctx, td, in)
		if err != nil {
			c.logger.ErrorContext(ctx, "error executing tool",
				"tool", td.OperationID,
				"error", err,
			)

			return nil, nil, err
		}

		text := string(body)

		var (
			data    any
			decoded bool
		)

		if err := json.Unmarshal(body, &data); err == nil {
			decoded = true

			if td.WrapOutput && status >= 200 && status < 300 {
				data = map[string]any{"result": data}
			}

			if c.enableTOON {
				if encoded, err := toon.Marshal(data); err == nil {
					text = string(encoded)
				}
			} else {
				if minified, err := json.Marshal(data); err == nil {
					text = string(minified)
				}
			}
		}

		// Report upstream failures as failures.
		//
		// ZenTao serves an unsupported route as HTTP 200 carrying a PHP fatal
		// error page, and a broken one as 4xx/5xx. Neither was marked as an
		// error, so a model saw "HTTP 500" plus an error page as a successful
		// call and could conclude that a task had been created. A non-2xx
		// status, or a 2xx whose body is neither JSON nor empty, is an error.
		failed := status < http.StatusOK || status >= http.StatusMultipleChoices
		if !failed && !decoded && len(bytes.TrimSpace(body)) > 0 {
			failed = true
		}

		if failed {
			c.logger.WarnContext(ctx, "upstream call reported as tool error",
				"tool", td.OperationID,
				"status", status,
				"json_body", decoded,
			)
		}

		res := &mcpsdk.CallToolResult{
			IsError: failed,
			Content: []mcpsdk.Content{
				&mcpsdk.TextContent{Text: fmt.Sprintf("HTTP %d", status)},
				&mcpsdk.TextContent{Text: text},
			},
		}

		// Structured output is only valid when the tool declared an output
		// schema. Skipping the schema also skips the normalization walk, which
		// dominates latency on large list responses.
		var structuredOutput any
		if c.publishOutputSchema && len(td.OutputSchema) > 0 && status >= 200 && status < 300 && data != nil {
			structuredOutput = normalizeStructuredOutput(td.OutputSchema, data)
		}

		return res, structuredOutput, nil
	}
}
