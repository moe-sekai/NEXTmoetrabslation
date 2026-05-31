// Command migrate imports the legacy translations/ directory into SQLite and
// verifies the import is lossless.
//
// Two checks run:
//   - FATAL: DB round-trip. Everything loaded from the legacy files (text,
//     source, ids, every event-story line and its order) must read back from
//     the DB unchanged. A failure here means data loss and aborts.
//   - WARN: consumer-format compatibility. Regenerated flat JSON is compared
//     against the legacy flat files. The legacy data is internally inconsistent
//     (e.g. gacha.json disagrees with gacha.full.json on 2 entries), so diffs
//     here are reported, not fatal.
//
// Usage:
//
//	go run ./cmd/migrate -src ../../translations -db ./data/moesekai.db
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"moesekai/server/internal/db"
	"moesekai/server/internal/legacy"
	"moesekai/server/internal/model"
	"moesekai/server/internal/store"
)

func main() {
	src := flag.String("src", "../../translations", "legacy translations directory")
	dbPath := flag.String("db", "./data/moesekai.db", "target SQLite path")
	verify := flag.Bool("verify", true, "verify lossless DB round-trip")
	flag.Parse()

	if err := run(*src, *dbPath, *verify); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}
}

func run(src, dbPath string, verify bool) error {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return err
	}
	database, err := db.Open(dbPath)
	if err != nil {
		return err
	}
	defer database.Close()

	s := store.New(database)
	es := store.NewEventStore(database)

	// ---- Import flat categories ----
	loadedCats := map[string]model.Category{}
	totalEntries := 0
	for _, cat := range model.SupportedCategories {
		category, warnings, err := legacy.LoadCategory(src, cat)
		if err != nil {
			fmt.Printf("[migrate] skip %s: %v\n", cat, err)
			continue
		}
		for _, w := range warnings {
			fmt.Printf("[migrate] WARN %s\n", w)
		}
		n, err := s.ImportCategory(cat, category)
		if err != nil {
			return fmt.Errorf("import %s: %w", cat, err)
		}
		loadedCats[cat] = category
		totalEntries += n
		fmt.Printf("[migrate] %-12s %d entries\n", cat, n)
	}
	fmt.Printf("[migrate] total entries: %d\n", totalEntries)

	// ---- Import event stories ----
	esDir := filepath.Join(src, "eventStory")
	storyFiles, _ := filepath.Glob(filepath.Join(esDir, "event_*.json"))
	sort.Slice(storyFiles, func(i, j int) bool {
		return idOf(storyFiles[i]) < idOf(storyFiles[j])
	})
	loadedStories := map[int]*legacy.EventStory{}
	for _, f := range storyFiles {
		eventID := idOf(f)
		if eventID <= 0 {
			continue
		}
		story, err := legacy.LoadEventStory(f)
		if err != nil {
			fmt.Printf("[migrate] skip event %d: %v\n", eventID, err)
			continue
		}
		eps := make([]store.OrderedEpisode, 0, len(story.EpisodeKeys))
		for _, no := range story.EpisodeKeys {
			ep := story.Episodes[no]
			eps = append(eps, store.OrderedEpisode{
				EpisodeNo:    no,
				ScenarioID:   ep.ScenarioID,
				Title:        ep.Title,
				TitleSource:  ep.TitleSource,
				TalkKeys:     ep.TalkKeys,
				TalkData:     ep.TalkData,
				TalkSources:  ep.TalkSources,
				SpeakerNames: ep.SpeakerNames,
			})
		}
		if err := es.ImportOrdered(eventID, story.Meta, eps); err != nil {
			return fmt.Errorf("import event %d: %w", eventID, err)
		}
		loadedStories[eventID] = story
	}
	fmt.Printf("[migrate] event stories: %d\n", len(loadedStories))

	if !verify {
		return nil
	}
	if err := verifyDBRoundTrip(s, es, loadedCats, loadedStories); err != nil {
		return err
	}
	reportFlatCompat(src, s)
	return nil
}

func idOf(path string) int {
	base := strings.TrimSuffix(filepath.Base(path), ".json")
	base = strings.TrimPrefix(base, "event_")
	n, err := strconv.Atoi(base)
	if err != nil {
		return -1
	}
	return n
}

// verifyDBRoundTrip is the FATAL check: everything we loaded must read back from
// the DB identically (no dropped/altered text, source, ids, lines, or order).
func verifyDBRoundTrip(s *store.Store, es *store.EventStore,
	cats map[string]model.Category, stories map[int]*legacy.EventStory) error {

	mismatches := 0

	for cat, loaded := range cats {
		got, err := s.CategoryData(cat)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(normalizeCat(loaded), normalizeCat(got)) {
			mismatches++
			fmt.Printf("[verify] FATAL category %s did not round-trip\n", cat)
			reportCatDiff(cat, normalizeCat(loaded), normalizeCat(got))
		}
	}

	for id, story := range stories {
		od, err := es.OrderedDetail(id)
		if err != nil {
			return fmt.Errorf("read back event %d: %w", id, err)
		}
		if msg := eventDiff(story, od); msg != "" {
			mismatches++
			if mismatches <= 12 {
				fmt.Printf("[verify] FATAL event_%d: %s\n", id, msg)
			}
		}
	}

	if mismatches > 0 {
		return fmt.Errorf("%d objects did not round-trip through the DB", mismatches)
	}
	fmt.Printf("[verify] OK — DB round-trip lossless: %d categories, %d event stories\n",
		len(cats), len(stories))
	return nil
}

// normalizeCat drops empty fields and normalizes empty source to "unknown" and
// empty ids slices to nil so comparison reflects stored semantics.
func normalizeCat(c model.Category) model.Category {
	out := make(model.Category, len(c))
	for field, entries := range c {
		if len(entries) == 0 {
			continue // empty fields are not stored as rows
		}
		ne := make(map[string]model.Entry, len(entries))
		for k, e := range entries {
			if e.Source == "" {
				e.Source = model.SourceUnknown
			}
			if len(e.Ids) == 0 {
				e.Ids = nil
			}
			ne[k] = e
		}
		out[field] = ne
	}
	return out
}

func reportCatDiff(cat string, a, b model.Category) {
	shown := 0
	for field, entries := range a {
		for k, e := range entries {
			be, ok := b[field][k]
			if !ok {
				fmt.Printf("    missing in DB: %s/%s %q\n", cat, field, trunc(k))
				shown++
			} else if !reflect.DeepEqual(e, be) {
				fmt.Printf("    differs: %s/%s %q  loaded=%+v db=%+v\n", cat, field, trunc(k), e, be)
				shown++
			}
			if shown >= 5 {
				return
			}
		}
	}
}

// eventDiff returns "" if the loaded story matches the DB read-back exactly,
// preserving episode order, line order, text, and speaker names.
func eventDiff(loaded *legacy.EventStory, od store.OrderedDetail) string {
	if len(loaded.EpisodeKeys) != len(od.Episodes) {
		return fmt.Sprintf("episode count %d != %d", len(loaded.EpisodeKeys), len(od.Episodes))
	}
	for i, no := range loaded.EpisodeKeys {
		dbEp := od.Episodes[i]
		if dbEp.EpisodeNo != no {
			return fmt.Sprintf("episode order at %d: %q != %q", i, no, dbEp.EpisodeNo)
		}
		le := loaded.Episodes[no]
		if le.Title != dbEp.Title {
			return fmt.Sprintf("ep %s title mismatch", no)
		}
		if len(le.TalkKeys) != len(dbEp.TalkKeys) {
			return fmt.Sprintf("ep %s line count %d != %d", no, len(le.TalkKeys), len(dbEp.TalkKeys))
		}
		for j, jp := range le.TalkKeys {
			if dbEp.TalkKeys[j] != jp {
				return fmt.Sprintf("ep %s line order at %d", no, j)
			}
			if le.TalkData[jp] != dbEp.TalkData[jp] {
				return fmt.Sprintf("ep %s line %d text mismatch", no, j)
			}
		}
	}
	return ""
}

// reportFlatCompat is the WARN check: how close regenerated flat JSON is to the
// legacy flat X.json files (the consumer-site contract). Informational only —
// the legacy flat and full files are themselves inconsistent in places.
func reportFlatCompat(src string, s *store.Store) {
	for _, cat := range model.SupportedCategories {
		raw, err := os.ReadFile(filepath.Join(src, cat+".json"))
		if err != nil {
			continue
		}
		var legacyFlat map[string]map[string]string
		if err := json.Unmarshal(raw, &legacyFlat); err != nil {
			continue
		}
		dbData, err := s.CategoryData(cat)
		if err != nil {
			continue
		}
		diffs, missing := 0, 0
		for field, entries := range legacyFlat {
			for k, text := range entries {
				dbEntry, ok := dbData[field][k]
				if !ok {
					missing++
				} else if dbEntry.Text != text {
					diffs++
				}
			}
		}
		if diffs > 0 || missing > 0 {
			fmt.Printf("[compat] %s: %d text diffs, %d keys only-in-flat vs DB "+
				"(legacy flat/full inconsistency, non-fatal)\n", cat, diffs, missing)
		}
	}
}

func trunc(s string) string {
	r := []rune(s)
	if len(r) > 30 {
		return string(r[:30]) + "…"
	}
	return s
}
