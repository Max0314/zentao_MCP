package zentao

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/merzzzl/openapi-mcp-server/internal/middleware"
	"github.com/merzzzl/openapi-mcp-server/internal/service/bugindex"
)

const (
	defaultIndexRefresh   = 6 * time.Hour
	minIndexRefresh       = 15 * time.Minute
	defaultIndexBodyChars = 400
	defaultIndexProducts  = 400
	defaultIndexPerScope  = 4000
	indexPageSize         = 100

	defaultSimilarLimit = 8
	maxSimilarLimit     = 25
	// verifyConcurrency bounds the parallel per-caller permission checks.
	verifyConcurrency = 6
	// overFetch decides how many extra ranked candidates to keep so that
	// records the caller cannot read can be dropped without starving results.
	overFetch          = 3
	maxVerifyCandidate = 60
	stepsClip          = 420
	fixNoteClip        = 420
)

// IndexOptions configures the background corpus builder.
type IndexOptions struct {
	// Account and Password belong to the service account whose visibility
	// defines the corpus. Nothing indexed under it is ever shown to a caller
	// without re-reading the record as that caller.
	Account  string
	Password string
	Refresh  time.Duration
	// BodyChars caps how much of each bug's steps text is indexed.
	BodyChars int
	// MaxProducts and MaxBugsPerProduct bound a build.
	MaxProducts       int
	MaxBugsPerProduct int
}

func (o IndexOptions) normalized() IndexOptions {
	if o.Refresh <= 0 {
		o.Refresh = defaultIndexRefresh
	}

	if o.Refresh < minIndexRefresh {
		o.Refresh = minIndexRefresh
	}

	if o.BodyChars <= 0 {
		o.BodyChars = defaultIndexBodyChars
	}

	if o.MaxProducts <= 0 {
		o.MaxProducts = defaultIndexProducts
	}

	if o.MaxBugsPerProduct <= 0 {
		o.MaxBugsPerProduct = defaultIndexPerScope
	}

	return o
}

// AttachIndex enables historical symptom lookup on this service.
func (s *Service) AttachIndex(ix *bugindex.Index, opts IndexOptions) {
	s.index = ix
	s.indexOpts = opts.normalized()
}

// RunIndexBuilder builds the corpus once and then refreshes it on a ticker.
// It blocks until ctx is done, so run it in its own goroutine.
func (s *Service) RunIndexBuilder(ctx context.Context) {
	if s.index == nil {
		return
	}

	if s.indexOpts.Account == "" || s.indexOpts.Password == "" {
		s.logger.ErrorContext(ctx, "bug index disabled: no service account configured")

		return
	}

	s.refreshIndex(ctx)

	ticker := time.NewTicker(s.indexOpts.Refresh)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.refreshIndex(ctx)
		}
	}
}

func (s *Service) refreshIndex(ctx context.Context) {
	start := time.Now()

	// The builder runs as the service account, not as any caller.
	buildCtx := middleware.WithZentaoCredentials(ctx, s.indexOpts.Account, s.indexOpts.Password)

	docs, err := s.buildCorpus(buildCtx)
	if err != nil {
		s.logger.ErrorContext(ctx, "bug index build failed", "error", err, "elapsed", time.Since(start).String())

		return
	}

	s.index.Replace(docs)

	s.logger.InfoContext(ctx, "bug index built",
		"documents", len(docs),
		"elapsed", time.Since(start).String(),
	)
}

// buildCorpus pulls every readable bug into indexable documents.
func (s *Service) buildCorpus(ctx context.Context) ([]bugindex.Doc, error) {
	products, err := s.listAll(ctx, "/products", url.Values{}, s.indexOpts.MaxProducts)
	if err != nil {
		return nil, fmt.Errorf("list products: %w", err)
	}

	query := url.Values{}
	query.Set("status", "all")
	query.Set("orderBy", "id_desc")

	// The upstream repeats records across page boundaries, so collect by id.
	byID := make(map[int]bugindex.Doc, 4096)

	for _, product := range products.Records {
		pid := fieldInt(product, "id")
		if pid <= 0 {
			continue
		}

		pname := fieldString(product, "name")

		bugs, err := s.listAll(ctx, fmt.Sprintf("/products/%d/bugs", pid), query, s.indexOpts.MaxBugsPerProduct)
		if err != nil {
			s.logger.WarnContext(ctx, "bug index: product scan failed", "product", pid, "error", err)

			continue
		}

		for _, rec := range bugs.Records {
			id := fieldInt(rec, "id")
			if id <= 0 {
				continue
			}

			title := fieldString(rec, "title")

			byID[id] = bugindex.Doc{
				ID:           id,
				Product:      pid,
				ProductName:  pname,
				Module:       fieldInt(rec, "module"),
				Title:        title,
				Body:         bugindex.Clip(bugindex.PlainText(fieldString(rec, "steps")), s.indexOpts.BodyChars),
				Tags:         bugindex.ExtractTags(title),
				Severity:     fieldInt(rec, "severity"),
				Type:         fieldString(rec, "type"),
				Status:       fieldString(rec, "status"),
				Resolution:   fieldString(rec, "resolution"),
				OpenedDate:   dateOnly(field(rec, "openedDate")),
				ResolvedDate: dateOnly(field(rec, "resolvedDate")),
			}
		}
	}

	docs := make([]bugindex.Doc, 0, len(byID))
	for _, d := range byID {
		docs = append(docs, d)
	}

	return docs, nil
}

// SimilarBug is one historical record judged similar to a reported symptom.
// Every field comes from a read performed as the calling user.
type SimilarBug struct {
	ID           int      `json:"id"`
	Score        float64  `json:"score"`
	Product      int      `json:"product,omitempty"`
	ProductName  string   `json:"productName,omitempty"`
	Title        string   `json:"title"`
	Type         string   `json:"type,omitempty"`
	Status       string   `json:"status,omitempty"`
	Resolution   string   `json:"resolution,omitempty"`
	Severity     int      `json:"severity,omitempty"`
	OpenedDate   string   `json:"openedDate,omitempty"`
	ResolvedDate string   `json:"resolvedDate,omitempty"`
	ResolvedBy   string   `json:"resolvedBy,omitempty"`
	MatchedKeys  []string `json:"matchedOn,omitempty"`
	Steps        string   `json:"stepsPreview,omitempty"`
	FixNote      string   `json:"fixNote,omitempty"`
	Comments     int      `json:"comments,omitempty"`
	Reopens      int      `json:"reopens,omitempty"`
}

// IndexStatus describes the corpus a lookup ran against.
type IndexStatus struct {
	Documents int     `json:"documents"`
	BuiltAt   string  `json:"builtAt,omitempty"`
	AgeHours  float64 `json:"ageHours,omitempty"`
}

// HistoryResult is the payload of the historical symptom lookup.
type HistoryResult struct {
	Symptom    string       `json:"symptom"`
	Index      IndexStatus  `json:"index"`
	Terms      int          `json:"queryTerms"`
	Candidates int          `json:"candidatesRanked"`
	Returned   int          `json:"returned"`
	Hidden     int          `json:"hiddenByPermission,omitempty"`
	Bugs       []SimilarBug `json:"bugs"`
	Notes      []string     `json:"notes,omitempty"`
}

// HistoryRequest describes a symptom lookup.
type HistoryRequest struct {
	Symptom      string
	Product      int
	Type         string
	Resolution   string
	OpenedAfter  string
	OpenedBefore string
	FixedOnly    bool
	Limit        int
}

// HistoryRequestFrom decodes tool arguments into a symptom lookup.
func HistoryRequestFrom(in map[string]any) HistoryRequest {
	return HistoryRequest{
		Symptom:      argString(in, "symptom", "text", "keyword"),
		Product:      argInt(in, "productID", argInt(in, "product", 0)),
		Type:         strings.ToLower(argString(in, "type")),
		Resolution:   strings.ToLower(argString(in, "resolution")),
		OpenedAfter:  normalizeDay(argString(in, "openedAfter")),
		OpenedBefore: normalizeDay(argString(in, "openedBefore")),
		FixedOnly:    argBool(in, "fixedOnly", false),
		Limit:        clamp(argInt(in, "limit", defaultSimilarLimit), 1, maxSimilarLimit),
	}
}

// FindSimilarBugs ranks historical bugs against a symptom description.
//
// The index is built under a service account, so it is used only to rank
// candidates. Every returned record is re-read with the calling user's own
// credentials, and anything that read cannot see is dropped. No indexed text
// reaches the caller unverified.
func (s *Service) FindSimilarBugs(ctx context.Context, req HistoryRequest) (*HistoryResult, error) {
	ctx, span := s.tracer.Start(ctx, "FindSimilarBugs")
	defer span.End()

	if s.index == nil {
		return nil, fmt.Errorf("%w: the bug index is not enabled for this server", errInvalidArgument)
	}

	if strings.TrimSpace(req.Symptom) == "" {
		return nil, fmt.Errorf("%w: symptom is required", errInvalidArgument)
	}

	if !s.index.Ready() {
		return nil, fmt.Errorf("%w: the bug index is still building, retry in a moment", errIndexNotReady)
	}

	limit := req.Limit
	if limit <= 0 {
		limit = defaultSimilarLimit
	}

	candidateCount := clamp(limit*overFetch, limit, maxVerifyCandidate)

	hits, stats := s.index.Search(bugindex.Query{
		Text:         req.Symptom,
		Product:      req.Product,
		Type:         req.Type,
		Resolution:   req.Resolution,
		OpenedAfter:  req.OpenedAfter,
		OpenedBefore: req.OpenedBefore,
		FixedOnly:    req.FixedOnly,
		PreferFixed:  true,
		Limit:        candidateCount,
	})

	out := &HistoryResult{
		Symptom:    req.Symptom,
		Index:      s.indexStatus(),
		Terms:      stats.Terms,
		Candidates: stats.Candidates,
		Bugs:       []SimilarBug{},
	}

	if len(hits) == 0 {
		out.Notes = append(out.Notes, "没有命中。换用更具体的现象词，或补上设备型号；也可以放宽 openedAfter/type 等过滤条件。")

		return out, nil
	}

	visible, hidden := s.verifyHits(ctx, hits, limit)

	out.Bugs = visible
	out.Returned = len(visible)
	out.Hidden = hidden

	if hidden > 0 {
		out.Notes = append(out.Notes, fmt.Sprintf("%d 条候选因当前账号无权访问已被丢弃，仅统计数量，不返回其内容。", hidden))
	}

	if age := out.Index.AgeHours; age > 24 {
		out.Notes = append(out.Notes, fmt.Sprintf("索引已有 %.0f 小时未刷新，最近新建的 Bug 可能不在其中。", age))
	}

	out.Notes = append(out.Notes,
		"命中是词面相似，不是语义判断。请自行确认现象、设备型号和版本是否真的对得上，再参考其修复方式。")

	return out, nil
}

func (s *Service) indexStatus() IndexStatus {
	status := IndexStatus{Documents: s.index.Len()}

	if built := s.index.BuiltAt(); !built.IsZero() {
		status.BuiltAt = built.In(location).Format("2006-01-02 15:04:05")
		status.AgeHours = time.Since(built).Hours()
	}

	return status
}

// verifyHits re-reads ranked candidates as the calling user, in rank order,
// stopping once enough visible records are collected.
func (s *Service) verifyHits(ctx context.Context, hits []bugindex.Hit, limit int) ([]SimilarBug, int) {
	var (
		visible []SimilarBug
		hidden  int
	)

	for start := 0; start < len(hits) && len(visible) < limit; start += limit {
		end := minInt(start+limit, len(hits))
		batch := hits[start:end]

		records := make([]map[string]any, len(batch))
		errs := make([]error, len(batch))

		var (
			wg  sync.WaitGroup
			sem = make(chan struct{}, verifyConcurrency)
		)

		for i := range batch {
			wg.Add(1)

			go func(i int) {
				defer wg.Done()

				sem <- struct{}{}
				defer func() { <-sem }()

				records[i], errs[i] = s.detail(ctx, fmt.Sprintf("/bugs/%d", batch[i].ID), "bug")
			}(i)
		}

		wg.Wait()

		for i := range batch {
			if len(visible) >= limit {
				break
			}

			if errs[i] != nil || records[i] == nil {
				hidden++

				continue
			}

			visible = append(visible, similarFrom(batch[i], records[i]))
		}
	}

	return visible, hidden
}

// similarFrom builds the returned record from the live payload only.
func similarFrom(hit bugindex.Hit, rec map[string]any) SimilarBug {
	timeline, _, metrics := actionInsights(rec)

	out := SimilarBug{
		ID:           hit.ID,
		Score:        hit.Score,
		Product:      fieldInt(rec, "product", "productID"),
		ProductName:  hit.ProductName,
		Title:        fieldString(rec, "title"),
		Type:         fieldString(rec, "type"),
		Status:       fieldString(rec, "status"),
		Resolution:   fieldString(rec, "resolution"),
		Severity:     fieldInt(rec, "severity"),
		OpenedDate:   dateOnly(field(rec, "openedDate")),
		ResolvedDate: dateOnly(field(rec, "resolvedDate")),
		ResolvedBy:   fieldString(rec, "resolvedBy"),
		MatchedKeys:  hit.MatchedKeys,
		Steps:        truncate(plainText(field(rec, "steps")), stepsClip),
		Comments:     metrics.Comments,
		Reopens:      metrics.Reopens,
	}

	for _, entry := range timeline {
		if strings.EqualFold(entry.Action, "resolved") && entry.Comment != "" {
			out.FixNote = truncate(entry.Comment, fixNoteClip)

			break
		}
	}

	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}

	return b
}
