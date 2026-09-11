package bugindex

import (
	"math"
	"sort"
	"strings"
)

// BM25 parameters. k1 controls term frequency saturation, b controls length
// normalization. These are the standard defaults and suit short defect reports.
const (
	bm25K1 = 1.2
	bm25B  = 0.75

	// Boost factors applied after BM25. A record that was actually fixed is
	// worth more than one closed as notrepro, and an exact hit on a device
	// model or province is a much stronger signal than loose text overlap.
	boostFixed     = 1.35
	boostCodeError = 1.15
	boostExactKey  = 1.6

	defaultLimit = 10
	maxLimit     = 50
	// candidatePool caps how many scored documents are kept before boosts and
	// filters are applied.
	candidatePool = 500
)

// Query describes a symptom lookup.
type Query struct {
	// Text is the free-form symptom description.
	Text string
	// Product, Type and Resolution filter exactly when non-zero.
	Product    int
	Type       string
	Resolution string
	// OpenedAfter and OpenedBefore bound the open date, inclusive, YYYY-MM-DD.
	OpenedAfter  string
	OpenedBefore string
	// FixedOnly keeps only records that were resolved as fixed.
	FixedOnly bool
	// PreferFixed boosts fixed records instead of filtering to them.
	PreferFixed bool
	Limit       int
}

// Hit is one ranked candidate. It carries only identifying and ranking data;
// the caller is expected to re-read the record with the end user's own
// credentials before showing any content.
type Hit struct {
	ID          int
	Score       float64
	Product     int
	ProductName string
	Title       string
	Type        string
	Status      string
	Resolution  string
	Severity    int
	OpenedDate  string
	Tags        []string
	MatchedKeys []string
}

// Stats describes what a search looked at.
type Stats struct {
	Documents  int
	Candidates int
	Returned   int
	Terms      int
}

// Search ranks documents against the query.
func (ix *Index) Search(q Query) ([]Hit, Stats) {
	ix.mu.RLock()
	snap := ix.snap
	ix.mu.RUnlock()

	limit := q.Limit
	if limit <= 0 {
		limit = defaultLimit
	}

	if limit > maxLimit {
		limit = maxLimit
	}

	stats := Stats{Documents: len(snap.docs)}

	terms := dedupe(Tokenize(q.Text))
	stats.Terms = len(terms)

	if len(snap.docs) == 0 || len(terms) == 0 {
		return nil, stats
	}

	scores := map[uint32]float64{}
	n := float64(len(snap.docs))

	for _, term := range terms {
		posts := snap.postings[term]
		if len(posts) == 0 {
			continue
		}

		// A term present in most documents carries no signal; BM25's idf
		// already damps it, and this keeps the candidate map smaller.
		idf := math.Log(1 + (n-float64(len(posts))+0.5)/(float64(len(posts))+0.5))
		if idf <= 0 {
			continue
		}

		for _, p := range posts {
			tf := float64(p.tf)
			dl := float64(snap.docLen[p.slot])
			denom := tf + bm25K1*(1-bm25B+bm25B*dl/snap.avgDL)

			if denom == 0 {
				continue
			}

			scores[p.slot] += idf * (tf * (bm25K1 + 1)) / denom
		}
	}

	stats.Candidates = len(scores)

	queryKeys := queryBoostKeys(q.Text)
	hits := make([]Hit, 0, minInt(len(scores), candidatePool))

	for slot, score := range scores {
		doc := &snap.docs[slot]

		if !doc.passes(q) {
			continue
		}

		matched := intersect(snap.keys[slot], queryKeys)

		if q.PreferFixed && strings.EqualFold(doc.Resolution, "fixed") {
			score *= boostFixed
		}

		if strings.EqualFold(doc.Type, "codeerror") {
			score *= boostCodeError
		}

		if len(matched) > 0 {
			score *= boostExactKey
		}

		hits = append(hits, Hit{
			ID:          doc.ID,
			Score:       math.Round(score*100) / 100,
			Product:     doc.Product,
			ProductName: doc.ProductName,
			Title:       doc.Title,
			Type:        doc.Type,
			Status:      doc.Status,
			Resolution:  doc.Resolution,
			Severity:    doc.Severity,
			OpenedDate:  doc.OpenedDate,
			Tags:        doc.Tags,
			MatchedKeys: matched,
		})
	}

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}

		return hits[i].ID > hits[j].ID
	})

	if len(hits) > limit {
		hits = hits[:limit]
	}

	stats.Returned = len(hits)

	return hits, stats
}

// passes applies the exact filters of a query to one document.
func (d *Doc) passes(q Query) bool {
	if q.Product > 0 && d.Product != q.Product {
		return false
	}

	if q.Type != "" && !strings.EqualFold(d.Type, q.Type) {
		return false
	}

	if q.Resolution != "" && !strings.EqualFold(d.Resolution, q.Resolution) {
		return false
	}

	if q.FixedOnly && !strings.EqualFold(d.Resolution, "fixed") {
		return false
	}

	if q.OpenedAfter != "" && (d.OpenedDate == "" || d.OpenedDate < q.OpenedAfter) {
		return false
	}

	if q.OpenedBefore != "" && (d.OpenedDate == "" || d.OpenedDate > q.OpenedBefore) {
		return false
	}

	return true
}

// queryBoostKeys extracts the exact-match handles from a symptom description:
// anything the reporter wrote in brackets, plus latin runs such as a model
// number pasted straight out of an alarm.
func queryBoostKeys(text string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 4)

	add := func(s string) {
		s = normalizeKey(s)
		if s == "" || len(s) < minLatinToken {
			return
		}

		if _, ok := seen[s]; ok {
			return
		}

		seen[s] = struct{}{}
		out = append(out, s)
	}

	for _, t := range ExtractTags(text) {
		add(t)
	}

	for _, t := range latinPattern.FindAllString(text, -1) {
		add(t)
	}

	return out
}

func intersect(docKeys, queryKeys []string) []string {
	if len(docKeys) == 0 || len(queryKeys) == 0 {
		return nil
	}

	want := make(map[string]struct{}, len(queryKeys))
	for _, k := range queryKeys {
		want[k] = struct{}{}
	}

	var out []string

	for _, k := range docKeys {
		if _, ok := want[k]; ok {
			out = append(out, k)
		}
	}

	return out
}

func dedupe(tokens []string) []string {
	seen := make(map[string]struct{}, len(tokens))
	out := make([]string, 0, len(tokens))

	for _, t := range tokens {
		if _, ok := seen[t]; ok {
			continue
		}

		seen[t] = struct{}{}
		out = append(out, t)
	}

	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}

	return b
}
