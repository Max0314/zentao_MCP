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

// toolFor adapts a decode-then-run pair into a tool handler.
//
// Every composite tool has the same shape: decode the arguments into a request,
// reject a bad request, run it. Writing that out per tool meant seven
// near-identical closures that a reviewer had to diff character by character,
// and it let argument coercion drift between them.
func toolFor[Q, R any](
	decode func(map[string]any) (Q, error),
	run func(context.Context, Q) (R, error),
) zentaoToolHandler {
	return func(ctx context.Context, in map[string]any) (any, error) {
		req, err := decode(in)
		if err != nil {
			return nil, err
		}

		out, err := run(ctx, req)
		if err != nil {
			return nil, err
		}

		return out, nil
	}
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

func (c *Controller) zentaoToolSet() []zentaoTool {
	svc := c.zentao

	tools := []zentaoTool{
		{
			name: "zentao_resolve_scope",
			description: "按关键字查找禅道的产品、项目、执行(迭代)和人员 ID。" +
				"其他工具需要 productID / projectID / executionID / account 时先用它解析。" +
				"Resolve ZenTao product, project, execution and user ids from a name fragment.",
			inputSchema: schemaResolveScope,
			handler:     toolFor(zentao.ResolveRequestFrom, svc.ResolveScope),
		},
		{
			name: "zentao_search_bugs",
			description: "跨产品/项目/执行查找 Bug，支持标题与复现步骤关键字、状态、严重程度、类型、" +
				"指派人、创建人、解决人和日期区间过滤，返回匹配列表和分布统计。" +
				"Search ZenTao bugs across scopes with keyword, owner and date filters.",
			inputSchema: schemaSearchBugs,
			handler:     toolFor(zentao.BugSearchRequestFrom, svc.SearchBugs),
		},
		{
			name: "zentao_analyze_bug",
			description: "对单个 Bug 做问题分析：重现步骤、完整处理时间线、逐条备注（解决过程通常写在备注里）、" +
				"修复时长、重新激活次数，并给出同产品相似 Bug 和根因分析检查清单。" +
				"Build a root cause analysis packet for one ZenTao bug.",
			inputSchema: schemaAnalyzeBug,
			handler:     toolFor(zentao.AnalyzeRequestFrom, svc.AnalyzeBug),
		},
		{
			name: "zentao_ai_score",
			description: "查询禅道任务/Bug/需求的 AI 评分：对象自身评分(0-100)、每条独立备注的评分、" +
				"已评分与未评分数量和平均分。可传 ids，也可传 scope + scopeID 批量查看。" +
				"Read ZenTao AI scores for objects and their scoreable comments.",
			inputSchema: schemaAIScore,
			handler:     toolFor(zentao.ScoreRequestFrom, svc.AIScores),
		},
		{
			name: "zentao_quality_report",
			description: "对一个产品/项目/执行做 Bug 质量统计：状态、严重程度、类型、解决方案分布，" +
				"模块与责任人 TOP 榜，修复时长、重新激活率和逐月趋势。" +
				"Aggregate ZenTao bug quality statistics for one scope.",
			inputSchema: schemaQualityReport,
			handler:     toolFor(zentao.ReportRequestFrom, svc.QualityReportFor),
		},
		{
			name: "zentao_user_worklog",
			description: "按人员和时间窗口汇总禅道工作记录：他创建/完成的任务、创建/解决的 Bug、创建/关闭的需求。" +
				"适合核对月度工作量和上传结果。" +
				"Summarise what one ZenTao account opened, finished or resolved in a time window.",
			inputSchema: schemaUserWorklog,
			handler:     toolFor(zentao.WorklogRequestFrom, svc.UserWorklog),
		},
	}

	// Only advertise historical search when an index is actually attached.
	// Registering it unconditionally meant tools/list promised the capability on
	// every server while each call returned "the bug index is not enabled".
	if svc.HasIndex() {
		tools = append(tools, zentaoTool{
			name: "zentao_find_similar_bugs",
			description: "按现象描述检索历史上处理过的同类 Bug：给出现网反馈原话（最好带设备型号、告警码或日志片段），" +
				"返回最相似的历史记录及其解决过程。适合“现网出了个问题，以前有没有遇到过、当时怎么修的”。" +
				"Find historical ZenTao bugs similar to a field-reported symptom.",
			inputSchema: schemaFindSimilarBugs,
			handler:     toolFor(zentao.HistoryRequestFrom, svc.FindSimilarBugs),
		})
	}

	return tools
}
