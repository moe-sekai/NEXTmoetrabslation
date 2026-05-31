package translator

import (
	"reflect"
	"strconv"
	"testing"
)

func TestBuildAndParseXMLRoundTrip(t *testing.T) {
	texts := []string{"こんにちは", "A & B", "<tag>", "三", ""}
	xml := buildXMLInput(texts)
	// Simulate an LLM echoing translations back in the expected format.
	resp := "<translations>"
	for i := range texts {
		resp += "<t id=\"" + strconv.Itoa(i+1) + "\">译" + strconv.Itoa(i+1) + "</t>"
	}
	resp += "</translations>"
	got := parseXMLTranslations(resp, len(texts))
	want := []string{"译1", "译2", "译3", "译4", "译5"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parse mismatch:\n got %v\nwant %v\nxml=%s", got, want, xml)
	}
}

func TestParseXMLStripsThinkAndHandlesGaps(t *testing.T) {
	content := `<think>reasoning here</think><t id="1">甲</t><t id="3">丙</t>`
	got := parseXMLTranslations(content, 3)
	want := []string{"甲", "", "丙"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestXMLEscapeUnescapeRoundTrip(t *testing.T) {
	in := `a & b < c > d`
	if out := xmlUnescape(xmlEscape(in)); out != in {
		t.Errorf("round trip failed: %q -> %q", in, out)
	}
}

func TestCollectPairBlanksWhenEqual(t *testing.T) {
	m := map[string]string{}
	collectPair(m, "同じ", "同じ") // jp==cn means untranslated
	if m["同じ"] != "" {
		t.Errorf("expected blank cn when jp==cn, got %q", m["同じ"])
	}
	collectPair(m, "日本語", "中文")
	if m["日本語"] != "中文" {
		t.Errorf("expected translated value, got %q", m["日本語"])
	}
}

func TestTraceMapDedup(t *testing.T) {
	tm := newTraceMap("name")
	tm.add("name", "テスト", 1)
	tm.add("name", "テスト", 1) // duplicate
	tm.add("name", "テスト", 2)
	tm.add("name", "", 3)    // empty jp ignored
	tm.add("name", "テスト", 0) // zero id ignored
	if got := tm["name"]["テスト"]; !reflect.DeepEqual(got, []string{"1", "2"}) {
		t.Errorf("trace dedup: got %v", got)
	}
}

func TestNormalizeStorySource(t *testing.T) {
	cases := map[string]string{
		"official_cn":        "official_cn",
		"official_cn_legacy": "official_cn",
		"cn":                 "official_cn",
		"llm":                "llm",
		"human":              "human",
		"jp_pending":         "jp_pending",
		"":                   "jp_pending",
	}
	for in, want := range cases {
		if got := normalizeStorySource(in); got != want {
			t.Errorf("normalizeStorySource(%q) = %q, want %q", in, got, want)
		}
	}
}
