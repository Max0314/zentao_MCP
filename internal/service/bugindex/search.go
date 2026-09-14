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
// The corpus is built by a service account, so a Hit deliberately carries no
// content: title, product name, status and tags stay inside the index and the
// caller re-reads them under its own credentials. MatchedKeys is the caller's
// own query terms, and is only ever surfaced for a record that passed that
// re-read.
type Hit struct {
	ID          int
	Score       float64
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

	// Hoisted out of the loop: intersect() rebuilt this set for every candidate
	// document, and a common bigram produces thousands of candidates.
	want := make(map[string]struct{}, len(queryKeys))
	for _, k := range queryKeys {
		want[k] = struct{}{}
	}

	// Rank on a lightweight (slot, score) pair and materialise a full Hit only
	// for the survivors. Building a Hit per matching document copied the title
	// and two slices each time; a corpus-wide bigram such as 设备 matches most
	// records, so one query allocated and sorted ~17k of them to return 8.
	ranked := make([]scored, 0, minInt(len(scores), candidatePool))

	for slot, score := range scores {
		doc := &snap.docs[slot]

		if !doc.passes(q) {
			continue
		}

		if q.PreferFixed && strings.EqualFold(doc.Resolution, "fixed") {
			score *= boostFixed
		}

		if strings.EqualFold(doc.Type, "codeerror") {
			score *= boostCodeError
		}

		if intersects(snap.keys[slot], want) {
			score *= boostExactKey
		}

		ranked = append(ranked, scored{slot: slot, score: score})
	}

	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}

		return snap.docs[ranked[i].slot].ID > snap.docs[ranked[j].slot].ID
	})

	if len(ranked) > limit {
		ranked = ranked[:limit]
	}

	hits := make([]Hit, 0, len(ranked))

	for _, r := range ranked {
		doc := &snap.docs[r.slot]

		hits = append(hits, Hit{
			ID:          doc.ID,
			Score:       math.Round(r.score*100) / 100,
			MatchedKeys: intersect(snap.keys[r.slot], queryKeys),
		})
	}

	stats.Returned = len(hits)

	return hits, stats
}

// scored is the ranking pair: a document slot and its boosted score.
type scored struct {
	slot  uint32
	score float64
}

// intersects reports whether any document key is in the query key set.
func intersects(docKeys []string, want map[string]struct{}) bool {
	if len(docKeys) == 0 || len(want) == 0 {
		return false
	}

	for _, k := range docKeys {
		if _, ok := want[k]; ok {
			return true
		}
	}

	return false
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
	return dedupedKeys(ExtractTags(text), text)
}

// dedupedKeys builds the exact-match handles of a record or a query: the
// bracketed tags (device model, province, carrier) plus the latin runs of the
// title. Index side and query side must use one implementation - when only the
// query side enforced the minimum length, a short key written into the index
// could never be matched and its boost silently never fired.
func dedupedKeys(tags []string, title string) []string {
	seen := make(map[string]struct{}, len(tags)+4)
	out := make([]string, 0, len(tags)+4)

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

	for _, t := range tags {
		add(t)
	}

	for _, t := range latinPattern.FindAllString(title, -1) {
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
