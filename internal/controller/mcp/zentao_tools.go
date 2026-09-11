package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/merzzzl/openapi-mcp-server/internal/service/zentao"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	toon "github.com/toon-format/toon-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// zentaoToolHandler runs one composite ZenTao tool and returns a JSON payload.
type zentaoToolHandler func(context.Context, map[string]any) (any, error)

// zentaoTool is a hand written tool that combines several ZenTao endpoints.
type zentaoTool struct {
	name        string
	description string
	inputSchema string
	handler     zentaoToolHandler
}

// RegisterZentaoTools adds the composite ZenTao tools to the MCP server.
// They are registered only when a ZenTao service is configured for the server.
func (c *Controller) RegisterZentaoTools(ctx context.Context, server *mcpsdk.Server) {
	if c.zentao == nil {
		return
	}

	ctx, span := c.tracer.Start(ctx, "RegisterZentaoTools")
	defer span.End()

	for _, t := range c.zentaoToolSet() {
		c.logger.InfoContext(ctx, "adding zentao extension tool", "tool", t.name)

		tool := &mcpsdk.Tool{
			Name:        t.name,
			Description: t.description,
			InputSchema: json.RawMessage(t.inputSchema),
		}

		mcpsdk.AddTool(server, tool, c.zentaoHandler(t))
	}

	c.logger.InfoContext(ctx, "zentao extension tools registered")
}

func (c *Controller) zentaoHandler(t zentaoTool) func(context.Context, *mcpsdk.CallToolRequest, map[string]any) (*mcpsdk.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcpsdk.CallToolRequest, in map[string]any) (*mcpsdk.CallToolResult, any, error) {
		start := time.Now()

		ctx, span := c.tracer.Start(ctx, t.name)
		defer span.End()

		attrs := metric.WithAttributes(attribute.String("tool", t.name))

		c.metrics.ToolCalls.Add(ctx, 1, attrs)

		defer func() {
			c.metrics.ToolDuration.Record(ctx, time.Since(start).Seconds(), attrs)
		}()

		c.logger.InfoContext(ctx, "new zentao extension call", "tool", t.name)

		payload, err := t.handler(ctx, in)
		if err != nil {
			c.logger.ErrorContext(ctx, "error executing zentao extension tool",
				"tool", t.name,
				"error", err,
			)

			return nil, nil, err
		}

		text, err := c.encodePayload(payload)
		if err != nil {
			return nil, nil, err
		}

		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: text}},
		}, nil, nil
	}
}

// encodePayload renders a tool payload as JSON, or as TOON when enabled.
func (c *Controller) encodePayload(payload any) (string, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal result: %w", err)
	}

	if !c.enableTOON {
		return string(raw), nil
	}

	var generic any

	if err := json.Unmarshal(raw, &generic); err != nil {
		return string(raw), nil
	}

	encoded, err := toon.Marshal(generic)
	if err != nil {
		return string(raw), nil
	}

	return string(encoded), nil
}

//nolint:funlen // the tool set is a declarative table.
func (c *Controller) zentaoToolSet() []zentaoTool {
	svc := c.zentao

	return []zentaoTool{
		{
			name: "zentao_resolve_scope",
			description: "按关键字查找禅道的产品、项目、执行(迭代)和人员 ID。" +
				"其他工具需要 productID / projectID / executionID / account 时先用它解析。" +
				"Resolve ZenTao product, project, execution and user ids from a name fragment.",
			inputSchema: schemaResolveScope,
			handler: func(ctx context.Context, in map[string]any) (any, error) {
				out, err := svc.ResolveScope(ctx, zentao.ResolveRequestFrom(in))
				if err != nil {
					return nil, err
				}

				return out, nil
			},
		},
		{
			name: "zentao_search_bugs",
			description: "跨产品/项目/执行查找 Bug，支持标题关键字、状态、严重程度、类型、指派人、创建人、解决人和日期区间过滤，" +
				"返回匹配列表和按状态/严重程度/类型/责任人聚合的分布。" +
				"Search ZenTao bugs across scopes with keyword, owner and date filters.",
			inputSchema: schemaSearchBugs,
			handler: func(ctx context.Context, in map[string]any) (any, error) {
				out, err := svc.SearchBugs(ctx, zentao.BugSearchRequestFrom(in))
				if err != nil {
					return nil, err
				}

				return out, nil
			},
		},
		{
			name: "zentao_analyze_bug",
			description: "对单个 Bug 做问题分析：整理重现步骤、处理时间线、备注、修复时长、重新激活次数，" +
				"并给出同产品相似 Bug 和根因分析检查清单。" +
				"Build a root cause analysis packet for one ZenTao bug.",
			inputSchema: schemaAnalyzeBug,
			handler: func(ctx context.Context, in map[string]any) (any, error) {
				out, err := svc.AnalyzeBug(ctx,
					argIntValue(in, "bugID"),
					argBoolValue(in, "includeRelated", true),
					argIntValue(in, "relatedLimit"),
					argIntValue(in, "commentLimit"),
				)
				if err != nil {
					return nil, err
				}

				return out, nil
			},
		},
		{
			name: "zentao_ai_score",
			description: "查询禅道任务/Bug/需求的 AI 评分：对象自身评分、每条备注的评分、已评分与未评分数量和平均分。" +
				"可传 ids，也可传 scope + scopeID 批量查看最近的对象。" +
				"Read ZenTao AI scores for objects and their scoreable comments.",
			inputSchema: schemaAIScore,
			handler: func(ctx context.Context, in map[string]any) (any, error) {
				out, err := svc.AIScores(ctx, zentao.ScoreRequestFrom(in))
				if err != nil {
					return nil, err
				}

				return out, nil
			},
		},
		{
			name: "zentao_quality_report",
			description: "对一个产品/项目/执行做 Bug 质量统计：状态、严重程度、类型、解决方案分布，" +
				"模块与责任人 TOP 榜，修复时长、重新激活率和逐月趋势。" +
				"Aggregate ZenTao bug quality statistics for one scope.",
			inputSchema: schemaQualityReport,
			handler: func(ctx context.Context, in map[string]any) (any, error) {
				out, err := svc.QualityReportFor(ctx, zentao.ReportRequestFrom(in))
				if err != nil {
					return nil, err
				}

				return out, nil
			},
		},
		{
			name: "zentao_user_worklog",
			description: "按人员和时间窗口汇总禅道工作记录：他创建/完成的任务、创建/解决的 Bug、创建/关闭的需求。" +
				"适合核对月度工作量和上传结果。" +
				"Summarise what one ZenTao account opened, finished or resolved in a time window.",
			inputSchema: schemaUserWorklog,
			handler: func(ctx context.Context, in map[string]any) (any, error) {
				out, err := svc.UserWorklog(ctx, zentao.WorklogRequestFrom(in))
				if err != nil {
					return nil, err
				}

				return out, nil
			},
		},
	}
}

func argIntValue(in map[string]any, name string) int {
	v, ok := in[name]
	if !ok || v == nil {
		return 0
	}

	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return int(i)
		}
	case string:
		var parsed int

		if _, err := fmt.Sscanf(t, "%d", &parsed); err == nil {
			return parsed
		}
	}

	return 0
}

func argBoolValue(in map[string]any, name string, def bool) bool {
	v, ok := in[name]
	if !ok || v == nil {
		return def
	}

	if b, ok := v.(bool); ok {
		return b
	}

	return def
}
