package zentao

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

const (
	defaultReportScan = 1000
	maxReportScan     = 5000
)

// FixTimeStats summarises how long resolved bugs stayed open.
type FixTimeStats struct {
	Resolved  int     `json:"resolvedWithDates"`
	AvgDays   float64 `json:"averageDays"`
	MedianDay float64 `json:"medianDays"`
	P90Days   float64 `json:"p90Days"`
	MaxDays   float64 `json:"maxDays"`
}

// MonthPoint is one month of the opened and resolved trend.
type MonthPoint struct {
	Month    string `json:"month"`
	Opened   int    `json:"opened"`
	Resolved int    `json:"resolved"`
}

// QualityTotals holds the headline counters of a quality report.
type QualityTotals struct {
	Bugs           int     `json:"bugs"`
	Active         int     `json:"active"`
	Resolved       int     `json:"resolved"`
	Closed         int     `json:"closed"`
	Unconfirmed    int     `json:"unconfirmed"`
	Reopened       int     `json:"reopenedBugs"`
	ResolvedRate   float64 `json:"resolvedRatePercent"`
	ClosedRate     float64 `json:"closedRatePercent"`
	ReopenRate     float64 `json:"reopenRatePercent"`
	NotFixedRate   float64 `json:"notFixedRatePercent"`
	SeriousBugRate float64 `json:"severity1And2Percent"`
}

// QualityReport is the payload returned by the quality report tool.
type QualityReport struct {
	Scope        ScopeStat         `json:"scope"`
	Window       map[string]string `json:"window,omitempty"`
	Totals       QualityTotals     `json:"totals"`
	ByStatus     []NameCount       `json:"byStatus,omitempty"`
	BySeverity   []NameCount       `json:"bySeverity,omitempty"`
	ByType       []NameCount       `json:"byType,omitempty"`
	ByResolution []NameCount       `json:"byResolution,omitempty"`
	TopModules   []NameCount       `json:"topModules,omitempty"`
	TopAssignees []NameCount       `json:"topAssignees,omitempty"`
	TopOpeners   []NameCount       `json:"topOpeners,omitempty"`
	TopResolvers []NameCount       `json:"topResolvers,omitempty"`
	FixTime      FixTimeStats      `json:"fixTime"`
	Trend        []MonthPoint      `json:"monthlyTrend,omitempty"`
	Oldest       []BugSummary      `json:"oldestActiveBugs,omitempty"`
	Highlights   []string          `json:"highlights,omitempty"`
	Notes        []string          `json:"notes,omitempty"`
	Query        map[string]any    `json:"query"`
}

// ReportRequest describes an aggregate bug quality report.
type ReportRequest struct {
	Scope        string
	ScopeID      int
	Month        string
	OpenedAfter  string
	OpenedBefore string
	AssignedTo   string
	MaxScan      int
	TopN         int
}

// ReportRequestFrom decodes tool arguments into a quality report request.
func ReportRequestFrom(in map[string]any) (ReportRequest, error) {
	scope, scopeID, err := scopeSelector(in, "product")
	if err != nil {
		return ReportRequest{}, err
	}

	req := ReportRequest{
		Scope:      scope,
		ScopeID:    scopeID,
		Month:      argString(in, "month"),
		AssignedTo: argString(in, "assignedTo"),
		MaxScan:    clamp(argInt(in, "maxScan", defaultReportScan), 1, maxReportScan),
		TopN:       clamp(argInt(in, "topN", 10), 1, 50),
	}

	if req.OpenedAfter, err = normalizeDay("openedAfter", argString(in, "openedAfter")); err != nil {
		return ReportRequest{}, err
	}

	if req.OpenedBefore, err = normalizeDay("openedBefore", argString(in, "openedBefore")); err != nil {
		return ReportRequest{}, err
	}

	from, to, err := monthWindow(req.Month)
	if err != nil {
		return ReportRequest{}, err
	}

	if from != "" && req.OpenedAfter == "" && req.OpenedBefore == "" {
		req.OpenedAfter, req.OpenedBefore = from, to
	}

	return req, nil
}

// QualityReportFor scans one scope and aggregates its bug quality picture.
func (s *Service) QualityReportFor(ctx context.Context, req ReportRequest) (*QualityReport, error) {
	ctx, span := s.tracer.Start(ctx, "QualityReport")
	defer span.End()

	apiPath, err := collectionPath(req.Scope, req.ScopeID, "bugs")
	if err != nil {
		return nil, err
	}

	query := url.Values{}
	query.Set("status", "all")
	query.Set("orderBy", "id_desc")

	list, err := s.listAll(ctx, apiPath, query, req.MaxScan)
	if err != nil {
		return nil, err
	}

	out := &QualityReport{
		Scope: ScopeStat{
			Scope:     req.Scope,
			ID:        req.ScopeID,
			Scanned:   len(list.Records),
			Total:     list.Total,
			Truncated: list.Truncated,
		},
		Query: map[string]any{
			"scope":   req.Scope,
			"scopeID": req.ScopeID,
			"maxScan": req.MaxScan,
		},
	}

	if req.OpenedAfter != "" || req.OpenedBefore != "" {
		out.Window = map[string]string{"openedAfter": req.OpenedAfter, "openedBefore": req.OpenedBefore}
	}

	if req.AssignedTo != "" {
		out.Query["assignedTo"] = req.AssignedTo
	}

	if list.Truncated {
		out.Notes = append(out.Notes, fmt.Sprintf("only the %d most recent bugs were scanned out of %d; raise maxScan or narrow the window for a complete report", len(list.Records), list.Total))
	}

	counters := map[string]map[string]int{
		"status":     {},
		"severity":   {},
		"type":       {},
		"resolution": {},
		"module":     {},
		"assignedTo": {},
		"openedBy":   {},
		"resolvedBy": {},
	}

	trend := map[string]*MonthPoint{}

	var (
		fixDays      []float64
		active       []BugSummary
		seriousCount int
	)

	for _, rec := range list.Records {
		if !withinRange(field(rec, "openedDate"), req.OpenedAfter, req.OpenedBefore) {
			continue
		}

		if !matchUser(field(rec, "assignedTo"), req.AssignedTo) {
			continue
		}

		bug := bugSummary(rec)
		out.Totals.Bugs++

		switch strings.ToLower(bug.Status) {
		case "active":
			out.Totals.Active++

			active = append(active, bug)
		case "resolved":
			out.Totals.Resolved++
		case "closed":
			out.Totals.Closed++
		}

		if bug.Confirmed == 0 {
			out.Totals.Unconfirmed++
		}

		// ZenTao counts reactivations, not activations: activatedCount is 0 on
		// a bug that was never reopened, so > 1 missed every single-reopen bug
		// and reported regression quality as clean.
		if bug.ActivatedNum > 0 {
			out.Totals.Reopened++
		}

		if bug.Severity == 1 || bug.Severity == 2 {
			seriousCount++
		}

		bump(counters["status"], bug.Status)
		bump(counters["severity"], strconv.Itoa(bug.Severity))
		bump(counters["type"], bug.Type)
		bump(counters["resolution"], bug.Resolution)
		bump(counters["assignedTo"], bug.AssignedTo)
		bump(counters["openedBy"], bug.OpenedBy)
		bump(counters["resolvedBy"], bug.ResolvedBy)

		if module := fieldString(rec, "moduleName"); module != "" {
			bump(counters["module"], module)
		} else if bug.Module > 0 {
			bump(counters["module"], "module:"+strconv.Itoa(bug.Module))
		}

		if bug.FixDays != nil {
			fixDays = append(fixDays, *bug.FixDays)
		}

		addTrend(trend, bug.OpenedDate, true)
		addTrend(trend, bug.ResolvedDate, false)
	}

	out.Totals.SeriousBugRate = ratio(seriousCount, out.Totals.Bugs)
	out.Totals.ResolvedRate = ratio(out.Totals.Resolved+out.Totals.Closed, out.Totals.Bugs)
	out.Totals.ClosedRate = ratio(out.Totals.Closed, out.Totals.Bugs)
	out.Totals.ReopenRate = ratio(out.Totals.Reopened, out.Totals.Bugs)
	out.Totals.NotFixedRate = ratio(counters["resolution"]["notrepro"]+counters["resolution"]["bydesign"]+counters["resolution"]["duplicate"]+counters["resolution"]["willnotfix"], out.Totals.Bugs)

	out.ByStatus = topN(counters["status"], 0)
	out.BySeverity = topN(counters["severity"], 0)
	out.ByType = topN(counters["type"], 0)
	out.ByResolution = topN(counters["resolution"], 0)
	out.TopModules = topN(counters["module"], req.TopN)
	out.TopAssignees = topN(counters["assignedTo"], req.TopN)
	out.TopOpeners = topN(counters["openedBy"], req.TopN)
	out.TopResolvers = topN(counters["resolvedBy"], req.TopN)
	out.FixTime = fixTimeStats(fixDays)
	out.Trend = trendPoints(trend)
	out.Oldest = oldestActive(active, 5)
	out.Highlights = highlights(out, seriousCount)

	return out, nil
}

func addTrend(trend map[string]*MonthPoint, day string, opened bool) {
	if len(day) < 7 {
		return
	}

	month := day[:7]

	point, ok := trend[month]
	if !ok {
		point = &MonthPoint{Month: month}
		trend[month] = point
	}

	if opened {
		point.Opened++

		return
	}

	point.Resolved++
}

func trendPoints(trend map[string]*MonthPoint) []MonthPoint {
	out := make([]MonthPoint, 0, len(trend))
	for _, p := range trend {
		out = append(out, *p)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Month < out[j].Month })

	return out
}

func fixTimeStats(days []float64) FixTimeStats {
	stats := FixTimeStats{Resolved: len(days)}
	if len(days) == 0 {
		return stats
	}

	sort.Float64s(days)

	if avg := average(days); avg != nil {
		stats.AvgDays = *avg
	}

	stats.MedianDay = percentile(days, 0.5)
	stats.P90Days = percentile(days, 0.9)
	stats.MaxDays = math.Round(days[len(days)-1]*100) / 100

	return stats
}

func oldestActive(bugs []BugSummary, limit int) []BugSummary {
	sort.Slice(bugs, func(i, j int) bool {
		if bugs[i].OpenedDate != bugs[j].OpenedDate {
			return bugs[i].OpenedDate < bugs[j].OpenedDate
		}

		return bugs[i].ID < bugs[j].ID
	})

	if len(bugs) > limit {
		bugs = bugs[:limit]
	}

	return bugs
}

func highlights(report *QualityReport, seriousCount int) []string {
	if report.Totals.Bugs == 0 {
		return []string{"筛选条件下没有匹配的 Bug，请放宽时间窗口或确认 scope 是否正确。"}
	}

	out := []string{
		fmt.Sprintf("共 %d 个 Bug：激活 %d、已解决 %d、已关闭 %d，解决率 %.1f%%。",
			report.Totals.Bugs, report.Totals.Active, report.Totals.Resolved, report.Totals.Closed, report.Totals.ResolvedRate),
	}

	if seriousCount > 0 {
		out = append(out, fmt.Sprintf("严重程度 1/2 的 Bug 有 %d 个，占 %.1f%%，优先分析这部分。", seriousCount, report.Totals.SeriousBugRate))
	}

	if report.FixTime.Resolved > 0 {
		out = append(out, fmt.Sprintf("修复时长：平均 %.2f 天，中位数 %.2f 天，P90 %.2f 天，最长 %.2f 天。",
			report.FixTime.AvgDays, report.FixTime.MedianDay, report.FixTime.P90Days, report.FixTime.MaxDays))
	}

	if report.Totals.Reopened > 0 {
		out = append(out, fmt.Sprintf("有 %d 个 Bug 被重新激活过（占 %.1f%%），需要检查修复质量和回归验证。", report.Totals.Reopened, report.Totals.ReopenRate))
	}

	if len(report.TopModules) > 0 {
		out = append(out, fmt.Sprintf("问题最集中的模块是 %s（%d 个）。", report.TopModules[0].Name, report.TopModules[0].Count))
	}

	if report.Totals.Unconfirmed > 0 {
		out = append(out, fmt.Sprintf("有 %d 个 Bug 未确认，可能缺少责任人跟进。", report.Totals.Unconfirmed))
	}

	return out
}
