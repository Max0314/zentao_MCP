package zentao

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"
)

const (
	defaultScoreBatch = 20
	maxScoreBatch     = 50
	scoreCommentClip  = 240
	// scoreConcurrency bounds the parallel detail reads of one score lookup.
	scoreConcurrency = 6
)

// CommentScore is one scored ZenTao comment.
type CommentScore struct {
	Date    string `json:"date,omitempty"`
	Actor   string `json:"actor,omitempty"`
	Score   string `json:"aiScore,omitempty"`
	Comment string `json:"comment,omitempty"`
}

// ScoreItem is the AI score report for one ZenTao object.
type ScoreItem struct {
	Type            string         `json:"type"`
	ID              int            `json:"id"`
	Title           string         `json:"title,omitempty"`
	Status          string         `json:"status,omitempty"`
	ObjectAIScore   string         `json:"objectAiScore,omitempty"`
	Comments        int            `json:"comments"`
	ScoredComments  int            `json:"scoredComments"`
	MissingScores   int            `json:"unscoredComments"`
	AverageAIScore  *float64       `json:"averageAiScore,omitempty"`
	MinAIScore      *float64       `json:"minAiScore,omitempty"`
	MaxAIScore      *float64       `json:"maxAiScore,omitempty"`
	CommentScores   []CommentScore `json:"commentScores,omitempty"`
	Error           string         `json:"error,omitempty"`
	ScoringComplete bool           `json:"scoringComplete"`
}

// ScoreResult is the payload returned by the AI score tool.
type ScoreResult struct {
	Query   map[string]any `json:"query"`
	Items   []ScoreItem    `json:"items"`
	Summary ScoreSummary   `json:"summary"`
	Notes   []string       `json:"notes,omitempty"`
}

// ScoreSummary aggregates every inspected object.
type ScoreSummary struct {
	Objects           int      `json:"objects"`
	ObjectsWithScore  int      `json:"objectsWithScoredComments"`
	Comments          int      `json:"comments"`
	ScoredComments    int      `json:"scoredComments"`
	UnscoredComments  int      `json:"unscoredComments"`
	AverageAIScore    *float64 `json:"averageAiScore,omitempty"`
	ScoringCoverage   float64  `json:"scoringCoveragePercent"`
	ObjectsWithErrors int      `json:"objectsWithErrors,omitempty"`
}

// ScoreRequest describes an AI score lookup.
type ScoreRequest struct {
	ObjectType    string
	IDs           []int
	Scope         string
	ScopeID       int
	Limit         int
	IncludeDetail bool
}

// ScoreRequestFrom decodes tool arguments into an AI score request.
func ScoreRequestFrom(in map[string]any) (ScoreRequest, error) {
	req := ScoreRequest{
		ObjectType:    strings.ToLower(argString(in, "objectType", "type")),
		IDs:           argInts(in, "ids", "id"),
		Limit:         clamp(argInt(in, "limit", defaultScoreBatch), 1, maxScoreBatch),
		IncludeDetail: argBool(in, "includeComments", true),
	}

	if req.ObjectType == "" {
		req.ObjectType = "task"
	}

	if _, _, err := resolveObject(req.ObjectType); err != nil {
		return ScoreRequest{}, err
	}

	// A scope is only needed when no explicit ids were given.
	if len(req.IDs) > 0 {
		return req, nil
	}

	scope, scopeID, err := scopeSelector(in, "")
	if err != nil {
		return ScoreRequest{}, err
	}

	req.Scope, req.ScopeID = scope, scopeID

	return req, nil
}

// AIScores reads ZenTao AI scores for objects and their scoreable comments.
func (s *Service) AIScores(ctx context.Context, req ScoreRequest) (*ScoreResult, error) {
	ctx, span := s.tracer.Start(ctx, "AIScores")
	defer span.End()

	out := &ScoreResult{
		Query: map[string]any{
			"objectType": req.ObjectType,
			"limit":      req.Limit,
		},
		Items: []ScoreItem{},
	}

	ids := req.IDs

	if len(ids) == 0 {
		resolved, note, err := s.scoreScopeIDs(ctx, req)
		if err != nil {
			return nil, err
		}

		ids = resolved

		if note != "" {
			out.Notes = append(out.Notes, note)
		}

		out.Query["scope"] = req.Scope
		out.Query["scopeID"] = req.ScopeID
	}

	if len(ids) == 0 {
		return nil, fmt.Errorf("%w: pass ids, or a scope plus scopeID", errInvalidArgument)
	}

	if len(ids) > req.Limit {
		out.Notes = append(out.Notes, fmt.Sprintf("only the first %d of %d objects were inspected", req.Limit, len(ids)))
		ids = ids[:req.Limit]
	}

	// Each object is an independent detail read of ~0.3s; serially this took
	// about 16s for 50 objects, past many MCP clients' tool timeout.
	items := make([]ScoreItem, len(ids))
	valueSets := make([][]float64, len(ids))

	forEachBounded(len(ids), scoreConcurrency, func(i int) {
		items[i], valueSets[i] = s.scoreOne(ctx, req.ObjectType, ids[i], req.IncludeDetail)
	})

	var all []float64

	for i := range items {
		item, values := items[i], valueSets[i]

		out.Summary.Objects++
		out.Summary.Comments += item.Comments
		out.Summary.ScoredComments += item.ScoredComments
		out.Summary.UnscoredComments += item.MissingScores

		if item.Error != "" {
			out.Summary.ObjectsWithErrors++
		}

		if item.ScoredComments > 0 {
			out.Summary.ObjectsWithScore++
		}

		all = append(all, values...)

		out.Items = append(out.Items, item)
	}

	out.Summary.AverageAIScore = average(all)
	out.Summary.ScoringCoverage = ratio(out.Summary.ScoredComments, out.Summary.Comments)

	if out.Summary.Comments == 0 {
		out.Notes = append(out.Notes, "no commented actions were found; ZenTao only scores independent comments, not the description or the finish/resolve remark")
	}

	if out.Summary.ScoredComments > 0 {
		out.Notes = append(out.Notes, "objectAiScore and the per-comment scores use different scales: the object score is 0-100, the per-comment score comes from the action 'score' field and is a small integer; do not average the two together")
	}

	if out.Summary.UnscoredComments > 0 {
		out.Notes = append(out.Notes, "unscored comments are normal right after an upload; ZenTao scores asynchronously, so re-read after a short wait")
	}

	return out, nil
}

// scoreScopeIDs lists candidate object ids inside a scope.
func (s *Service) scoreScopeIDs(ctx context.Context, req ScoreRequest) ([]int, string, error) {
	if req.Scope == "" || req.ScopeID <= 0 {
		return nil, "", fmt.Errorf("%w: pass ids, or a scope plus scopeID", errInvalidArgument)
	}

	collection, _, err := resolveObject(req.ObjectType)
	if err != nil {
		return nil, "", err
	}

	apiPath, err := collectionPath(req.Scope, req.ScopeID, collection)
	if err != nil {
		return nil, "", err
	}

	query := url.Values{}
	query.Set("status", "all")
	query.Set("orderBy", "id_desc")

	list, err := s.listAll(ctx, apiPath, query, req.Limit)
	if err != nil {
		return nil, "", err
	}

	ids := make([]int, 0, len(list.Records))

	for _, rec := range list.Records {
		if id := fieldInt(rec, "id"); id > 0 {
			ids = append(ids, id)
		}
	}

	note := fmt.Sprintf("ids were taken from the %d most recent %s of %s %d", len(ids), collection, req.Scope, req.ScopeID)

	return ids, note, nil
}

// scoreOne reads one object and reports its AI scoring state.
func (s *Service) scoreOne(ctx context.Context, objectType string, id int, includeDetail bool) (ScoreItem, []float64) {
	item := ScoreItem{Type: strings.ToLower(objectType), ID: id}

	apiPath, wrapKey, err := objectPath(objectType, id)
	if err != nil {
		item.Error = err.Error()

		return item, nil
	}

	rec, err := s.detail(ctx, apiPath, wrapKey)
	if err != nil {
		item.Error = err.Error()

		return item, nil
	}

	item.Title = fieldString(rec, "title", "name")
	item.Status = fieldString(rec, "status")
	item.ObjectAIScore = fieldString(rec, "aiScore")

	_, comments, metrics := actionInsights(rec)

	item.Comments = metrics.Comments
	item.ScoredComments = metrics.ScoredComments
	item.MissingScores = metrics.Comments - metrics.ScoredComments
	item.AverageAIScore = metrics.AverageAIScore
	item.ScoringComplete = metrics.Comments > 0 && item.MissingScores == 0

	values := make([]float64, 0, len(comments))

	for _, c := range comments {
		if v, ok := toFloat(c.AIScore); ok {
			values = append(values, v)
		}

		if includeDetail {
			item.CommentScores = append(item.CommentScores, CommentScore{
				Date:    c.Date,
				Actor:   c.Actor,
				Score:   c.AIScore,
				Comment: truncate(c.Comment, scoreCommentClip),
			})
		}
	}

	if len(values) > 0 {
		sort.Float64s(values)

		low := math.Round(values[0]*100) / 100
		high := math.Round(values[len(values)-1]*100) / 100
		item.MinAIScore = &low
		item.MaxAIScore = &high
	}

	return item, values
}
