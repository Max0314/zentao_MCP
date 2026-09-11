package bugindex

import (
	"strings"
	"testing"
)

// corpus mirrors the shape of the real ZenTao records: a bracketed device
// model and module in the title, structured 步骤/结果/期望 in the body.
func corpus() []Doc {
	raw := []struct {
		id                              int
		product                         int
		pname, title, body              string
		btype, status, resolution, date string
	}{
		{65526, 337, "终端政企",
			"【云南DCMG100】网关重启上报 Inform 报文，DeviceInfo.SpecVersion 节点参数异常，不符合规范",
			"操作步骤 全光网关首次上电，与管理平台建立正常连接；对网关执行重启/重新上电操作；抓包查看上报的 Inform 报文",
			"codeerror", "resolved", "fixed", "2026-05-12"},
		{9414, 53, "移动集团技术规范",
			"【天津】H2-2 inform上报节点信息不全",
			"[步骤] 网关恢复出厂设置。开启网关WAN口抓包。点击开始注册，检查首次上报的 BOOTSTRAP 报文信息。",
			"codeerror", "closed", "fixed", "2025-11-03"},
		{67278, 337, "终端政企",
			"【DCMG150】【IPV4配置】弹框遮罩未遮住页面全部，未遮住部分可操作",
			"[步骤] 进入IPV4配置页面，修改IP地址，点击保存，弹出二次确认框 [结果] 弹框遮罩未遮住页面全部",
			"codeerror", "resolved", "fixed", "2026-07-20"},
		{32851, 337, "终端政企",
			"【陕西移动】【DCMG150】组网平台设置wifi参数-5G没有生效",
			"[步骤] 省级组网平台设置所有的wifi参数后，web查看5G的账号密码没有生效",
			"codeerror", "closed", "fixed", "2025-08-14"},
		{70595, 161, "硬件测试",
			"【艾尔OTT项目】【DDT5370】【1.2.1_Power sequence电源顺序】【Fail】",
			"【测试小项】1.2.1_Power sequence电源顺序 【实际结论】Fail",
			"others", "active", "", "2026-09-01"},
		{63101, 53, "移动集团技术规范",
			"【吉林】【H5-9】LAN侧端口扫描存在多个端口开放",
			"[步骤] 使用zenmap进行扫描 [结果] 存在多个端口开放，其中17998为未知端口",
			"others", "closed", "bydesign", "2026-03-11"},
	}

	docs := make([]Doc, 0, len(raw))

	for _, r := range raw {
		docs = append(docs, Doc{
			ID: r.id, Product: r.product, ProductName: r.pname,
			Title: r.title, Body: r.body, Tags: ExtractTags(r.title),
			Type: r.btype, Status: r.status, Resolution: r.resolution,
			OpenedDate: r.date,
		})
	}

	return docs
}

func newTestIndex(t *testing.T) *Index {
	t.Helper()

	ix := New()
	ix.Replace(corpus())

	if !ix.Ready() {
		t.Fatal("index reports not ready after Replace")
	}

	return ix
}

func topID(t *testing.T, hits []Hit) int {
	t.Helper()

	if len(hits) == 0 {
		t.Fatal("no hits")
	}

	return hits[0].ID
}

// SQLite FTS5's trigram tokenizer returns nothing for two character Chinese
// terms, which is why this package tokenizes into bigrams instead. Defect
// vocabulary is full of them, so this is the load-bearing behaviour.
func TestTokenizeCoversTwoCharacterChineseTerms(t *testing.T) {
	for _, term := range []string{"重启", "告警", "速率", "丢包"} {
		toks := Tokenize(term)
		if len(toks) == 0 {
			t.Fatalf("Tokenize(%q) produced no terms", term)
		}

		if toks[0] != term {
			t.Fatalf("Tokenize(%q) = %v, want the bigram itself first", term, toks)
		}
	}
}

func TestTokenizeKeepsDeviceModelsIntact(t *testing.T) {
	toks := Tokenize("【云南DCMG100】网关重启")

	joined := strings.Join(toks, " ")
	if !strings.Contains(joined, "dcmg100") {
		t.Fatalf("device model lost, tokens = %v", toks)
	}

	if !strings.Contains(joined, "网关") || !strings.Contains(joined, "重启") {
		t.Fatalf("Chinese bigrams missing, tokens = %v", toks)
	}
}

func TestExtractTags(t *testing.T) {
	tags := ExtractTags("【陕西移动】【DCMG150】组网平台设置wifi参数")

	if len(tags) != 2 || tags[0] != "陕西移动" || tags[1] != "DCMG150" {
		t.Fatalf("tags = %v, want [陕西移动 DCMG150]", tags)
	}

	if got := ExtractTags("没有方括号的标题"); got != nil {
		t.Fatalf("expected nil for an untagged title, got %v", got)
	}
}

func TestPlainTextStripsHTML(t *testing.T) {
	got := PlainText("<p>进入页面</p><p>点击保存&nbsp;&amp;&nbsp;观察</p>")

	if strings.Contains(got, "<") || strings.Contains(got, "&nbsp;") {
		t.Fatalf("markup survived: %q", got)
	}

	if !strings.Contains(got, "点击保存") {
		t.Fatalf("text lost: %q", got)
	}
}

// The field scenario: a symptom written in the engineer's own words should
// surface the historical defect plus its cross-product analogues.
func TestSearchFindsHistoricalDefectFromSymptomWording(t *testing.T) {
	ix := newTestIndex(t)

	hits, stats := ix.Search(Query{Text: "网关重启后上报报文参数异常不符合规范", PreferFixed: true})

	if stats.Terms == 0 {
		t.Fatal("query produced no terms")
	}

	if got := topID(t, hits); got != 65526 {
		t.Fatalf("top hit = %d, want 65526", got)
	}

	found := false

	for _, h := range hits {
		if h.ID == 9414 {
			found = true
		}
	}

	if !found {
		t.Fatalf("expected the cross-product analogue 9414 among hits, got %v", ids(hits))
	}
}

func TestSearchBoostsExactDeviceModelMatch(t *testing.T) {
	ix := newTestIndex(t)

	hits, _ := ix.Search(Query{Text: "DCMG150 弹框遮罩未遮住页面全部", PreferFixed: true})

	if got := topID(t, hits); got != 67278 {
		t.Fatalf("top hit = %d, want 67278; hits = %v", got, ids(hits))
	}

	if len(hits[0].MatchedKeys) == 0 {
		t.Fatalf("expected dcmg150 reported in matchedOn, got %v", hits[0].MatchedKeys)
	}

	// The other DCMG150 record should rank too, but below the exact symptom.
	if len(hits) < 2 || hits[1].Score >= hits[0].Score {
		t.Fatalf("expected a clear margin for the exact match, got %v", hits)
	}
}

func TestSearchFiltersByProductAndResolution(t *testing.T) {
	ix := newTestIndex(t)

	hits, _ := ix.Search(Query{Text: "端口开放扫描", Product: 53})
	for _, h := range hits {
		if h.Product != 53 {
			t.Fatalf("product filter leaked product %d", h.Product)
		}
	}

	fixed, _ := ix.Search(Query{Text: "端口开放扫描", FixedOnly: true})
	for _, h := range fixed {
		if h.Resolution != "fixed" {
			t.Fatalf("fixedOnly returned resolution %q", h.Resolution)
		}
	}
}

func TestSearchFiltersByOpenedDate(t *testing.T) {
	ix := newTestIndex(t)

	hits, _ := ix.Search(Query{Text: "网关", OpenedAfter: "2026-01-01"})
	for _, h := range hits {
		if h.OpenedDate < "2026-01-01" {
			t.Fatalf("date filter leaked %s (#%d)", h.OpenedDate, h.ID)
		}
	}
}

func TestSearchRespectsLimit(t *testing.T) {
	ix := newTestIndex(t)

	hits, stats := ix.Search(Query{Text: "网关 端口 电源 配置", Limit: 2})
	if len(hits) > 2 {
		t.Fatalf("returned %d hits for limit 2", len(hits))
	}

	if stats.Returned != len(hits) {
		t.Fatalf("stats.Returned = %d, len(hits) = %d", stats.Returned, len(hits))
	}
}

func TestSearchOnEmptyIndexAndEmptyQuery(t *testing.T) {
	empty := New()

	if empty.Ready() {
		t.Fatal("a fresh index must not report ready")
	}

	if hits, _ := empty.Search(Query{Text: "任何现象"}); len(hits) != 0 {
		t.Fatalf("empty index returned %d hits", len(hits))
	}

	ix := newTestIndex(t)

	if hits, stats := ix.Search(Query{Text: "   "}); len(hits) != 0 || stats.Terms != 0 {
		t.Fatalf("blank query returned %d hits / %d terms", len(hits), stats.Terms)
	}
}

func TestReplaceSwapsGenerationAndTracksMaxID(t *testing.T) {
	ix := newTestIndex(t)

	if ix.Len() != len(corpus()) {
		t.Fatalf("Len = %d, want %d", ix.Len(), len(corpus()))
	}

	if ix.MaxID() != 70595 {
		t.Fatalf("MaxID = %d, want 70595", ix.MaxID())
	}

	if ix.BuiltAt().IsZero() {
		t.Fatal("BuiltAt not recorded")
	}

	ix.Replace([]Doc{{ID: 1, Title: "只剩一条"}})

	if ix.Len() != 1 || ix.MaxID() != 1 {
		t.Fatalf("after Replace: Len=%d MaxID=%d, want 1/1", ix.Len(), ix.MaxID())
	}

	if hits, _ := ix.Search(Query{Text: "网关重启"}); len(hits) != 0 {
		t.Fatalf("old generation still searchable: %v", ids(hits))
	}
}

func TestDocsReturnsACopy(t *testing.T) {
	ix := newTestIndex(t)

	docs := ix.Docs()
	if len(docs) == 0 {
		t.Fatal("Docs returned nothing")
	}

	docs[0].Title = "被外部改掉了"

	for _, d := range ix.Docs() {
		if d.Title == "被外部改掉了" {
			t.Fatal("Docs exposed the live slice")
		}
	}
}

func ids(hits []Hit) []int {
	out := make([]int, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.ID)
	}

	return out
}
