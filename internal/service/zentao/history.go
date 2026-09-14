package zentao

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
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
	// maxFailedScanPercent is how much of a refresh may fail before the new
	// generation is considered too degraded to replace a larger healthy one.
	maxFailedScanPercent = 20
	// indexConcurrency bounds the parallel product scans of one build.
	indexConcurrency = 8
	indexPageSize    = 100

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

// HasIndex reports whether historical symptom lookup is available, so the
// controller can avoid advertising a tool that would always fail.
func (s *Service) HasIndex() bool {
	return s.index != nil
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

	docs, scanned, failed, err := s.buildCorpus(buildCtx)
	if err != nil {
		s.buildState.Store(&buildState{done: true, err: err, at: time.Now()})

		s.logger.ErrorContext(ctx, "bug index build failed", "error", err, "elapsed", time.Since(start).String())

		return
	}

	s.buildState.Store(&buildState{done: true, at: time.Now()})

	// Never trade a healthy corpus for a degraded one. Per-product scan
	// failures are individually survivable, but when the service account's
	// token expires mid-build most products fail at once; swapping that in
	// silently shrank every symptom lookup to a fraction of the corpus and
	// looked exactly like "no similar bug exists".
	if failed*100 >= scanned*maxFailedScanPercent && s.index.Len() > len(docs) {
		s.logger.ErrorContext(ctx, "bug index refresh abandoned: too many product scans failed",
			"failed", failed,
			"products", scanned,
			"new_documents", len(docs),
			"kept_documents", s.index.Len(),
		)

		return
	}

	s.index.Replace(docs)

	s.logger.InfoContext(ctx, "bug index built",
		"documents", len(docs),
		"products", scanned,
		"failed_products", failed,
		"elapsed", time.Since(start).String(),
	)
}

// buildCorpus pulls every readable bug into indexable documents. It reports how
// many products were scanned and how many of those scans failed, so the caller
// can refuse to install a badly degraded generation.
func (s *Service) buildCorpus(ctx context.Context) ([]bugindex.Doc, int, int, error) {
	products, err := s.listAll(ctx, "/products", url.Values{}, s.indexOpts.MaxProducts)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("list products: %w", err)
	}

	var scanned, failed int

	query := url.Values{}
	query.Set("status", "all")
	query.Set("orderBy", "id_desc")

	// Scan products concurrently: 600+ products at ~0.2s per page made a
	// sequential build take minutes, during which the corpus is stale.
	type productScan struct {
		docs    []bugindex.Doc
		err     error
		counted bool
	}

	recs := products.Records
	scans := make([]productScan, len(recs))

	forEachBounded(len(recs), indexConcurrency, func(i int) {
		pid := fieldInt(recs[i], "id")
		if pid <= 0 {
			return
		}

		pname := fieldString(recs[i], "name")
		scans[i].counted = true

		bugs, err := s.listAll(ctx, fmt.Sprintf("/products/%d/bugs", pid), query, s.indexOpts.MaxBugsPerProduct)
		if err != nil {
			scans[i].err = err

			return
		}

		docs := make([]bugindex.Doc, 0, len(bugs.Records))

		for _, rec := range bugs.Records {
			id := fieldInt(rec, "id")
			if id <= 0 {
				continue
			}

			title := fieldString(rec, "title")

			docs = append(docs, bugindex.Doc{
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
			})
		}

		scans[i].docs = docs
	})

	// Merge on one goroutine; the upstream repeats records across page
	// boundaries, so collect by id.
	byID := make(map[int]bugindex.Doc, 4096)

	for i := range scans {
		if !scans[i].counted {
			continue
		}

		scanned++

		if scans[i].err != nil {
			failed++

			s.logger.WarnContext(ctx, "bug index: product scan failed",
				"product", fieldInt(recs[i], "id"), "error", scans[i].err)

			continue
		}

		for _, d := range scans[i].docs {
			byID[d.ID] = d
		}
	}

	docs := make([]bugindex.Doc, 0, len(byID))
	for _, d := range byID {
		docs = append(docs, d)
	}

	return docs, scanned, failed, nil
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
	Symptom  string       `json:"symptom"`
	Index    IndexStatus  `json:"index"`
	Terms    int          `json:"queryTerms"`
	Returned int          `json:"returned"`
	// Hidden counts candidates the caller may not read. The corpus-wide match
	// count is deliberately not reported: it was computed before any filtering
	// or permission check, so it answered "how many bugs anywhere in ZenTao
	// mention this phrase" for a caller who can read none of them.
	Hidden      int          `json:"hiddenByPermission,omitempty"`
	Unavailable int          `json:"unavailable,omitempty"`
	Bugs        []SimilarBug `json:"bugs"`
	Notes       []string     `json:"notes,omitempty"`
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
func HistoryRequestFrom(in map[string]any) (HistoryRequest, error) {
	req := HistoryRequest{
		Symptom:    argString(in, "symptom", "text", "keyword"),
		Product:    argInt(in, "productID", argInt(in, "product", 0)),
		Type:       strings.ToLower(argString(in, "type")),
		Resolution: strings.ToLower(argString(in, "resolution")),
		FixedOnly:  argBool(in, "fixedOnly", false),
		Limit:      clamp(argInt(in, "limit", defaultSimilarLimit), 1, maxSimilarLimit),
	}

	var err error

	if req.OpenedAfter, err = normalizeDay("openedAfter", argString(in, "openedAfter")); err != nil {
		return HistoryRequest{}, err
	}

	if req.OpenedBefore, err = normalizeDay("openedBefore", argString(in, "openedBefore")); err != nil {
		return HistoryRequest{}, err
	}

	return req, nil
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
		// Distinguish the three states that all used to say "retry in a moment".
		// A failed build will not fix itself before the next refresh tick, so
		// telling the caller to retry was simply wrong.
		switch st := s.buildState.Load(); {
		case st == nil || !st.done:
			return nil, fmt.Errorf("%w: 索引正在首次构建，请稍后重试", errIndexNotReady)
		case st.err != nil:
			return nil, fmt.Errorf("%w: 索引构建失败（%v），在下次刷新前重试不会有变化，请检查索引服务账号和禅道连通性",
				errIndexNotReady, st.err)
		default:
			return nil, fmt.Errorf("%w: 索引构建成功但没有任何记录，请确认索引服务账号能读到 Bug 数据", errIndexNotReady)
		}
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
		Symptom: req.Symptom,
		Index:   s.indexStatus(),
		Terms:   stats.Terms,
		Bugs:    []SimilarBug{},
	}

	s.logger.InfoContext(ctx, "similar bug search",
		"terms", stats.Terms,
		"corpus_candidates", stats.Candidates,
		"ranked", len(hits),
	)

	if len(hits) == 0 {
		out.Notes = append(out.Notes, "没有命中。换用更具体的现象词，或补上设备型号；也可以放宽 openedAfter/type 等过滤条件。")

		return out, nil
	}

	visible, hidden, unavailable := s.verifyHits(ctx, hits, limit)

	out.Bugs = visible
	out.Returned = len(visible)
	out.Hidden = hidden
	out.Unavailable = unavailable

	if hidden > 0 {
		out.Notes = append(out.Notes, fmt.Sprintf("%d 条候选因当前账号无权访问已被丢弃，仅统计数量，不返回其内容。", hidden))
	}

	if unavailable > 0 {
		out.Notes = append(out.Notes, fmt.Sprintf("%d 条候选回查失败（超时、限流或服务端错误，非权限问题），稍后重试可能会多出结果。", unavailable))
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
		status.BuiltAt = built.In(currentLocation()).Format("2006-01-02 15:04:05")
		status.AgeHours = time.Since(built).Hours()
	}

	return status
}

// verifyHits re-reads ranked candidates as the calling user, in rank order,
// stopping once enough visible records are collected.
func (s *Service) verifyHits(ctx context.Context, hits []bugindex.Hit, limit int) ([]SimilarBug, int, int) {
	var (
		visible     []SimilarBug
		hidden      int
		unavailable int
	)

	for start := 0; start < len(hits) && len(visible) < limit; start += limit {
		end := minInt(start+limit, len(hits))
		batch := hits[start:end]

		records := make([]map[string]any, len(batch))
		errs := make([]error, len(batch))

		forEachBounded(len(batch), verifyConcurrency, func(i int) {
			records[i], errs[i] = s.detail(ctx, fmt.Sprintf("/bugs/%d", batch[i].ID), "bug")
		})

		for i := range batch {
			if len(visible) >= limit {
				break
			}

			if errs[i] != nil || records[i] == nil {
				if isDenied(errs[i]) {
					hidden++
				} else {
					unavailable++
				}

				continue
			}

			// ZenTao answers some denied reads with HTTP 200 and an error body,
			// and detail() falls back to the whole payload when the wrapper key
			// is absent - so "no transport error" is not proof of access.
			// Require the record to echo the id we asked for.
			if fieldInt(records[i], "id") != batch[i].ID {
				hidden++

				continue
			}

			visible = append(visible, similarFrom(batch[i], records[i]))
		}
	}

	return visible, hidden, unavailable
}

// isDenied reports whether an upstream error means "you may not see this",
// as opposed to a timeout, a rate limit or a server fault. Reporting the two
// alike told the caller they lacked permission when the upstream was simply
// unwell, steering them toward an access request instead of a retry.
func isDenied(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}

	switch apiErr.Status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		return true
	}

	return false
}

// similarFrom builds the returned record from the live payload only.
func similarFrom(hit bugindex.Hit, rec map[string]any) SimilarBug {
	timeline, _, metrics := actionInsights(rec)

	out := SimilarBug{
		ID:           hit.ID,
		Score:        hit.Score,
		Product:      fieldInt(rec, "product", "productID"),
		ProductName:  fieldString(rec, "productName", "productTitle"),
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
