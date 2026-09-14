package zentao

import (
	"context"
	"net/url"
	"sort"
	"strings"
)

const (
	defaultResolveScan  = 300
	maxResolveScan      = 2000
	defaultResolveLimit = 10
	maxResolveLimit     = 50
)

// ScopeMatch is one product, project, execution or user matched by keyword.
type ScopeMatch struct {
	Kind     string `json:"kind"`
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Account  string `json:"account,omitempty"`
	Code     string `json:"code,omitempty"`
	Status   string `json:"status,omitempty"`
	Parent   string `json:"parent,omitempty"`
	Begin    string `json:"begin,omitempty"`
	End      string `json:"end,omitempty"`
	Deleted  bool   `json:"deleted,omitempty"`
	UsableAs string `json:"usableAs,omitempty"`
}

// ResolveResult is the payload returned by the scope resolver tool.
type ResolveResult struct {
	Keyword string       `json:"keyword"`
	Kinds   []string     `json:"kinds"`
	Matches []ScopeMatch `json:"matches"`
	Counts  []NameCount  `json:"countsByKind,omitempty"`
	Notes   []string     `json:"notes,omitempty"`
}

// ResolveRequest describes a keyword lookup for ZenTao ids.
type ResolveRequest struct {
	Keyword string
	Kinds   []string
	Limit   int
	MaxScan int
}

var resolveKinds = map[string]struct {
	path     string
	usableAs string
}{
	"product":   {path: "/products", usableAs: "productID for bug, story and testcase lists"},
	"project":   {path: "/projects", usableAs: "projectID for bug, story and execution lists"},
	"execution": {path: "/executions", usableAs: "executionID for task, bug and story lists, and for creating tasks"},
	"program":   {path: "/programs", usableAs: "programID for product and project lists"},
	"user":      {path: "/users", usableAs: "account for assignedTo, openedBy and resolvedBy filters"},
}

// ResolveRequestFrom decodes tool arguments into a scope lookup request.
func ResolveRequestFrom(in map[string]any) (ResolveRequest, error) {
	req := ResolveRequest{
		Keyword: argString(in, "keyword", "name", "query"),
		Limit:   clamp(argInt(in, "limit", defaultResolveLimit), 1, maxResolveLimit),
		MaxScan: clamp(argInt(in, "maxScan", defaultResolveScan), 1, maxResolveScan),
	}

	for _, kind := range argStrings(in, "kinds", "kind") {
		normalized := normalizeKind(kind)
		if _, ok := resolveKinds[normalized]; ok {
			req.Kinds = append(req.Kinds, normalized)
		}
	}

	if len(req.Kinds) == 0 {
		req.Kinds = []string{"product", "project", "execution", "user"}
	}

	return req, nil
}

func normalizeKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "product", "products", "产品":
		return "product"
	case "project", "projects", "项目":
		return "project"
	case "execution", "executions", "sprint", "iteration", "执行", "迭代":
		return "execution"
	case "program", "programs", "项目集":
		return "program"
	case "user", "users", "account", "人员", "用户":
		return "user"
	}

	return strings.ToLower(strings.TrimSpace(kind))
}

// ResolveScope turns a name fragment into the ids the other tools need.
func (s *Service) ResolveScope(ctx context.Context, req ResolveRequest) (*ResolveResult, error) {
	ctx, span := s.tracer.Start(ctx, "ResolveScope")
	defer span.End()

	out := &ResolveResult{Keyword: req.Keyword, Kinds: req.Kinds, Matches: []ScopeMatch{}}
	counts := map[string]int{}

	for _, kind := range req.Kinds {
		spec, ok := resolveKinds[kind]
		if !ok {
			continue
		}

		query := url.Values{}
		if kind != "user" {
			query.Set("status", "all")
		}

		list, err := s.listAll(ctx, spec.path, query, req.MaxScan)
		if err != nil {
			out.Notes = append(out.Notes, kind+" lookup failed: "+err.Error())
			s.logger.WarnContext(ctx, "scope lookup failed", "kind", kind, "error", err)

			continue
		}

		if list.Truncated {
			out.Notes = append(out.Notes, kind+" results are partial; raise maxScan if the expected entry is missing")
		}

		for _, rec := range list.Records {
			match, ok := scopeMatch(kind, spec.usableAs, rec, req.Keyword)
			if !ok {
				continue
			}

			counts[kind]++

			out.Matches = append(out.Matches, match)
		}
	}

	sort.Slice(out.Matches, func(i, j int) bool {
		if out.Matches[i].Kind != out.Matches[j].Kind {
			return out.Matches[i].Kind < out.Matches[j].Kind
		}

		return out.Matches[i].ID > out.Matches[j].ID
	})

	out.Counts = topN(counts, 0)

	if len(out.Matches) > req.Limit {
		out.Notes = append(out.Notes, "more entries matched than were listed; refine the keyword or raise limit")
		out.Matches = out.Matches[:req.Limit]
	}

	if len(out.Matches) == 0 {
		out.Notes = append(out.Notes, "nothing matched; try a shorter keyword, or drop it to list what this account can read")
	}

	return out, nil
}

func scopeMatch(kind, usableAs string, rec map[string]any, keyword string) (ScopeMatch, bool) {
	id := fieldInt(rec, "id")
	if id <= 0 {
		return ScopeMatch{}, false
	}

	name := fieldString(rec, "name", "realname", "realName", "title")
	account := fieldString(rec, "account")

	haystack := strings.Join([]string{name, account, fieldString(rec, "code"), fieldString(rec, "abbr")}, " ")
	if !containsFold(haystack, keyword) {
		return ScopeMatch{}, false
	}

	return ScopeMatch{
		Kind:     kind,
		ID:       id,
		Name:     name,
		Account:  account,
		Code:     fieldString(rec, "code"),
		Status:   fieldString(rec, "status"),
		Parent:   fieldString(rec, "parentName", "projectName", "productName"),
		Begin:    dateOnly(field(rec, "begin")),
		End:      dateOnly(field(rec, "end")),
		Deleted:  fieldString(rec, "deleted") == "1",
		UsableAs: usableAs,
	}, true
}

// argStrings reads a string list argument, accepting arrays and separated text.
func argStrings(in map[string]any, names ...string) []string {
	v := field(in, names...)
	if v == nil {
		return nil
	}

	var out []string

	switch t := v.(type) {
	case []any:
		for _, item := range t {
			if s := strings.TrimSpace(asString(item)); s != "" {
				out = append(out, s)
			}
		}
	case string:
		for _, part := range strings.FieldsFunc(t, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
			if s := strings.TrimSpace(part); s != "" {
				out = append(out, s)
			}
		}
	}

	return out
}
