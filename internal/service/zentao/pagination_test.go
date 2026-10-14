package zentao

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/merzzzl/openapi-mcp-server/internal/service/bugindex"
)

type pagingProxy struct {
	respond func(*http.Request) (int, map[string]any)
}

func (p pagingProxy) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	status, payload := p.respond(req)
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), Request: req}, nil
}

func records(first, last int) []map[string]any {
	out := []map[string]any{}
	for id := first; id <= last; id++ {
		out = append(out, map[string]any{"id": id, "title": "historical bug", "product": 1})
	}
	return out
}

func TestListAllBudgetAndExactLastPage(t *testing.T) {
	for _, tc := range []struct {
		name          string
		total, budget int
		reported      bool
		want          int
		truncated     bool
	}{
		{"past_50_pages", 6200, 7000, true, 6200, false},
		{"exact_full_page", 100, 100, true, 100, false},
		{"exact_full_page_without_total", 100, 100, false, 100, false},
		{"budget_without_total", 200, 100, false, 100, true},
		{"partial_last_page", 150, 200, true, 150, false},
		{"budget_in_middle_of_page", 200, 150, true, 150, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pages := 0
			proxy := pagingProxy{respond: func(r *http.Request) (int, map[string]any) {
				pages++
				page, _ := strconv.Atoi(r.URL.Query().Get("page"))
				limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
				payload := map[string]any{"bugs": records((page-1)*limit+1, min(page*limit, tc.total))}
				if tc.reported {
					payload["total"] = tc.total
				}
				return 200, payload
			}}
			res, err := New(proxy, testBaseURL).listAll(context.Background(), "/bugs", url.Values{}, tc.budget)
			if err != nil || len(res.Records) != tc.want || res.Truncated != tc.truncated {
				t.Fatalf("records=%d truncated=%v reason=%q err=%v", len(res.Records), res.Truncated, res.TruncationReason, err)
			}
			if tc.total > 5000 && pages <= 50 {
				t.Fatal("the legacy 50-page ceiling remains")
			}
		})
	}
}

func TestListAllDeduplicatesOverlapsAndStopsOnRepeatedPages(t *testing.T) {
	for _, repeat := range []bool{false, true} {
		pages := 0
		proxy := pagingProxy{respond: func(r *http.Request) (int, map[string]any) {
			pages++
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			items := records(1, 100)
			if !repeat {
				if page == 2 {
					items = records(96, 195)
				}
				if page == 3 {
					items = records(196, 200)
				}
			}
			return 200, map[string]any{"total": 200, "bugs": items}
		}}
		res, err := New(proxy, testBaseURL).listAll(context.Background(), "/bugs", nil, 250)
		if err != nil {
			t.Fatal(err)
		}
		if repeat {
			if !res.Truncated || res.TruncationReason != "pagination_no_progress" || pages != 2 {
				t.Fatalf("repeated page not stopped: %+v pages=%d", res, pages)
			}
		} else if len(res.Records) != 200 || res.Truncated {
			t.Fatalf("overlapping page lost records: %d / %v", len(res.Records), res.Truncated)
		}
	}
}

func TestZeroReportedTotalDoesNotStopAFullPage(t *testing.T) {
	proxy := pagingProxy{respond: func(r *http.Request) (int, map[string]any) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		return 200, map[string]any{"total": 0, "bugs": records((page-1)*100+1, min(page*100, 150))}
	}}
	res, err := New(proxy, testBaseURL).listAll(context.Background(), "/bugs", nil, 200)
	if err != nil || len(res.Records) != 150 || res.Truncated {
		t.Fatalf("invalid zero total stopped pagination: %d %v %v", len(res.Records), res.Truncated, err)
	}
}

func TestIndexReportsPartialCoverageEvenWhenQueryHasNoHits(t *testing.T) {
	proxy := pagingProxy{respond: func(r *http.Request) (int, map[string]any) {
		if strings.HasSuffix(r.URL.Path, "/products") {
			return 200, map[string]any{"total": 2, "products": records(1, 2)}
		}
		return 200, map[string]any{"total": 3, "bugs": records(1, 3)}
	}}
	svc := New(proxy, testBaseURL)
	svc.AttachIndex(bugindex.New(), IndexOptions{Account: "svc", Password: "secret", MaxProducts: 1, MaxBugsPerProduct: 2})
	svc.refreshIndex(context.Background())
	out, err := svc.FindSimilarBugs(context.Background(), HistoryRequest{Symptom: "unmatched_unique_term", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Index.Partial || !out.Index.ProductListTruncated || out.Index.TruncatedProducts != 1 || out.Index.Documents != 2 {
		t.Fatalf("truncation not exposed: %+v", out.Index)
	}
	if len(out.Notes) < 2 || !strings.Contains(out.Notes[0], "覆盖不完整") {
		t.Fatalf("no-hit result concealed missing coverage: %v", out.Notes)
	}
}

func TestFailedRefreshRetainsPreviousIndexAndReportsFailure(t *testing.T) {
	proxy := pagingProxy{respond: func(r *http.Request) (int, map[string]any) {
		if strings.HasSuffix(r.URL.Path, "/products") {
			return 200, map[string]any{"total": 1, "products": records(1, 1)}
		}
		return 500, map[string]any{"error": "unavailable"}
	}}
	svc := New(proxy, testBaseURL)
	ix := bugindex.New()
	ix.Replace([]bugindex.Doc{{ID: 1, Title: "existing history"}})
	svc.AttachIndex(ix, IndexOptions{Account: "svc", Password: "secret"})
	svc.refreshIndex(context.Background())
	if ix.Len() != 1 || !svc.indexStatus().LastRefreshFailed {
		t.Fatal("failed refresh replaced a good corpus or claimed success")
	}
	empty := New(proxy, testBaseURL)
	empty.AttachIndex(bugindex.New(), IndexOptions{Account: "svc", Password: "secret"})
	empty.refreshIndex(context.Background())
	if st := empty.buildState.Load(); st == nil || st.err == nil {
		t.Fatal("a wholly failed first build was reported as an empty success")
	}
}
