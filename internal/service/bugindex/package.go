// Package bugindex is an in-memory full text index for short Chinese and
// mixed-script records, built for "find historical issues that look like this
// symptom" lookups.
//
// It is deliberately dependency free. ZenTao v1 exposes no text search at all
// (every keyword-ish query parameter is silently ignored), and SQLite FTS5's
// trigram tokenizer misses two character Chinese terms such as 重启 or 告警,
// which are exactly the words defect reports are written in. So the index
// tokenizes CJK into overlapping bigrams and scores with BM25.
//
// The package knows nothing about ZenTao or about who is allowed to read a
// record. It ranks candidates; enforcing per-user visibility is the caller's
// job.
package bugindex

import (
	"sync"
	"time"
)

// Doc is one indexed record.
type Doc struct {
	ID           int
	Product      int
	ProductName  string
	Module       int
	Title        string
	Body         string
	Tags         []string
	Severity     int
	Type         string
	Status       string
	Resolution   string
	OpenedDate   string
	ResolvedDate string
}

// posting is one token occurrence inside one document.
type posting struct {
	slot uint32
	tf   uint32
}

// snapshot is an immutable index generation. Replace swaps a whole snapshot so
// searches never observe a half built index.
type snapshot struct {
	docs     []Doc
	keys     [][]string
	postings map[string][]posting
	docLen   []uint32
	avgDL    float64
	builtAt  time.Time
}

// Index holds the current snapshot behind a read-mostly lock.
type Index struct {
	mu   sync.RWMutex
	snap *snapshot
}

// New returns an empty index.
func New() *Index {
	return &Index{snap: &snapshot{postings: map[string][]posting{}}}
}

// Replace atomically installs a new generation built from docs.
func (ix *Index) Replace(docs []Doc) {
	// Build outside the lock. Tokenizing a 17k document corpus takes seconds,
	// and doing it inside the critical section stalled every concurrent search
	// for that whole window - Go's RWMutex also blocks new readers once a
	// writer is waiting, so requests arriving just before a refresh queued up.
	snap := build(docs)

	ix.mu.Lock()
	ix.snap = snap
	ix.mu.Unlock()
}

// Len reports how many documents the current generation holds.
func (ix *Index) Len() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()

	return len(ix.snap.docs)
}

// BuiltAt reports when the current generation was installed. A zero time means
// no generation has been built yet.
func (ix *Index) BuiltAt() time.Time {
	ix.mu.RLock()
	defer ix.mu.RUnlock()

	return ix.snap.builtAt
}

// Ready reports whether a generation is available to search.
func (ix *Index) Ready() bool {
	return ix.Len() > 0
}

// MaxID returns the largest document id in the current generation, which lets
// a builder pull only newer records on a refresh.
func (ix *Index) MaxID() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()

	best := 0

	for i := range ix.snap.docs {
		if ix.snap.docs[i].ID > best {
			best = ix.snap.docs[i].ID
		}
	}

	return best
}

// Docs returns a copy of the current generation, so a refresh can merge new
// records into the records it already has.
func (ix *Index) Docs() []Doc {
	ix.mu.RLock()
	defer ix.mu.RUnlock()

	out := make([]Doc, len(ix.snap.docs))
	copy(out, ix.snap.docs)

	return out
}

// build turns documents into a searchable generation. Titles are weighted by
// indexing their tokens twice, which keeps a title hit ahead of a body hit
// without a second postings list.
func build(docs []Doc) *snapshot {
	snap := &snapshot{
		docs:     docs,
		keys:     make([][]string, len(docs)),
		postings: make(map[string][]posting, len(docs)*8),
		docLen:   make([]uint32, len(docs)),
		builtAt:  time.Now(),
	}

	total := uint64(0)

	for slot := range docs {
		counts := map[string]uint32{}

		for _, tok := range Tokenize(docs[slot].Title) {
			counts[tok] += 2
		}

		for _, tok := range Tokenize(docs[slot].Body) {
			counts[tok]++
		}

		length := uint32(0)

		for tok, tf := range counts {
			snap.postings[tok] = append(snap.postings[tok], posting{slot: uint32(slot), tf: tf})
			length += tf
		}

		snap.docLen[slot] = length
		total += uint64(length)
		snap.keys[slot] = boostKeys(docs[slot])
	}

	if len(docs) > 0 {
		snap.avgDL = float64(total) / float64(len(docs))
	}

	if snap.avgDL == 0 {
		snap.avgDL = 1
	}

	return snap
}

// boostKeys are the exact-match handles of a document: its bracketed tags plus
// the latin runs of its title. In this corpus those carry the device model,
// the province and the carrier, so an exact hit on one is a strong signal.
func boostKeys(d Doc) []string {
	return dedupedKeys(d.Tags, d.Title)
}
