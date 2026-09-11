package zentao

import (
	"encoding/json"
	"fmt"
	"html"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const emptyDatePrefix = "0000-00-00"

var (
	tagPattern    = regexp.MustCompile(`(?s)<[^>]*>`)
	spacePattern  = regexp.MustCompile(`[ \t\x{00a0}]+`)
	blankPattern  = regexp.MustCompile(`\n{3,}`)
	hanPattern    = regexp.MustCompile(`\p{Han}+`)
	latinPattern  = regexp.MustCompile(`[a-z0-9_]{2,}`)
	userRefFields = []string{"account", "realname", "realName", "name", "id"}
)

// ZenTao v1 mixes two date encodings in one payload: object fields such as
// openedDate come back as UTC instants ("2026-08-31T08:18:28Z") while action
// dates come back as bare local wall clock ("2026-08-31 16:18:28"). Zoned
// values must be converted to the ZenTao server's zone before a calendar date
// is taken, or records created between midnight and 08:00 Beijing time land on
// the previous day and drop out of month filters. Bare values are already local.
var (
	zonedLayouts = []string{
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02T15:04:05.999999999Z07:00",
	}
	localLayouts = []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"2006-01-02",
		"2006/01/02 15:04:05",
	}

	// location is the ZenTao server's wall-clock zone. It is process wide
	// because every configured upstream is the same deployment; SetLocation
	// must be called before any server starts serving.
	location = defaultLocation()
)

// defaultLocation resolves Asia/Shanghai, falling back to a fixed +08:00 zone
// when the runtime image ships no tzdata.
func defaultLocation() *time.Location {
	if loc, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		return loc
	}

	return time.FixedZone("CST", 8*60*60)
}

// SetLocation overrides the ZenTao wall-clock zone by IANA name. An empty or
// unknown name keeps the current zone and reports the error.
func SetLocation(name string) error {
	if strings.TrimSpace(name) == "" {
		return nil
	}

	loc, err := time.LoadLocation(name)
	if err != nil {
		return fmt.Errorf("load timezone %q: %w", name, err)
	}

	location = loc

	return nil
}

// LocationName reports the zone used to turn ZenTao timestamps into dates.
func LocationName() string {
	return location.String()
}

// plainString renders a scalar JSON value as text.
func plainString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		if t == math.Trunc(t) && math.Abs(t) < 1e15 {
			return strconv.FormatInt(int64(t), 10)
		}

		return strconv.FormatFloat(t, 'f', -1, 64)
	case json.Number:
		return t.String()
	default:
		return fmt.Sprint(v)
	}
}

// userRef extracts a readable account from a ZenTao user reference object.
func userRef(m map[string]any) string {
	for _, key := range userRefFields {
		if s := strings.TrimSpace(plainString(m[key])); s != "" && s != "0" {
			return s
		}
	}

	return ""
}

// asString renders any ZenTao field, including user reference objects, as text.
func asString(v any) string {
	if m, ok := v.(map[string]any); ok {
		return userRef(m)
	}

	return plainString(v)
}

// toInt converts a ZenTao field to an int, tolerating numeric strings and refs.
func toInt(v any) (int, bool) {
	switch t := v.(type) {
	case float64:
		return int(t), true
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return int(i), true
		}
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0, false
		}

		if i, err := strconv.Atoi(s); err == nil {
			return i, true
		}

		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return int(f), true
		}
	case bool:
		if t {
			return 1, true
		}

		return 0, true
	case map[string]any:
		if id, ok := t["id"]; ok {
			return toInt(id)
		}
	}

	return 0, false
}

// toFloat converts a ZenTao field to a float, tolerating numeric strings.
func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case json.Number:
		if f, err := t.Float64(); err == nil {
			return f, true
		}
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0, false
		}

		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f, true
		}
	}

	return 0, false
}

// field returns the first present, non-nil value among the given field names.
func field(rec map[string]any, names ...string) any {
	for _, n := range names {
		if v, ok := rec[n]; ok && v != nil {
			return v
		}
	}

	return nil
}

func fieldString(rec map[string]any, names ...string) string {
	return strings.TrimSpace(asString(field(rec, names...)))
}

func fieldInt(rec map[string]any, names ...string) int {
	n, _ := toInt(field(rec, names...))

	return n
}

// parseDate parses the date formats returned by ZenTao and returns the instant
// in the ZenTao server's zone, rejecting zero dates. Zoned inputs are
// converted; bare wall-clock inputs are read as already being in that zone.
func parseDate(v any) (time.Time, bool) {
	s := strings.TrimSpace(asString(v))
	if s == "" || strings.HasPrefix(s, emptyDatePrefix) {
		return time.Time{}, false
	}

	for _, layout := range zonedLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.In(location), true
		}
	}

	for _, layout := range localLayouts {
		if t, err := time.ParseInLocation(layout, s, location); err == nil {
			return t, true
		}
	}

	return time.Time{}, false
}

func dateOnly(v any) string {
	t, ok := parseDate(v)
	if !ok {
		return ""
	}

	return t.Format("2006-01-02")
}

func dateTime(v any) string {
	t, ok := parseDate(v)
	if !ok {
		return ""
	}

	return t.Format("2006-01-02 15:04:05")
}

// normalizeDay normalizes a user supplied boundary to YYYY-MM-DD.
func normalizeDay(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	if t, ok := parseDate(s); ok {
		return t.Format("2006-01-02")
	}

	return s
}

// monthRange converts YYYY-MM into inclusive first and last day boundaries.
func monthRange(month string) (string, string, bool) {
	t, err := time.Parse("2006-01", strings.TrimSpace(month))
	if err != nil {
		return "", "", false
	}

	return t.Format("2006-01-02"), t.AddDate(0, 1, -1).Format("2006-01-02"), true
}

// withinRange reports whether a ZenTao date falls inside an inclusive day window.
func withinRange(v any, after, before string) bool {
	if after == "" && before == "" {
		return true
	}

	t, ok := parseDate(v)
	if !ok {
		return false
	}

	day := t.Format("2006-01-02")

	if after != "" && day < after {
		return false
	}

	if before != "" && day > before {
		return false
	}

	return true
}

// daysBetween returns the elapsed days between two ZenTao dates.
func daysBetween(from, to any) (float64, bool) {
	start, ok := parseDate(from)
	if !ok {
		return 0, false
	}

	end, ok := parseDate(to)
	if !ok {
		return 0, false
	}

	if end.Before(start) {
		return 0, false
	}

	return math.Round(end.Sub(start).Hours()/24*100) / 100, true
}

// plainText converts ZenTao rich text into readable plain text.
func plainText(v any) string {
	s := asString(v)
	if s == "" {
		return ""
	}

	for _, br := range []string{"<br>", "<br/>", "<br />", "<BR>", "</p>", "</P>", "</div>", "</li>", "</tr>"} {
		s = strings.ReplaceAll(s, br, "\n")
	}

	s = tagPattern.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = spacePattern.ReplaceAllString(s, " ")

	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}

	s = blankPattern.ReplaceAllString(strings.Join(lines, "\n"), "\n\n")

	return strings.TrimSpace(s)
}

// truncate shortens text on rune boundaries.
func truncate(s string, limit int) string {
	if limit <= 0 {
		return s
	}

	r := []rune(s)
	if len(r) <= limit {
		return s
	}

	return string(r[:limit]) + "..."
}

func containsFold(haystack, needle string) bool {
	if needle == "" {
		return true
	}

	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

// matchUser compares an account, real name or id against a wanted user.
func matchUser(v any, want string) bool {
	if want == "" {
		return true
	}

	want = strings.ToLower(strings.TrimSpace(want))

	if m, ok := v.(map[string]any); ok {
		for _, key := range userRefFields {
			if strings.ToLower(strings.TrimSpace(plainString(m[key]))) == want {
				return true
			}
		}

		return false
	}

	return strings.ToLower(strings.TrimSpace(asString(v))) == want
}

// titleTokens builds a comparable token set from a title, using Han bigrams.
func titleTokens(s string) map[string]struct{} {
	out := make(map[string]struct{})

	for _, w := range latinPattern.FindAllString(strings.ToLower(s), -1) {
		out[w] = struct{}{}
	}

	for _, run := range hanPattern.FindAllString(s, -1) {
		r := []rune(run)
		for i := 0; i+1 < len(r); i++ {
			out[string(r[i:i+2])] = struct{}{}
		}
	}

	return out
}

// similarity returns the Jaccard similarity of two token sets.
func similarity(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}

	small, large := a, b
	if len(large) < len(small) {
		small, large = large, small
	}

	inter := 0

	for k := range small {
		if _, ok := large[k]; ok {
			inter++
		}
	}

	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}

	return math.Round(float64(inter)/float64(union)*1000) / 1000
}

// NameCount is a ranked counter entry.
type NameCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// topN ranks a counter map by count and then name.
func topN(counts map[string]int, n int) []NameCount {
	out := make([]NameCount, 0, len(counts))

	for name, count := range counts {
		if name == "" {
			continue
		}

		out = append(out, NameCount{Name: name, Count: count})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}

		return out[i].Name < out[j].Name
	})

	if n > 0 && len(out) > n {
		out = out[:n]
	}

	return out
}

func bump(counts map[string]int, key string) {
	if key == "" {
		key = "(empty)"
	}

	counts[key]++
}

func average(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}

	sum := 0.0
	for _, v := range values {
		sum += v
	}

	avg := math.Round(sum/float64(len(values))*100) / 100

	return &avg
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}

	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}

	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}

	return math.Round(sorted[idx]*100) / 100
}

func ratio(part, total int) float64 {
	if total <= 0 {
		return 0
	}

	return math.Round(float64(part)/float64(total)*1000) / 10
}

// argString reads a string tool argument, accepting several field aliases.
func argString(in map[string]any, names ...string) string {
	return strings.TrimSpace(asString(field(in, names...)))
}

// argInt reads an int tool argument with a default.
func argInt(in map[string]any, name string, def int) int {
	if v, ok := in[name]; ok && v != nil {
		if n, ok := toInt(v); ok {
			return n
		}
	}

	return def
}

// argBool reads a bool tool argument with a default.
func argBool(in map[string]any, name string, def bool) bool {
	v, ok := in[name]
	if !ok || v == nil {
		return def
	}

	switch t := v.(type) {
	case bool:
		return t
	case string:
		if b, err := strconv.ParseBool(strings.TrimSpace(t)); err == nil {
			return b
		}
	case float64:
		return t != 0
	}

	return def
}

// argInts reads an int list argument, accepting arrays and separated text.
func argInts(in map[string]any, names ...string) []int {
	v := field(in, names...)
	if v == nil {
		return nil
	}

	var out []int

	switch t := v.(type) {
	case []any:
		for _, item := range t {
			if n, ok := toInt(item); ok && n > 0 {
				out = append(out, n)
			}
		}
	case string:
		for _, part := range strings.FieldsFunc(t, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
			if n, err := strconv.Atoi(strings.TrimSpace(part)); err == nil && n > 0 {
				out = append(out, n)
			}
		}
	default:
		if n, ok := toInt(v); ok && n > 0 {
			out = append(out, n)
		}
	}

	return out
}

// clamp keeps a numeric argument inside sane bounds.
func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}

	if v > hi {
		return hi
	}

	return v
}
