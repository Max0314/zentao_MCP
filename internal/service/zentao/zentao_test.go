package zentao

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

const testBaseURL = "http://zentao.test/api.php/v1"

// fakeProxy serves canned JSON bodies keyed by request path.
type fakeProxy struct {
	bodies map[string]string
	calls  []string
}

func (f *fakeProxy) Do(_ context.Context, req *http.Request) (*http.Response, error) {
	f.calls = append(f.calls, req.URL.Path)

	body, ok := f.bodies[req.URL.Path]
	if !ok {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader(`{"error":"not found"}`)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	}

	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

func newTestService(bodies map[string]string) (*Service, *fakeProxy) {
	proxy := &fakeProxy{bodies: bodies}

	return New(proxy, testBaseURL), proxy
}

const productBugsBody = `{
  "status": "success",
  "total": 3,
  "bugs": [
    {"id": 101, "title": "登录页保存后状态未刷新", "status": "resolved", "severity": 2, "pri": 3,
     "type": "codeerror", "product": 654, "module": 12, "confirmed": 1, "activatedCount": 2,
     "openedBy": {"id": 7, "account": "chenpenglie", "realname": "陈鹏列"}, "openedDate": "2026-05-06 09:12:00",
     "assignedTo": {"account": "liuwei"}, "resolvedBy": {"account": "chenpenglie"},
     "resolvedDate": "2026-05-08 18:00:00", "resolution": "fixed", "steps": "<p>进入页面</p><p>点击保存</p>"},
    {"id": 102, "title": "报表导出乱码", "status": "active", "severity": 3, "pri": 2,
     "type": "codeerror", "product": 654, "module": 13,
     "openedBy": {"account": "liuwei"}, "openedDate": "2026-06-01 10:00:00",
     "assignedTo": {"account": "chenpenglie"}, "steps": "导出 CSV 后中文乱码"},
    {"id": 103, "title": "登录页验证码不刷新", "status": "closed", "severity": 2, "pri": 3,
     "type": "designdefect", "product": 654, "module": 12,
     "openedBy": {"account": "chenpenglie"}, "openedDate": "2026-05-20 08:00:00",
     "resolvedBy": {"account": "liuwei"}, "resolvedDate": "2026-05-21 08:00:00",
     "resolution": "fixed", "closedDate": "2026-05-22 08:00:00"}
  ]
}`

func TestSearchBugsFiltersByOwnerAndMonth(t *testing.T) {
	svc, _ := newTestService(map[string]string{
		"/api.php/v1/products/654/bugs": productBugsBody,
	})

	req := BugSearchRequestFrom(map[string]any{
		"productID": float64(654),
		"openedBy":  "chenpenglie",
		"month":     "2026-05",
	})

	res, err := svc.SearchBugs(context.Background(), req)
	if err != nil {
		t.Fatalf("SearchBugs: %v", err)
	}

	if res.Matched != 2 {
		t.Fatalf("matched = %d, want 2 (bugs 101 and 103)", res.Matched)
	}

	if res.Scanned != 3 {
		t.Fatalf("scanned = %d, want 3", res.Scanned)
	}

	if res.Bugs[0].ID != 103 {
		t.Fatalf("first bug id = %d, want 103 (newest first)", res.Bugs[0].ID)
	}

	if res.Bugs[1].FixDays == nil || *res.Bugs[1].FixDays != 2.37 {
		t.Fatalf("bug 101 fixDays = %v, want 2.37", res.Bugs[1].FixDays)
	}
}

func TestSearchBugsMatchesRealName(t *testing.T) {
	svc, _ := newTestService(map[string]string{
		"/api.php/v1/products/654/bugs": productBugsBody,
	})

	// Only bug 101 carries a realname on its openedBy reference.
	req := BugSearchRequestFrom(map[string]any{
		"productID": float64(654),
		"openedBy":  "陈鹏列",
	})

	res, err := svc.SearchBugs(context.Background(), req)
	if err != nil {
		t.Fatalf("SearchBugs: %v", err)
	}

	if res.Matched != 1 || res.Bugs[0].ID != 101 {
		t.Fatalf("matched = %d, want only bug 101", res.Matched)
	}

	if res.Bugs[0].OpenedBy != "chenpenglie" {
		t.Fatalf("openedBy = %q, want the account name", res.Bugs[0].OpenedBy)
	}
}

func TestSearchBugsKeywordSearchesSteps(t *testing.T) {
	svc, _ := newTestService(map[string]string{
		"/api.php/v1/products/654/bugs": productBugsBody,
	})

	req := BugSearchRequestFrom(map[string]any{
		"productID": float64(654),
		"keyword":   "点击保存",
	})

	res, err := svc.SearchBugs(context.Background(), req)
	if err != nil {
		t.Fatalf("SearchBugs: %v", err)
	}

	if res.Matched != 1 || res.Bugs[0].ID != 101 {
		t.Fatalf("matched = %d, want only bug 101", res.Matched)
	}
}

func TestSearchBugsRequiresIDForNonProductScope(t *testing.T) {
	svc, _ := newTestService(map[string]string{})

	req := BugSearchRequestFrom(map[string]any{"scope": "execution"})

	if _, err := svc.SearchBugs(context.Background(), req); err == nil {
		t.Fatal("expected an error when an execution search carries no id")
	}
}

const bugDetailBody = `{
  "id": 101, "title": "登录页保存后状态未刷新", "status": "resolved", "severity": 2,
  "product": 654, "module": 12, "resolution": "fixed",
  "openedBy": {"account": "chenpenglie"}, "openedDate": "2026-05-06 09:00:00",
  "resolvedBy": {"account": "chenpenglie"}, "resolvedDate": "2026-05-08 09:00:00",
  "closedDate": "2026-05-09 09:00:00", "activatedCount": 2,
  "steps": "<p>进入页面</p><p>点击保存，状态没有刷新</p>",
  "actions": [
    {"action": "opened", "actor": "chenpenglie", "date": "2026-05-06 09:00:00"},
    {"action": "commented", "actor": "chenpenglie", "date": "2026-05-06 10:00:00",
     "comment": "【bug解决步骤】定位到前端未重新拉取状态", "aiScore": "88"},
    {"action": "activated", "actor": "liuwei", "date": "2026-05-07 09:00:00"},
    {"action": "commented", "actor": "chenpenglie", "date": "2026-05-07 15:00:00",
     "comment": "【伪代码】refresh(state)"},
    {"action": "resolved", "actor": "chenpenglie", "date": "2026-05-08 09:00:00",
     "comment": "修复保存后刷新状态"}
  ]
}`

func TestAnalyzeBugBuildsTimelineAndMetrics(t *testing.T) {
	svc, _ := newTestService(map[string]string{
		"/api.php/v1/bugs/101":          bugDetailBody,
		"/api.php/v1/products/654/bugs": productBugsBody,
	})

	out, err := svc.AnalyzeBug(context.Background(), 101, true, 5, 0)
	if err != nil {
		t.Fatalf("AnalyzeBug: %v", err)
	}

	if out.Metrics.Actions != 5 {
		t.Fatalf("actions = %d, want 5", out.Metrics.Actions)
	}

	if out.Metrics.Comments != 2 {
		t.Fatalf("comments = %d, want 2", out.Metrics.Comments)
	}

	if out.Metrics.Reopens != 1 {
		t.Fatalf("reopens = %d, want 1", out.Metrics.Reopens)
	}

	if out.Metrics.ScoredComments != 1 {
		t.Fatalf("scoredComments = %d, want 1", out.Metrics.ScoredComments)
	}

	if out.Metrics.OpenToResolveDays == nil || *out.Metrics.OpenToResolveDays != 2 {
		t.Fatalf("openToResolveDays = %v, want 2", out.Metrics.OpenToResolveDays)
	}

	if out.Content.ResolutionNote != "修复保存后刷新状态" {
		t.Fatalf("resolutionNote = %q", out.Content.ResolutionNote)
	}

	if !strings.Contains(out.Content.Steps, "点击保存") || strings.Contains(out.Content.Steps, "<p>") {
		t.Fatalf("steps were not converted to plain text: %q", out.Content.Steps)
	}

	if len(out.Checklist) == 0 {
		t.Fatal("expected a root cause checklist")
	}

	foundRelated := false

	for _, rel := range out.Related {
		if rel.ID == 103 {
			foundRelated = true
		}

		if rel.ID == 101 {
			t.Fatal("related bugs must not contain the analysed bug itself")
		}
	}

	if !foundRelated {
		t.Fatalf("expected bug 103 among related bugs, got %+v", out.Related)
	}
}

func TestAIScoresReportsCoverage(t *testing.T) {
	svc, _ := newTestService(map[string]string{
		"/api.php/v1/bugs/101": bugDetailBody,
	})

	res, err := svc.AIScores(context.Background(), ScoreRequestFrom(map[string]any{
		"objectType": "bug",
		"ids":        []any{float64(101)},
	}))
	if err != nil {
		t.Fatalf("AIScores: %v", err)
	}

	if len(res.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(res.Items))
	}

	item := res.Items[0]
	if item.Comments != 2 || item.ScoredComments != 1 || item.MissingScores != 1 {
		t.Fatalf("comment scoring = %d/%d (missing %d), want 1/2 (missing 1)", item.ScoredComments, item.Comments, item.MissingScores)
	}

	if item.ScoringComplete {
		t.Fatal("scoringComplete must be false while one comment is unscored")
	}

	if res.Summary.ScoringCoverage != 50 {
		t.Fatalf("coverage = %v, want 50", res.Summary.ScoringCoverage)
	}

	if res.Summary.AverageAIScore == nil || *res.Summary.AverageAIScore != 88 {
		t.Fatalf("average = %v, want 88", res.Summary.AverageAIScore)
	}
}

func TestAIScoresReportsUpstreamErrorPerObject(t *testing.T) {
	svc, _ := newTestService(map[string]string{})

	res, err := svc.AIScores(context.Background(), ScoreRequestFrom(map[string]any{
		"objectType": "task",
		"ids":        "555",
	}))
	if err != nil {
		t.Fatalf("AIScores: %v", err)
	}

	if len(res.Items) != 1 || res.Items[0].Error == "" {
		t.Fatalf("expected a per-object error, got %+v", res.Items)
	}

	if res.Summary.ObjectsWithErrors != 1 {
		t.Fatalf("objectsWithErrors = %d, want 1", res.Summary.ObjectsWithErrors)
	}
}

func TestQualityReportAggregates(t *testing.T) {
	svc, _ := newTestService(map[string]string{
		"/api.php/v1/products/654/bugs": productBugsBody,
	})

	res, err := svc.QualityReportFor(context.Background(), ReportRequestFrom(map[string]any{
		"productID": float64(654),
	}))
	if err != nil {
		t.Fatalf("QualityReportFor: %v", err)
	}

	if res.Totals.Bugs != 3 || res.Totals.Active != 1 || res.Totals.Resolved != 1 || res.Totals.Closed != 1 {
		t.Fatalf("totals = %+v", res.Totals)
	}

	if res.Totals.Reopened != 1 {
		t.Fatalf("reopened = %d, want 1", res.Totals.Reopened)
	}

	if res.FixTime.Resolved != 2 {
		t.Fatalf("fix time samples = %d, want 2", res.FixTime.Resolved)
	}

	if len(res.Trend) == 0 {
		t.Fatal("expected a monthly trend")
	}

	if len(res.Highlights) == 0 {
		t.Fatal("expected highlights")
	}
}

const executionTasksBody = `{
  "status": "success", "total": 2,
  "tasks": [
    {"id": 900, "name": "对接禅道 MCP", "status": "done",
     "openedBy": {"account": "chenpenglie"}, "openedDate": "2026-05-02 09:00:00",
     "finishedBy": {"account": "chenpenglie"}, "finishedDate": "2026-05-19 18:00:00",
     "assignedTo": {"account": "chenpenglie"}, "estimate": "8", "consumed": "9", "aiScore": "90"},
    {"id": 901, "name": "别人的任务", "status": "done",
     "openedBy": {"account": "liuwei"}, "openedDate": "2026-05-03 09:00:00",
     "finishedBy": {"account": "liuwei"}, "finishedDate": "2026-05-10 18:00:00",
     "assignedTo": {"account": "liuwei"}}
  ]
}`

func TestUserWorklogCollectsRoles(t *testing.T) {
	svc, _ := newTestService(map[string]string{
		"/api.php/v1/executions/4058/tasks": executionTasksBody,
	})

	res, err := svc.UserWorklog(context.Background(), WorklogRequestFrom(map[string]any{
		"account":     "chenpenglie",
		"month":       "2026-05",
		"executionID": float64(4058),
		"kinds":       []any{"task"},
	}))
	if err != nil {
		t.Fatalf("UserWorklog: %v", err)
	}

	if len(res.Items) != 1 || res.Items[0].ID != 900 {
		t.Fatalf("items = %+v, want only task 900", res.Items)
	}

	roles := strings.Join(res.Items[0].Roles, ",")
	if !strings.Contains(roles, "opened") || !strings.Contains(roles, "finished") {
		t.Fatalf("roles = %q, want opened and finished", roles)
	}

	if res.Summary["task"] != 1 || res.Summary["total"] != 1 {
		t.Fatalf("summary = %+v", res.Summary)
	}
}

func TestUserWorklogRequiresScope(t *testing.T) {
	svc, _ := newTestService(map[string]string{})

	_, err := svc.UserWorklog(context.Background(), WorklogRequestFrom(map[string]any{"account": "chenpenglie"}))
	if err == nil {
		t.Fatal("expected an error when no scope id is given")
	}
}

const productsBody = `{
  "status": "success", "total": 2,
  "products": [
    {"id": 654, "name": "BI 数据中心", "code": "bi", "status": "normal"},
    {"id": 655, "name": "运维平台", "code": "ops", "status": "normal"}
  ]
}`

func TestResolveScopeMatchesByName(t *testing.T) {
	svc, _ := newTestService(map[string]string{
		"/api.php/v1/products": productsBody,
	})

	res, err := svc.ResolveScope(context.Background(), ResolveRequestFrom(map[string]any{
		"keyword": "BI",
		"kinds":   []any{"product"},
	}))
	if err != nil {
		t.Fatalf("ResolveScope: %v", err)
	}

	if len(res.Matches) != 1 || res.Matches[0].ID != 654 {
		t.Fatalf("matches = %+v, want product 654", res.Matches)
	}

	if res.Matches[0].UsableAs == "" {
		t.Fatal("expected usableAs guidance on a match")
	}
}

func TestResolveScopeReportsFailedKind(t *testing.T) {
	svc, _ := newTestService(map[string]string{})

	res, err := svc.ResolveScope(context.Background(), ResolveRequestFrom(map[string]any{
		"keyword": "任意",
		"kinds":   []any{"product"},
	}))
	if err != nil {
		t.Fatalf("ResolveScope: %v", err)
	}

	if len(res.Notes) == 0 {
		t.Fatal("expected a note describing the failed lookup")
	}
}

// ZenTao v1 returns object dates as UTC instants and action dates as bare
// Beijing wall clock. Both must resolve to the same ZenTao calendar day.
func TestParseDateHandlesZentaoMixedTimezones(t *testing.T) {
	if got := dateOnly("2026-08-31T08:18:28Z"); got != "2026-08-31" {
		t.Fatalf("zoned midday = %q, want 2026-08-31", got)
	}

	if got := dateOnly("2026-08-31 16:18:28"); got != "2026-08-31" {
		t.Fatalf("bare local = %q, want 2026-08-31", got)
	}

	if a, b := dateOnly("2026-08-31T08:18:28Z"), dateOnly("2026-08-31 16:18:28"); a != b {
		t.Fatalf("the same instant produced %q and %q", a, b)
	}

	// 2026-08-31T18:30:00Z is 2026-09-01 02:30 in Beijing and must count as September.
	if got := dateOnly("2026-08-31T18:30:00Z"); got != "2026-09-01" {
		t.Fatalf("late-UTC instant = %q, want 2026-09-01", got)
	}
}

const midnightBugsBody = `{
  "status": "success", "total": 2,
  "bugs": [
    {"id": 201, "title": "跨月边界 Bug", "status": "active", "product": 654,
     "openedBy": {"account": "chenpenglie"}, "openedDate": "2026-08-31T18:30:00Z"},
    {"id": 202, "title": "八月的 Bug", "status": "active", "product": 654,
     "openedBy": {"account": "chenpenglie"}, "openedDate": "2026-08-20T02:00:00Z"}
  ]
}`

func TestSearchBugsMonthFilterUsesZentaoLocalDate(t *testing.T) {
	svc, _ := newTestService(map[string]string{
		"/api.php/v1/products/654/bugs": midnightBugsBody,
	})

	res, err := svc.SearchBugs(context.Background(), BugSearchRequestFrom(map[string]any{
		"productID": float64(654),
		"month":     "2026-09",
	}))
	if err != nil {
		t.Fatalf("SearchBugs: %v", err)
	}

	if res.Matched != 1 || res.Bugs[0].ID != 201 {
		t.Fatalf("matched = %d (%+v), want only bug 201 which is 2026-09-01 in Beijing", res.Matched, res.Bugs)
	}

	if res.Bugs[0].OpenedDate != "2026-09-01" {
		t.Fatalf("openedDate = %q, want 2026-09-01", res.Bugs[0].OpenedDate)
	}
}

// The per-comment score arrives under "score"; the object level uses "aiScore".
func TestActionInsightsReadsCommentScoreField(t *testing.T) {
	rec := map[string]any{
		"id": 1,
		"actions": []any{
			map[string]any{"action": "commented", "actor": "陈鹏列", "date": "2026-08-31 16:17:09",
				"comment": "<p>【任务完成步骤】盘点范围</p>", "score": float64(4)},
			map[string]any{"action": "commented", "actor": "陈鹏列", "date": "2026-08-31 16:18:09",
				"comment": "第二条备注"},
		},
	}

	_, comments, metrics := actionInsights(rec)

	if metrics.Comments != 2 {
		t.Fatalf("comments = %d, want 2", metrics.Comments)
	}

	if metrics.ScoredComments != 1 {
		t.Fatalf("scoredComments = %d, want 1", metrics.ScoredComments)
	}

	if comments[0].AIScore != "4" {
		t.Fatalf("comment score = %q, want 4 read from the score field", comments[0].AIScore)
	}

	if comments[0].Comment != "【任务完成步骤】盘点范围" {
		t.Fatalf("comment text = %q, want the HTML stripped", comments[0].Comment)
	}
}
