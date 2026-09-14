package zentao

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/merzzzl/openapi-mcp-server/internal/middleware"
)

const (
	maxResponseSize = 10 << 20
	defaultPageSize = 100
	maxPages        = 50
)

var (
	errUnknownScope    = errors.New("unknown scope")
	errMissingScope    = errors.New("a scope id is required")
	errUnknownObject   = errors.New("unknown object type")
	errInvalidArgument = errors.New("invalid argument")
	errIndexNotReady   = errors.New("index not ready")
)

// listKeys are the collection names ZenTao v1 uses to wrap list payloads.
var listKeys = []string{
	"bugs", "tasks", "stories", "products", "projects", "executions", "programs",
	"users", "testcases", "cases", "builds", "releases", "productplans", "plans",
	"feedbacks", "tickets", "data", "list",
}

// APIError reports a non-2xx upstream ZenTao response.
type APIError struct {
	Path   string
	Status int
	Body   string
	// AuthFailed marks a response produced because logging in failed, rather
	// than because the upstream refused this particular object. ZenTao uses
	// the same status code for both.
	AuthFailed bool
}

func (e *APIError) Error() string {
	if e.AuthFailed {
		return fmt.Sprintf("zentao login failed (HTTP %d): %s", e.Status, e.Body)
	}

	return fmt.Sprintf("zentao GET %s: HTTP %d: %s", e.Path, e.Status, e.Body)
}

// isAuthFailure reports whether an error came from a failed login.
func isAuthFailure(err error) bool {
	var apiErr *APIError

	return errors.As(err, &apiErr) && apiErr.AuthFailed
}

// getJSON performs an authenticated GET against the upstream ZenTao v1 API.
// Credentials are attached by the shared auth transport from the request context.
func (s *Service) getJSON(ctx context.Context, apiPath string, query url.Values) (map[string]any, error) {
	u, err := url.Parse(s.baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base URL: %w", err)
	}

	u.Path = path.Join(u.Path, apiPath)

	if len(query) > 0 {
		u.RawQuery = query.Encode()
	}

	// The body must be nil rather than http.NoBody: http.NewRequest stores
	// http.NoBody as a non-nil Body without setting GetBody, which makes the
	// request look unreplayable and stops the auth transport from refreshing
	// an expired ZenTao token after a 401.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := s.proxy.Do(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, &APIError{
			Path:       apiPath,
			Status:     resp.StatusCode,
			Body:       truncate(strings.TrimSpace(string(body)), 300),
			AuthFailed: resp.Header.Get(middleware.AuthFailureHeader) != "",
		}
	}

	var data map[string]any

	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("decode %s: %w", apiPath, err)
	}

	return data, nil
}

// detail fetches a single object, tolerating both flat and wrapped payloads.
func (s *Service) detail(ctx context.Context, apiPath, wrapKey string) (map[string]any, error) {
	data, err := s.getJSON(ctx, apiPath, nil)
	if err != nil {
		return nil, err
	}

	if inner, ok := data[wrapKey].(map[string]any); ok && len(inner) > 0 {
		return inner, nil
	}

	return data, nil
}

// listResult holds one paged scan over a ZenTao collection endpoint.
type listResult struct {
	Records   []map[string]any
	Total     int
	Truncated bool
}

// listAll pages through a ZenTao collection endpoint up to maxScan records.
func (s *Service) listAll(ctx context.Context, apiPath string, query url.Values, maxScan int) (listResult, error) {
	res := listResult{}

	if maxScan <= 0 {
		maxScan = defaultPageSize
	}

	pageSize := defaultPageSize
	if maxScan < pageSize {
		pageSize = maxScan
	}

	for page := 1; page <= maxPages; page++ {
		q := url.Values{}

		for k, vs := range query {
			q[k] = append([]string(nil), vs...)
		}

		q.Set("limit", strconv.Itoa(pageSize))
		q.Set("page", strconv.Itoa(page))

		data, err := s.getJSON(ctx, apiPath, q)
		if err != nil {
			return res, err
		}

		if total, ok := toInt(data["total"]); ok && total > res.Total {
			res.Total = total
		}

		items := pickList(data)
		res.Records = append(res.Records, items...)

		if len(items) < pageSize {
			break
		}

		if len(res.Records) >= maxScan || page == maxPages {
			res.Truncated = true

			break
		}
	}

	if len(res.Records) > maxScan {
		res.Records = res.Records[:maxScan]
		res.Truncated = true
	}

	if res.Total > len(res.Records) {
		res.Truncated = true
	}

	return res, nil
}

// pickList finds the collection array inside a ZenTao list payload.
func pickList(data map[string]any) []map[string]any {
	for _, key := range listKeys {
		if items, ok := data[key].([]any); ok {
			return toRecords(items)
		}
	}

	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	for _, k := range keys {
		if items, ok := data[k].([]any); ok {
			return toRecords(items)
		}
	}

	return nil
}

func toRecords(items []any) []map[string]any {
	out := make([]map[string]any, 0, len(items))

	for _, item := range items {
		if rec, ok := item.(map[string]any); ok {
			out = append(out, rec)
		}
	}

	return out
}

// forEachBounded runs fn for indices [0,n) with at most limit running at once.
//
// Every scan in this package is a set of independent upstream reads, and the
// upstream is the bottleneck: one detail read takes ~0.3s and one list page
// ~0.2s, so doing them one after another is what made a full index build take
// minutes and a 50-object score lookup exceed client timeouts.
func forEachBounded(n, limit int, fn func(i int)) {
	if limit < 1 {
		limit = 1
	}

	var wg sync.WaitGroup

	sem := make(chan struct{}, limit)

	for i := range n {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			fn(i)
		}(i)
	}

	wg.Wait()
}

// ScopeStat reports how much of one scope was scanned.
type ScopeStat struct {
	Scope     string `json:"scope"`
	ID        int    `json:"id"`
	Name      string `json:"name,omitempty"`
	Scanned   int    `json:"scanned"`
	Total     int    `json:"total,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Error     string `json:"error,omitempty"`
}

// collectionPath builds the ZenTao v1 list path for a scope and collection.
// Only routes confirmed to work on ZenTao Enterprise 12.3 v1 are allowed.
func collectionPath(scope string, id int, collection string) (string, error) {
	if id <= 0 {
		return "", fmt.Errorf("%w for scope %q", errMissingScope, scope)
	}

	allowed := map[string]map[string]bool{
		"product": {"bugs": true, "stories": true, "testcases": true, "releases": true},
		"project": {"bugs": true, "stories": true, "testcases": true, "testtasks": true, "builds": true, "executions": true},
		"execution": {"bugs": true, "stories": true, "testcases": true, "tasks": true, "builds": true},
	}

	collections, ok := allowed[scope]
	if !ok {
		return "", fmt.Errorf("%w: %q (use product, project or execution)", errUnknownScope, scope)
	}

	if !collections[collection] {
		return "", fmt.Errorf("%w: %q has no %q list on ZenTao 12.3 v1", errUnknownScope, scope, collection)
	}

	return fmt.Sprintf("/%ss/%d/%s", scope, id, collection), nil
}

// resolveObject maps an object type alias to its collection name and payload
// wrapper key. One table, so a list read and a detail read can never disagree
// about what an alias such as "requirement" means.
func resolveObject(objectType string) (string, string, error) {
	switch strings.ToLower(strings.TrimSpace(objectType)) {
	case "bug", "bugs":
		return "bugs", "bug", nil
	case "task", "tasks":
		return "tasks", "task", nil
	case "story", "stories", "requirement", "requirements":
		return "stories", "story", nil
	case "testcase", "testcases", "case":
		return "testcases", "testcase", nil
	}

	return "", "", fmt.Errorf("%w: %q (use bug, task, story or testcase)", errUnknownObject, objectType)
}

// objectPath maps an object type to its detail path and payload wrapper key.
func objectPath(objectType string, id int) (string, string, error) {
	collection, wrapKey, err := resolveObject(objectType)
	if err != nil {
		return "", "", err
	}

	return fmt.Sprintf("/%s/%d", collection, id), wrapKey, nil
}

// scopeAliases maps a tool argument name to the scope it implies.
var scopeAliases = []struct {
	arg   string
	scope string
}{
	{arg: "productID", scope: "product"},
	{arg: "projectID", scope: "project"},
	{arg: "executionID", scope: "execution"},
}

// scopeSelector resolves the scope and its id from the canonical scope/scopeID
// pair plus the productID/projectID/executionID aliases.
//
// The scope always comes from whichever alias supplied the id, so the two can
// never disagree. Resolving them independently previously let
// {"productID":10,"executionID":55} scan /executions/10/bugs - a real endpoint
// returning a real but unrelated bug list, with no error.
func scopeSelector(in map[string]any, defaultScope string) (string, int, error) {
	scope := normalizeScope(argString(in, "scope"))
	id := argInt(in, "scopeID", 0)

	var (
		aliasScope string
		aliasID    int
		seen       []string
	)

	for _, alias := range scopeAliases {
		v := argInt(in, alias.arg, 0)
		if v <= 0 {
			continue
		}

		seen = append(seen, alias.arg)
		aliasScope, aliasID = alias.scope, v
	}

	if len(seen) > 1 {
		return "", 0, fmt.Errorf("%w: %s were given together; pass exactly one scope",
			errInvalidArgument, strings.Join(seen, " and "))
	}

	if aliasID > 0 {
		if id > 0 && id != aliasID {
			return "", 0, fmt.Errorf("%w: scopeID=%d conflicts with %s=%d",
				errInvalidArgument, id, seen[0], aliasID)
		}

		if scope != "" && scope != aliasScope {
			return "", 0, fmt.Errorf("%w: scope=%q conflicts with %s",
				errInvalidArgument, scope, seen[0])
		}

		return aliasScope, aliasID, nil
	}

	if scope == "" {
		scope = defaultScope
	}

	// No scope at all and none required: the caller decides whether that is an
	// error, so it can explain what else it would have accepted.
	if scope == "" && id == 0 {
		return "", 0, nil
	}

	switch scope {
	case "product", "project", "execution":
		return scope, id, nil
	}

	return "", 0, fmt.Errorf("%w: %q (use product, project or execution)", errUnknownScope, scope)
}

// normalizeScope maps user supplied scope aliases to canonical scope names.
func normalizeScope(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "product", "products", "产品":
		return "product"
	case "project", "projects", "项目":
		return "project"
	case "execution", "executions", "sprint", "iteration", "执行", "迭代":
		return "execution"
	}

	return strings.ToLower(strings.TrimSpace(scope))
}
