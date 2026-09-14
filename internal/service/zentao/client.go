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
}

func (e *APIError) Error() string {
	return fmt.Sprintf("zentao GET %s: HTTP %d: %s", e.Path, e.Status, e.Body)
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
			Path:   apiPath,
			Status: resp.StatusCode,
			Body:   truncate(strings.TrimSpace(string(body)), 300),
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

// objectPath maps an object type to its detail path and payload wrapper key.
func objectPath(objectType string, id int) (string, string, error) {
	switch strings.ToLower(strings.TrimSpace(objectType)) {
	case "bug", "bugs":
		return fmt.Sprintf("/bugs/%d", id), "bug", nil
	case "task", "tasks":
		return fmt.Sprintf("/tasks/%d", id), "task", nil
	case "story", "stories", "requirement":
		return fmt.Sprintf("/stories/%d", id), "story", nil
	case "testcase", "testcases", "case":
		return fmt.Sprintf("/testcases/%d", id), "testcase", nil
	}

	return "", "", fmt.Errorf("%w: %q (use bug, task, story or testcase)", errUnknownObject, objectType)
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
