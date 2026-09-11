package bugindex

import (
	"regexp"
	"strings"
)

var (
	hanPattern    = regexp.MustCompile(`\p{Han}+`)
	latinPattern  = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9_.\-]*`)
	bracketTag    = regexp.MustCompile(`【([^】]{1,40})】`)
	tagPattern    = regexp.MustCompile(`(?s)<[^>]*>`)
	spacePattern  = regexp.MustCompile(`\s+`)
	minLatinToken = 2
)

// Tokenize turns text into index terms.
//
// CJK runs become overlapping bigrams, because Chinese has no word boundaries
// and the defect vocabulary here is largely two characters wide (重启, 告警,
// 丢包, 速率). A single character run is kept whole so it is still findable.
// Latin and numeric runs are lowercased and kept intact, which preserves the
// device models that identify a record (DCMG150, FXM6000, H3-1sProLite).
func Tokenize(text string) []string {
	if text == "" {
		return nil
	}

	out := make([]string, 0, len(text)/2)

	for _, word := range latinPattern.FindAllString(text, -1) {
		if len(word) >= minLatinToken {
			out = append(out, strings.ToLower(word))
		}
	}

	for _, run := range hanPattern.FindAllString(text, -1) {
		r := []rune(run)

		if len(r) == 1 {
			out = append(out, string(r))

			continue
		}

		for i := 0; i+1 < len(r); i++ {
			out = append(out, string(r[i:i+2]))
		}
	}

	return out
}

// ExtractTags pulls the bracketed segments out of a ZenTao title. Bug titles
// in this corpus are written as 【device】【module】description, so these
// segments are usable as facets: 81% of the bugs carry at least one.
func ExtractTags(title string) []string {
	matches := bracketTag.FindAllStringSubmatch(title, -1)
	if len(matches) == 0 {
		return nil
	}

	out := make([]string, 0, len(matches))

	for _, m := range matches {
		tag := strings.TrimSpace(m[1])
		if tag != "" {
			out = append(out, tag)
		}
	}

	return out
}

// PlainText strips the HTML ZenTao stores in rich text fields.
func PlainText(s string) string {
	if s == "" {
		return ""
	}

	for _, br := range []string{"<br>", "<br/>", "<br />", "</p>", "</div>", "</li>", "</tr>"} {
		s = strings.ReplaceAll(s, br, " ")
	}

	s = tagPattern.ReplaceAllString(s, " ")
	s = strings.NewReplacer("&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`).Replace(s)

	return strings.TrimSpace(spacePattern.ReplaceAllString(s, " "))
}

// Clip shortens text on rune boundaries.
func Clip(s string, limit int) string {
	if limit <= 0 {
		return s
	}

	r := []rune(s)
	if len(r) <= limit {
		return s
	}

	return string(r[:limit])
}

func normalizeKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
