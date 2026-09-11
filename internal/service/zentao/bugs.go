package zentao

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

const (
	defaultBugLimit   = 20
	maxBugLimit       = 200
	defaultBugScan    = 600
	maxBugScan        = 5000
	defaultScopeProbe = 30
	stepsPreview      = 200
)

// BugSummary is the compact bug projection returned by the extension tools.
type BugSummary struct {
	ID           int      `json:"id"`
	Title        string   `json:"title"`
	Status       string   `json:"status,omitempty"`
	Severity     int      `json:"severity,omitempty"`
	Pri          int      `json:"pri,omitempty"`
	Type         string   `json:"type,omitempty"`
	Product      int      `json:"product,omitempty"`
	Project      int      `json:"project,omitempty"`
	Execution    int      `json:"execution,omitempty"`
	Module       int      `json:"module,omitempty"`
	Confirmed    int      `json:"confirmed,omitempty"`
	ActivatedNum int      `json:"activatedCount,omitempty"`
	OpenedBy     string   `json:"openedBy,omitempty"`
	OpenedDate   string   `json:"openedDate,omitempty"`
	AssignedTo   string   `json:"assignedTo,omitempty"`
	ResolvedBy   string   `json:"resolvedBy,omitempty"`
	ResolvedDate string   `json:"resolvedDate,omitempty"`
	Resolution   string   `json:"resolution,omitempty"`
	ClosedBy     string   `json:"closedBy,omitempty"`
	ClosedDate   string   `json:"closedDate,omitempty"`
	OpenedBuild  string   `json:"openedBuild,omitempty"`
	AIScore      string   `json:"aiScore,omitempty"`
	FixDays      *float64 `json:"fixDays,omitempty"`
	Steps        string   `json:"stepsPreview,omitempty"`
	Scope        string   `json:"foundIn,omitempty"`
}

// BugSearchRequest describes a cross-scope bug search.
type BugSearchRequest struct {
	Scope          string
	ScopeID        int
	ScopeIDs       []int
	Keyword        string
	Status         string
	Resolution     string
	Type           string
	Severity       int
	Pri            int
	Module         int
	AssignedTo     string
	OpenedBy       string
	ResolvedBy     string
	OpenedAfter    string
	OpenedBefore   string
	ResolvedAfter  string
	ResolvedBefore string
	Month          string
	OrderBy        string
	Limit          int
	MaxScan        int
}

// BugSearchResult is the payload returned by the bug search tool.
type BugSearchResult struct {
	Query     map[string]any         `json:"query"`
	Scopes    []ScopeStat            `json:"scopes"`
	Scanned   int                    `json:"scanned"`
	Matched   int                    `json:"matched"`
	Returned  int                    `json:"returned"`
	Truncated bool                   `json:"truncated"`
	Facets    map[string][]NameCount `json:"facets,omitempty"`
	Bugs      []BugSummary           `json:"bugs"`
	Notes     []string               `json:"notes,omitempty"`
}

// BugSearchRequestFrom decodes tool arguments into a search request.
func BugSearchRequestFrom(in map[string]any) BugSearchRequest {
	req := BugSearchRequest{
		Scope:          normalizeScope(argString(in, "scope")),
		ScopeID:        argInt(in, "scopeID", 0),
		ScopeIDs:       argInts(in, "scopeIDs", "productIDs"),
		Keyword:        argString(in, "keyword"),
		Status:         strings.ToLower(argString(in, "status")),
		Resolution:     strings.ToLower(argString(in, "resolution")),
		Type:           strings.ToLower(argString(in, "type")),
		Severity:       argInt(in, "severity", 0),
		Pri:            argInt(in, "pri", 0),
		Module:         argInt(in, "module", 0),
		AssignedTo:     argString(in, "assignedTo"),
		OpenedBy:       argString(in, "openedBy"),
		ResolvedBy:     argString(in, "resolvedBy"),
		OpenedAfter:    normalizeDay(argString(in, "openedAfter")),
		OpenedBefore:   normalizeDay(argString(in, "openedBefore")),
		ResolvedAfter:  normalizeDay(argString(in, "resolvedAfter")),
		ResolvedBefore: normalizeDay(argString(in, "resolvedBefore")),
		Month:          argString(in, "month"),
		OrderBy:        argString(in, "orderBy"),
		Limit:          clamp(argInt(in, "limit", defaultBugLimit), 1, maxBugLimit),
		MaxScan:        clamp(argInt(in, "maxScan", defaultBugScan), 1, maxBugScan),
	}

	if req.ScopeID == 0 {
		req.ScopeID = argInt(in, "productID", 0)
	}

	if req.ScopeID == 0 {
		req.ScopeID = argInt(in, "projectID", 0)
	}

	if req.ScopeID == 0 {
		req.ScopeID = argInt(in, "executionID", 0)
	}

	if req.Scope == "" {
		switch {
		case argInt(in, "executionID", 0) > 0:
			req.Scope = "execution"
		case argInt(in, "projectID", 0) > 0:
			req.Scope = "project"
		default:
			req.Scope = "product"
		}
	}

	if req.Month != "" {
		if from, to, ok := monthRange(req.Month); ok && req.OpenedAfter == "" && req.OpenedBefore == "" {
			req.OpenedAfter, req.OpenedBefore = from, to
		}
	}

	return req
}

// SearchBugs scans one or more scopes and applies the requested filters.
func (s *Service) SearchBugs(ctx context.Context, req BugSearchRequest) (*BugSearchResult, error) {
	ctx, span := s.tracer.Start(ctx, "SearchBugs")
	defer span.End()

	res := &BugSearchResult{
		Query:  req.describe(),
		Facets: map[string][]NameCount{},
		Bugs:   []BugSummary{},
		Scopes: []ScopeStat{},
	}

	targets, notes, err := s.bugScopes(ctx, req)
	if err != nil {
		return nil, err
	}

	res.Notes = append(res.Notes, notes...)

	counts := map[string]map[string]int{
		"status":     {},
		"severity":   {},
		"type":       {},
		"resolution": {},
		"assignedTo": {},
		"openedBy":   {},
	}

	budget := req.MaxScan
	matched := make([]BugSummary, 0, req.Limit)
	seen := make(map[int]bool)

	query := url.Values{}
	query.Set("status", "all")
	query.Set("orderBy", firstNonEmptyText(req.OrderBy, "id_desc"))

	for _, target := range targets {
		if budget <= 0 {
			res.Truncated = true
			res.Notes = append(res.Notes, "scan budget exhausted before every scope was read; raise maxScan or narrow the scope")

			break
		}

		apiPath, err := collectionPath(target.Scope, target.ID, "bugs")
		if err != nil {
			target.Error = err.Error()
			res.Scopes = append(res.Scopes, target)

			continue
		}

		list, err := s.listAll(ctx, apiPath, query, budget)

		target.Scanned = len(list.Records)
		target.Total = list.Total
		target.Truncated = list.Truncated

		if err != nil {
			target.Error = err.Error()
			res.Scopes = append(res.Scopes, target)
			s.logger.WarnContext(ctx, "bug scope scan failed", "path", apiPath, "error", err)

			continue
		}

		budget -= len(list.Records)
		res.Scanned += len(list.Records)

		if list.Truncated {
			res.Truncated = true
		}

		for _, rec := range list.Records {
			if !req.matches(rec) {
				continue
			}

			summary := bugSummary(rec)
			if summary.ID > 0 && seen[summary.ID] {
				continue
			}

			seen[summary.ID] = true
			summary.Scope = fmt.Sprintf("%s:%d", target.Scope, target.ID)

			bump(counts["status"], summary.Status)
			bump(counts["severity"], fmt.Sprintf("%d", summary.Severity))
			bump(counts["type"], summary.Type)
			bump(counts["resolution"], summary.Resolution)
			bump(counts["assignedTo"], summary.AssignedTo)
			bump(counts["openedBy"], summary.OpenedBy)

			matched = append(matched, summary)
		}

		res.Scopes = append(res.Scopes, target)
	}

	sort.Slice(matched, func(i, j int) bool { return matched[i].ID > matched[j].ID })

	res.Matched = len(matched)

	for name, c := range counts {
		limit := 0
		if name == "assignedTo" || name == "openedBy" {
			limit = 10
		}

		if entries := topN(c, limit); len(entries) > 0 {
			res.Facets[name] = entries
		}
	}

	if len(matched) > req.Limit {
		matched = matched[:req.Limit]
		res.Notes = append(res.Notes, fmt.Sprintf("only the first %d of %d matched bugs are listed; facets cover all matches", req.Limit, res.Matched))
	}

	res.Bugs = matched
	res.Returned = len(matched)

	return res, nil
}

// bugScopes resolves the scopes to scan, defaulting to all readable products.
func (s *Service) bugScopes(ctx context.Context, req BugSearchRequest) ([]ScopeStat, []string, error) {
	var (
		targets []ScopeStat
		notes   []string
	)

	ids := req.ScopeIDs
	if req.ScopeID > 0 {
		ids = append([]int{req.ScopeID}, ids...)
	}

	if len(ids) > 0 {
		seen := make(map[int]bool)

		for _, id := range ids {
			if id <= 0 || seen[id] {
				continue
			}

			seen[id] = true

			targets = append(targets, ScopeStat{Scope: req.Scope, ID: id})
		}

		return targets, notes, nil
	}

	if req.Scope != "product" {
		return nil, nil, fmt.Errorf("%w: %s search requires an id", errMissingScope, req.Scope)
	}

	list, err := s.listAll(ctx, "/products", url.Values{}, defaultScopeProbe)
	if err != nil {
		return nil, nil, fmt.Errorf("list products: %w", err)
	}

	for _, rec := range list.Records {
		id := fieldInt(rec, "id")
		if id <= 0 {
			continue
		}

		targets = append(targets, ScopeStat{Scope: "product", ID: id, Name: fieldString(rec, "name")})
	}

	notes = append(notes, fmt.Sprintf("no scope id was given, so %d readable products were scanned; pass productID for a faster and more precise search", len(targets)))

	if list.Truncated {
		notes = append(notes, "more products exist than were scanned; pass productID or productIDs to target them")
	}

	return targets, notes, nil
}

// matches applies every client-side filter to one raw bug record.
func (req BugSearchRequest) matches(rec map[string]any) bool {
	if req.Status != "" && req.Status != "all" && !strings.EqualFold(fieldString(rec, "status"), req.Status) {
		return false
	}

	if req.Resolution != "" && !strings.EqualFold(fieldString(rec, "resolution"), req.Resolution) {
		return false
	}

	if req.Type != "" && !strings.EqualFold(fieldString(rec, "type"), req.Type) {
		return false
	}

	if req.Severity > 0 && fieldInt(rec, "severity") != req.Severity {
		return false
	}

	if req.Pri > 0 && fieldInt(rec, "pri") != req.Pri {
		return false
	}

	if req.Module > 0 && fieldInt(rec, "module") != req.Module {
		return false
	}

	if !matchUser(field(rec, "assignedTo"), req.AssignedTo) {
		return false
	}

	if !matchUser(field(rec, "openedBy"), req.OpenedBy) {
		return false
	}

	if !matchUser(field(rec, "resolvedBy"), req.ResolvedBy) {
		return false
	}

	if !withinRange(field(rec, "openedDate"), req.OpenedAfter, req.OpenedBefore) {
		return false
	}

	if !withinRange(field(rec, "resolvedDate"), req.ResolvedAfter, req.ResolvedBefore) {
		return false
	}

	if req.Keyword != "" {
		haystack := strings.Join([]string{
			fieldString(rec, "title"),
			plainText(field(rec, "steps")),
			fieldString(rec, "keywords"),
			fieldString(rec, "moduleName"),
		}, "\n")

		if !containsFold(haystack, req.Keyword) {
			return false
		}
	}

	return true
}

func (req BugSearchRequest) describe() map[string]any {
	out := map[string]any{
		"scope":   req.Scope,
		"limit":   req.Limit,
		"maxScan": req.MaxScan,
	}

	pairs := map[string]string{
		"keyword":        req.Keyword,
		"status":         req.Status,
		"resolution":     req.Resolution,
		"type":           req.Type,
		"assignedTo":     req.AssignedTo,
		"openedBy":       req.OpenedBy,
		"resolvedBy":     req.ResolvedBy,
		"openedAfter":    req.OpenedAfter,
		"openedBefore":   req.OpenedBefore,
		"resolvedAfter":  req.ResolvedAfter,
		"resolvedBefore": req.ResolvedBefore,
		"month":          req.Month,
		"orderBy":        req.OrderBy,
	}

	for k, v := range pairs {
		if v != "" {
			out[k] = v
		}
	}

	nums := map[string]int{
		"scopeID":  req.ScopeID,
		"severity": req.Severity,
		"pri":      req.Pri,
		"module":   req.Module,
	}

	for k, v := range nums {
		if v > 0 {
			out[k] = v
		}
	}

	if len(req.ScopeIDs) > 0 {
		out["scopeIDs"] = req.ScopeIDs
	}

	return out
}

// bugSummary projects a raw ZenTao bug record onto the compact summary.
func bugSummary(rec map[string]any) BugSummary {
	out := BugSummary{
		ID:           fieldInt(rec, "id"),
		Title:        fieldString(rec, "title"),
		Status:       fieldString(rec, "status"),
		Severity:     fieldInt(rec, "severity"),
		Pri:          fieldInt(rec, "pri"),
		Type:         fieldString(rec, "type"),
		Product:      fieldInt(rec, "product", "productID"),
		Project:      fieldInt(rec, "project"),
		Execution:    fieldInt(rec, "execution"),
		Module:       fieldInt(rec, "module"),
		Confirmed:    fieldInt(rec, "confirmed"),
		ActivatedNum: fieldInt(rec, "activatedCount"),
		OpenedBy:     fieldString(rec, "openedBy"),
		OpenedDate:   dateOnly(field(rec, "openedDate")),
		AssignedTo:   fieldString(rec, "assignedTo"),
		ResolvedBy:   fieldString(rec, "resolvedBy"),
		ResolvedDate: dateOnly(field(rec, "resolvedDate")),
		Resolution:   fieldString(rec, "resolution"),
		ClosedBy:     fieldString(rec, "closedBy"),
		ClosedDate:   dateOnly(field(rec, "closedDate")),
		OpenedBuild:  fieldString(rec, "openedBuild"),
		AIScore:      fieldString(rec, "aiScore"),
		Steps:        truncate(plainText(field(rec, "steps")), stepsPreview),
	}

	if days, ok := daysBetween(field(rec, "openedDate"), field(rec, "resolvedDate")); ok {
		out.FixDays = &days
	}

	return out
}

func firstNonEmptyText(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}

	return ""
}
