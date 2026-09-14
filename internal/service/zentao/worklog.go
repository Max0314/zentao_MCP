package zentao

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

const (
	defaultWorklogScan  = 800
	maxWorklogScan      = 5000
	defaultWorklogLimit = 50
	maxWorklogLimit     = 300
)

// WorkItem is one task, bug or story attributed to a user in a time window.
type WorkItem struct {
	Kind       string   `json:"kind"`
	ID         int      `json:"id"`
	Title      string   `json:"title"`
	Status     string   `json:"status,omitempty"`
	Roles      []string `json:"roles"`
	Scope      string   `json:"foundIn,omitempty"`
	OpenedBy   string   `json:"openedBy,omitempty"`
	OpenedDate string   `json:"openedDate,omitempty"`
	AssignedTo string   `json:"assignedTo,omitempty"`
	DoneBy     string   `json:"doneBy,omitempty"`
	DoneDate   string   `json:"doneDate,omitempty"`
	Estimate   string   `json:"estimate,omitempty"`
	Consumed   string   `json:"consumed,omitempty"`
	AIScore    string   `json:"aiScore,omitempty"`
}

// WorklogResult is the payload returned by the personal worklog tool.
type WorklogResult struct {
	Account string         `json:"account"`
	From    string         `json:"from,omitempty"`
	To      string         `json:"to,omitempty"`
	Scopes  []ScopeStat    `json:"scopes"`
	Summary map[string]int `json:"summary"`
	Roles   []NameCount    `json:"roleBreakdown,omitempty"`
	Items   []WorkItem     `json:"items"`
	Notes   []string       `json:"notes,omitempty"`
}

// WorklogRequest describes a personal worklog lookup.
type WorklogRequest struct {
	Account      string
	From         string
	To           string
	ExecutionIDs []int
	ProductIDs   []int
	ProjectIDs   []int
	Kinds        []string
	Limit        int
	MaxScan      int
}

// WorklogRequestFrom decodes tool arguments into a worklog request.
func WorklogRequestFrom(in map[string]any) (WorklogRequest, error) {
	req := WorklogRequest{
		Account:      argString(in, "account", "user"),
		ExecutionIDs: argInts(in, "executionIDs", "executionID"),
		ProductIDs:   argInts(in, "productIDs", "productID"),
		ProjectIDs:   argInts(in, "projectIDs", "projectID"),
		Limit:        clamp(argInt(in, "limit", defaultWorklogLimit), 1, maxWorklogLimit),
		MaxScan:      clamp(argInt(in, "maxScan", defaultWorklogScan), 1, maxWorklogScan),
	}

	var err error

	if req.From, err = normalizeDay("from", argString(in, "from", "after")); err != nil {
		return WorklogRequest{}, err
	}

	if req.To, err = normalizeDay("to", argString(in, "to", "before")); err != nil {
		return WorklogRequest{}, err
	}

	from, to, err := monthWindow(argString(in, "month"))
	if err != nil {
		return WorklogRequest{}, err
	}

	if from != "" {
		if req.From == "" {
			req.From = from
		}

		if req.To == "" {
			req.To = to
		}
	}

	for _, kind := range argStrings(in, "kinds", "kind") {
		switch strings.ToLower(strings.TrimSpace(kind)) {
		case "task", "tasks":
			req.Kinds = append(req.Kinds, "task")
		case "bug", "bugs":
			req.Kinds = append(req.Kinds, "bug")
		case "story", "stories":
			req.Kinds = append(req.Kinds, "story")
		}
	}

	if len(req.Kinds) == 0 {
		req.Kinds = []string{"task", "bug", "story"}
	}

	return req, nil
}

// UserWorklog lists what one account opened, finished or resolved in a window.
func (s *Service) UserWorklog(ctx context.Context, req WorklogRequest) (*WorklogResult, error) {
	ctx, span := s.tracer.Start(ctx, "UserWorklog")
	defer span.End()

	if req.Account == "" {
		return nil, fmt.Errorf("%w: account is required", errInvalidArgument)
	}

	if len(req.ExecutionIDs) == 0 && len(req.ProductIDs) == 0 && len(req.ProjectIDs) == 0 {
		return nil, fmt.Errorf("%w: pass executionIDs, productIDs or projectIDs; use zentao_resolve_scope to find them", errInvalidArgument)
	}

	out := &WorklogResult{
		Account: req.Account,
		From:    req.From,
		To:      req.To,
		Scopes:  []ScopeStat{},
		Summary: map[string]int{},
		Items:   []WorkItem{},
	}

	wanted := map[string]bool{}
	for _, kind := range req.Kinds {
		wanted[kind] = true
	}

	budget := req.MaxScan
	budgetNoted := false
	roles := map[string]int{}
	seen := map[string]bool{}

	targets := []struct {
		scope       string
		ids         []int
		collections []string
	}{
		{"execution", req.ExecutionIDs, []string{"tasks", "bugs", "stories"}},
		{"project", req.ProjectIDs, []string{"bugs", "stories"}},
		{"product", req.ProductIDs, []string{"bugs", "stories"}},
	}

	for _, target := range targets {
		for _, id := range target.ids {
			for _, collection := range target.collections {
				kind := strings.TrimSuffix(collection, "s")
				if kind == "storie" {
					kind = "story"
				}

				if !wanted[kind] {
					continue
				}

				if budget <= 0 {
					if !budgetNoted {
						budgetNoted = true

						out.Notes = append(out.Notes, "scan budget exhausted before every scope was read; raise maxScan or pass fewer scopes")
					}

					continue
				}

				stat, items, used := s.worklogScope(ctx, req, target.scope, id, collection, kind, budget)
				budget -= used

				out.Scopes = append(out.Scopes, stat)

				for _, item := range items {
					key := fmt.Sprintf("%s:%d", item.Kind, item.ID)
					if seen[key] {
						continue
					}

					seen[key] = true
					out.Summary[item.Kind]++

					for _, role := range item.Roles {
						roles[role]++
					}

					out.Items = append(out.Items, item)
				}
			}
		}
	}

	sort.Slice(out.Items, func(i, j int) bool {
		if out.Items[i].Kind != out.Items[j].Kind {
			return out.Items[i].Kind < out.Items[j].Kind
		}

		return out.Items[i].ID > out.Items[j].ID
	})

	out.Summary["total"] = len(out.Items)
	out.Roles = topN(roles, 0)

	if len(out.Items) > req.Limit {
		out.Notes = append(out.Notes, fmt.Sprintf("only the first %d of %d items are listed; summary counts cover all of them", req.Limit, len(out.Items)))
		out.Items = out.Items[:req.Limit]
	}

	if len(out.Items) == 0 {
		out.Notes = append(out.Notes, "nothing matched; confirm the ZenTao account name (not the display name) and the scope ids")
	}

	return out, nil
}

// worklogScope scans one collection of one scope and keeps the user's records.
func (s *Service) worklogScope(ctx context.Context, req WorklogRequest, scope string, id int, collection, kind string, budget int) (ScopeStat, []WorkItem, int) {
	stat := ScopeStat{Scope: scope, ID: id, Name: collection}

	apiPath, err := collectionPath(scope, id, collection)
	if err != nil {
		stat.Error = err.Error()

		return stat, nil, 0
	}

	query := url.Values{}
	query.Set("status", "all")
	query.Set("orderBy", "id_desc")

	list, err := s.listAll(ctx, apiPath, query, budget)

	stat.Scanned = len(list.Records)
	stat.Total = list.Total
	stat.Truncated = list.Truncated

	if err != nil {
		stat.Error = err.Error()
		s.logger.WarnContext(ctx, "worklog scope scan failed", "path", apiPath, "error", err)

		return stat, nil, len(list.Records)
	}

	items := make([]WorkItem, 0, 8)

	for _, rec := range list.Records {
		if item, ok := worklogItem(kind, rec, req.Account, req.From, req.To); ok {
			item.Scope = fmt.Sprintf("%s:%d", scope, id)
			items = append(items, item)
		}
	}

	return stat, items, len(list.Records)
}

// worklogItem decides whether one record belongs to the account and window.
func worklogItem(kind string, rec map[string]any, account, from, to string) (WorkItem, bool) {
	item := WorkItem{
		Kind:       kind,
		ID:         fieldInt(rec, "id"),
		Title:      fieldString(rec, "title", "name"),
		Status:     fieldString(rec, "status"),
		OpenedBy:   fieldString(rec, "openedBy"),
		OpenedDate: dateOnly(field(rec, "openedDate")),
		AssignedTo: fieldString(rec, "assignedTo"),
		Estimate:   fieldString(rec, "estimate"),
		Consumed:   fieldString(rec, "consumed"),
		AIScore:    fieldString(rec, "aiScore"),
	}

	if item.ID <= 0 {
		return item, false
	}

	doneField, doneDateField := "finishedBy", "finishedDate"
	if kind == "bug" {
		doneField, doneDateField = "resolvedBy", "resolvedDate"
	}

	if kind == "story" {
		doneField, doneDateField = "closedBy", "closedDate"
	}

	item.DoneBy = fieldString(rec, doneField)
	item.DoneDate = dateOnly(field(rec, doneDateField))

	if matchUser(field(rec, "openedBy"), account) && withinRange(field(rec, "openedDate"), from, to) {
		item.Roles = append(item.Roles, "opened")
	}

	if matchUser(field(rec, doneField), account) && withinRange(field(rec, doneDateField), from, to) {
		switch kind {
		case "bug":
			item.Roles = append(item.Roles, "resolved")
		case "story":
			item.Roles = append(item.Roles, "closed")
		default:
			item.Roles = append(item.Roles, "finished")
		}
	}

	if len(item.Roles) == 0 && matchUser(field(rec, "assignedTo"), account) {
		if withinRange(field(rec, "openedDate"), from, to) || withinRange(field(rec, doneDateField), from, to) {
			item.Roles = append(item.Roles, "assigned")
		}
	}

	return item, len(item.Roles) > 0
}
