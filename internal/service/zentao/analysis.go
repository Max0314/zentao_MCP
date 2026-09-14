package zentao

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/merzzzl/openapi-mcp-server/internal/service/bugindex"
)

const (
	defaultRelatedLimit = 5
	maxRelatedLimit     = 20
	relatedScan         = 400
	minSimilarity       = 0.12
	commentPreview      = 600
)

// TimelineEntry is one ZenTao action rendered for analysis.
type TimelineEntry struct {
	Date    string `json:"date,omitempty"`
	Action  string `json:"action"`
	Actor   string `json:"actor,omitempty"`
	Comment string `json:"comment,omitempty"`
	AIScore string `json:"aiScore,omitempty"`
}

// BugContent holds the readable text extracted from a bug.
type BugContent struct {
	Steps          string `json:"steps,omitempty"`
	ResolutionNote string `json:"resolutionNote,omitempty"`
	Keywords       string `json:"keywords,omitempty"`
	Mailto         string `json:"mailto,omitempty"`
	Deadline       string `json:"deadline,omitempty"`
	LinkedStory    int    `json:"story,omitempty"`
	LinkedTask     int    `json:"task,omitempty"`
	ResolvedBuild  string `json:"resolvedBuild,omitempty"`
}

// BugMetrics holds the derived lifecycle numbers of one bug.
type BugMetrics struct {
	OpenToResolveDays  *float64 `json:"openToResolveDays,omitempty"`
	ResolveToCloseDays *float64 `json:"resolveToCloseDays,omitempty"`
	OpenToCloseDays    *float64 `json:"openToCloseDays,omitempty"`
	Actions            int      `json:"actions"`
	Comments           int      `json:"comments"`
	Reopens            int      `json:"reopens"`
	Assignments        int      `json:"assignments"`
	ScoredComments     int      `json:"scoredComments"`
	AverageAIScore     *float64 `json:"averageCommentAiScore,omitempty"`
}

// RelatedBug is a similar bug proposed for regression and pattern analysis.
type RelatedBug struct {
	ID         int     `json:"id"`
	Title      string  `json:"title"`
	Status     string  `json:"status,omitempty"`
	Resolution string  `json:"resolution,omitempty"`
	Module     int     `json:"module,omitempty"`
	OpenedDate string  `json:"openedDate,omitempty"`
	Similarity float64 `json:"similarity"`
	SameModule bool    `json:"sameModule,omitempty"`
}

// BugAnalysis is the payload returned by the bug analysis tool.
type BugAnalysis struct {
	Bug       BugSummary      `json:"bug"`
	Content   BugContent      `json:"content"`
	Metrics   BugMetrics      `json:"metrics"`
	Timeline  []TimelineEntry `json:"timeline,omitempty"`
	Comments  []TimelineEntry `json:"comments,omitempty"`
	Related   []RelatedBug    `json:"related,omitempty"`
	Checklist []string        `json:"analysisChecklist"`
	Notes     []string        `json:"notes,omitempty"`
}

// analysisChecklist steers the model towards a complete root cause write-up.
var analysisChecklist = []string{
	"复现与现象：确认重现步骤、触发条件、影响的版本或环境。",
	"定位：指出触发问题的模块、接口或代码路径，并说明依据（日志、报错、提交记录）。",
	"根因分类：需求遗漏 / 逻辑错误 / 边界与空值 / 并发与时序 / 配置与环境 / 依赖与兼容 / 数据问题。",
	"影响范围：受影响的功能、用户、数据量，以及是否需要数据修复。",
	"修复方案：实际改动点与取舍，附关键伪代码或提交。",
	"验证方式：回归用例、验证环境、验证结论。",
	"预防措施：同类问题的检查项、监控或用例补充。",
	"结论只写证据支持的内容；缺少证据的部分标注“待确认”。",
}

// AnalyzeRequest describes a single bug analysis.
type AnalyzeRequest struct {
	BugID          int
	IncludeRelated bool
	RelatedLimit   int
	CommentLimit   int
}

// AnalyzeRequestFrom decodes tool arguments into an analysis request.
func AnalyzeRequestFrom(in map[string]any) (AnalyzeRequest, error) {
	req := AnalyzeRequest{
		BugID:          argInt(in, "bugID", 0),
		IncludeRelated: argBool(in, "includeRelated", true),
		RelatedLimit:   argInt(in, "relatedLimit", 0),
		CommentLimit:   argInt(in, "commentLimit", 0),
	}

	if req.BugID <= 0 {
		return AnalyzeRequest{}, fmt.Errorf("%w: bugID is required", errInvalidArgument)
	}

	return req, nil
}

// AnalyzeBug builds a structured problem analysis packet for one bug.
func (s *Service) AnalyzeBug(ctx context.Context, req AnalyzeRequest) (*BugAnalysis, error) {
	ctx, span := s.tracer.Start(ctx, "AnalyzeBug")
	defer span.End()

	bugID, includeRelated := req.BugID, req.IncludeRelated
	relatedLimit, commentLimit := req.RelatedLimit, req.CommentLimit

	rec, err := s.detail(ctx, fmt.Sprintf("/bugs/%d", bugID), "bug")
	if err != nil {
		return nil, err
	}

	out := &BugAnalysis{
		Bug:       bugSummary(rec),
		Checklist: analysisChecklist,
	}

	out.Bug.Steps = ""
	out.Content = BugContent{
		Steps:         plainText(field(rec, "steps")),
		Keywords:      fieldString(rec, "keywords"),
		Mailto:        fieldString(rec, "mailto"),
		Deadline:      dateOnly(field(rec, "deadline")),
		LinkedStory:   fieldInt(rec, "story"),
		LinkedTask:    fieldInt(rec, "task"),
		ResolvedBuild: fieldString(rec, "resolvedBuild"),
	}

	timeline, comments, metrics := actionInsights(rec)
	out.Timeline = timeline
	out.Comments = comments
	out.Metrics = metrics

	if days, ok := daysBetween(field(rec, "openedDate"), field(rec, "resolvedDate")); ok {
		out.Metrics.OpenToResolveDays = &days
	}

	if days, ok := daysBetween(field(rec, "resolvedDate"), field(rec, "closedDate")); ok {
		out.Metrics.ResolveToCloseDays = &days
	}

	if days, ok := daysBetween(field(rec, "openedDate"), field(rec, "closedDate")); ok {
		out.Metrics.OpenToCloseDays = &days
	}

	out.Content.ResolutionNote = resolutionNote(timeline)

	if commentLimit > 0 && len(out.Comments) > commentLimit {
		out.Notes = append(out.Notes, fmt.Sprintf("only the last %d of %d comments are included", commentLimit, len(out.Comments)))
		out.Comments = out.Comments[len(out.Comments)-commentLimit:]
	}

	if len(timeline) == 0 {
		out.Notes = append(out.Notes, "this bug detail carried no actions list, so the timeline, comment count and AI scores are unavailable")
	}

	if includeRelated {
		related, notes := s.relatedBugs(ctx, out.Bug, relatedLimit)
		out.Related = related
		out.Notes = append(out.Notes, notes...)
	}

	return out, nil
}

// resolutionNote picks the text that explains how a bug was fixed.
//
// Teams record the diagnosis and the fix as separate dated 备注 (commented
// actions) and routinely leave the resolve remark itself empty, so reading only
// the resolved action returned nothing for bugs that are fully documented.
// Prefer an explicit resolve comment, then the last comment recorded up to the
// resolution, then the last comment on the bug.
func resolutionNote(timeline []TimelineEntry) string {
	var lastComment string

	for _, entry := range timeline {
		switch strings.ToLower(entry.Action) {
		case "commented":
			if entry.Comment != "" {
				lastComment = entry.Comment
			}
		case "resolved":
			if entry.Comment != "" {
				return entry.Comment
			}

			if lastComment != "" {
				return lastComment
			}
		}
	}

	return lastComment
}

// actionInsights turns the ZenTao actions list into a timeline plus metrics.
func actionInsights(rec map[string]any) ([]TimelineEntry, []TimelineEntry, BugMetrics) {
	var (
		timeline []TimelineEntry
		comments []TimelineEntry
		metrics  BugMetrics
		scores   []float64
	)

	raw, ok := field(rec, "actions").([]any)
	if !ok {
		if m, isMap := field(rec, "actions").(map[string]any); isMap {
			raw = make([]any, 0, len(m))

			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}

			sort.Strings(keys)

			for _, k := range keys {
				raw = append(raw, m[k])
			}
		}
	}

	for _, item := range raw {
		action, isMap := item.(map[string]any)
		if !isMap {
			continue
		}

		name := strings.ToLower(fieldString(action, "action"))
		comment := plainText(field(action, "comment"))

		entry := TimelineEntry{
			Date:    dateTime(field(action, "date")),
			Action:  name,
			Actor:   fieldString(action, "actor"),
			Comment: truncate(comment, commentPreview),
			AIScore: commentScore(action),
		}

		metrics.Actions++

		switch name {
		case "activated":
			metrics.Reopens++
		case "assigned":
			metrics.Assignments++
		}

		if name == "commented" {
			metrics.Comments++
			comments = append(comments, entry)

			if entry.AIScore != "" {
				metrics.ScoredComments++

				if v, ok := toFloat(entry.AIScore); ok {
					scores = append(scores, v)
				}
			}
		}

		timeline = append(timeline, entry)
	}

	metrics.AverageAIScore = average(scores)

	return timeline, comments, metrics
}

// relatedFromIndex ranks related bugs against the whole indexed product and
// re-reads each candidate as the calling user. It reports ok=false when the
// index yields nothing usable, so the caller can fall back to a live scan.
func (s *Service) relatedFromIndex(ctx context.Context, bug BugSummary, limit int) ([]RelatedBug, []string, bool) {
	hits, _ := s.index.Search(bugindex.Query{
		Text:    bug.Title,
		Product: bug.Product,
		Limit:   limit * overFetch,
	})

	candidates := make([]bugindex.Hit, 0, len(hits))

	for _, h := range hits {
		if h.ID != bug.ID {
			candidates = append(candidates, h)
		}
	}

	if len(candidates) == 0 {
		return nil, nil, false
	}

	records := make([]map[string]any, len(candidates))
	errs := make([]error, len(candidates))

	forEachBounded(len(candidates), verifyConcurrency, func(i int) {
		records[i], errs[i] = s.detail(ctx, fmt.Sprintf("/bugs/%d", candidates[i].ID), "bug")
	})

	want := titleTokens(bug.Title)
	found := make([]RelatedBug, 0, limit)

	for i := range candidates {
		if errs[i] != nil || records[i] == nil || fieldInt(records[i], "id") != candidates[i].ID {
			continue
		}

		if len(found) >= limit {
			break
		}

		rec := records[i]
		title := fieldString(rec, "title")
		module := fieldInt(rec, "module")

		found = append(found, RelatedBug{
			ID:         candidates[i].ID,
			Title:      title,
			Status:     fieldString(rec, "status"),
			Resolution: fieldString(rec, "resolution"),
			Module:     module,
			OpenedDate: dateOnly(field(rec, "openedDate")),
			Similarity: similarity(want, titleTokens(title)),
			SameModule: bug.Module > 0 && module == bug.Module,
		})
	}

	if len(found) == 0 {
		return nil, nil, false
	}

	return found, []string{"相似 Bug 来自全量索引（整个产品，不只最近几百条），每条都已用当前账号回查。"}, true
}

// relatedBugs finds similar bugs in the same product for pattern analysis.
func (s *Service) relatedBugs(ctx context.Context, bug BugSummary, limit int) ([]RelatedBug, []string) {
	if limit <= 0 {
		limit = defaultRelatedLimit
	}

	limit = clamp(limit, 1, maxRelatedLimit)

	if bug.Product <= 0 {
		return nil, []string{"related bugs were skipped because this bug carries no product id"}
	}

	// Prefer the index: it covers the whole product rather than the newest few
	// hundred bugs, and needs no list download. Candidates are still re-read as
	// the calling user, so nothing indexed reaches the caller unverified.
	if s.index != nil && s.index.Ready() {
		if related, notes, ok := s.relatedFromIndex(ctx, bug, limit); ok {
			return related, notes
		}
	}

	query := url.Values{}
	query.Set("status", "all")
	query.Set("orderBy", "id_desc")

	list, err := s.listAll(ctx, fmt.Sprintf("/products/%d/bugs", bug.Product), query, relatedScan)
	if err != nil {
		s.logger.WarnContext(ctx, "related bug scan failed", "product", bug.Product, "error", err)

		return nil, []string{fmt.Sprintf("related bugs are unavailable: %v", err)}
	}

	want := titleTokens(bug.Title)
	found := make([]RelatedBug, 0, limit)

	for _, rec := range list.Records {
		id := fieldInt(rec, "id")
		if id == bug.ID || id <= 0 {
			continue
		}

		title := fieldString(rec, "title")
		module := fieldInt(rec, "module")
		score := similarity(want, titleTokens(title))
		sameModule := bug.Module > 0 && module == bug.Module

		if score < minSimilarity && !sameModule {
			continue
		}

		found = append(found, RelatedBug{
			ID:         id,
			Title:      title,
			Status:     fieldString(rec, "status"),
			Resolution: fieldString(rec, "resolution"),
			Module:     module,
			OpenedDate: dateOnly(field(rec, "openedDate")),
			Similarity: score,
			SameModule: sameModule,
		})
	}

	sort.Slice(found, func(i, j int) bool {
		if found[i].Similarity != found[j].Similarity {
			return found[i].Similarity > found[j].Similarity
		}

		return found[i].ID > found[j].ID
	})

	notes := []string{}

	if list.Truncated {
		notes = append(notes, fmt.Sprintf("related bugs were matched against the %d most recent bugs of product %d only", len(list.Records), bug.Product))
	}

	if len(found) > limit {
		found = found[:limit]
	}

	return found, notes
}
