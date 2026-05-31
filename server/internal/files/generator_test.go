package files

import (
	"encoding/json"
	"testing"

	"moesekai/server/internal/db"
	"moesekai/server/internal/model"
	"moesekai/server/internal/store"
)

// TestCategoryRoundTrip verifies an imported category reads back from the DB
// with identical text, source, and ids, and that the generated flat JSON
// matches the input text.
func TestCategoryRoundTrip(t *testing.T) {
	database, err := db.Open(t.TempDir() + "/roundtrip.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	s := store.New(database)
	es := store.NewEventStore(database)

	cat := model.Category{
		"prefix": {
			"こんにちは":     {Text: "你好", Source: model.SourceCN, Ids: []string{"1", "2"}},
			"A & B < C": {Text: "甲 & 乙 < 丙", Source: model.SourceHuman},
		},
	}
	if _, err := s.ImportCategory("cards", cat); err != nil {
		t.Fatal(err)
	}

	got, err := s.CategoryData("cards")
	if err != nil {
		t.Fatal(err)
	}
	for field, entries := range cat {
		for k, want := range entries {
			g := got[field][k]
			if g.Text != want.Text || g.Source != want.Source {
				t.Errorf("%s/%s: got %+v want %+v", field, k, g, want)
			}
			if len(g.Ids) != len(want.Ids) {
				t.Errorf("%s/%s ids: got %v want %v", field, k, g.Ids, want.Ids)
			}
		}
	}

	// Flat JSON keeps HTML-escaping ON (legacy category-file convention).
	g := NewGenerator(s, es, "")
	flat, err := g.CategoryFlatJSON("cards")
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]map[string]string
	if err := json.Unmarshal(flat, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["prefix"]["A & B < C"] != "甲 & 乙 < 丙" {
		t.Errorf("flat text mismatch: %q", parsed["prefix"]["A & B < C"])
	}
}

// TestEventStoryOrderPreserved verifies that talk-line order survives the DB
// round-trip and that the public event JSON keeps lines in story order with
// literal &, <, > (event-file convention).
func TestEventStoryOrderPreserved(t *testing.T) {
	database, err := db.Open(t.TempDir() + "/eventorder.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	es := store.NewEventStore(database)
	meta := model.EventStoryMeta{Source: "official_cn", Version: "1.0", LastUpdated: 100}
	// Keys deliberately NOT alphabetical, to catch map-sorting regressions.
	keys := []string{"zebra", "apple", "mango & lime"}
	ep := store.OrderedEpisode{
		EpisodeNo:  "1",
		ScenarioID: "s1",
		Title:      "T",
		TalkKeys:   keys,
		TalkData:   map[string]string{"zebra": "z", "apple": "a", "mango & lime": "m"},
	}
	if err := es.ImportOrdered(7, meta, []store.OrderedEpisode{ep}); err != nil {
		t.Fatal(err)
	}

	od, err := es.OrderedDetail(7)
	if err != nil {
		t.Fatal(err)
	}
	if len(od.Episodes) != 1 {
		t.Fatalf("episodes: got %d want 1", len(od.Episodes))
	}
	got := od.Episodes[0].TalkKeys
	for i, k := range keys {
		if got[i] != k {
			t.Errorf("talk order at %d: got %q want %q", i, got[i], k)
		}
	}

	g := NewGenerator(store.New(database), es, "")
	b, err := g.EventStoryJSON(7)
	if err != nil {
		t.Fatal(err)
	}
	// Literal ampersand, not &.
	if !containsLiteral(b, "mango & lime") {
		t.Errorf("event JSON should keep literal &: %s", b)
	}
}

func containsLiteral(haystack []byte, needle string) bool {
	h, n := string(haystack), needle
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return true
		}
	}
	return false
}
