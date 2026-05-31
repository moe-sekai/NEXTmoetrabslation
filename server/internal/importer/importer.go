// Package importer loads a translations directory (legacy layout: category JSON
// files plus an eventStory/ subdir) into the SQLite-backed stores. It is shared
// by backup restore; the migration command has its own copy with verification.
package importer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"moesekai/server/internal/legacy"
	"moesekai/server/internal/model"
	"moesekai/server/internal/store"
)

// Result summarizes an import.
type Result struct {
	Categories   int
	Entries      int
	EventStories int
	Warnings     []string
}

// ImportDir loads every category and event story under src into the stores,
// then fires a single change notification so public files regenerate. src must
// directly contain X.json/X.full.json and an eventStory/ subdir.
func ImportDir(src string, s *store.Store, es *store.EventStore) (Result, error) {
	var res Result

	for _, cat := range model.SupportedCategories {
		category, warnings, err := legacy.LoadCategory(src, cat)
		if err != nil {
			continue // category file absent in this backup; skip
		}
		res.Warnings = append(res.Warnings, warnings...)
		n, err := s.ImportCategory(cat, category)
		if err != nil {
			return res, fmt.Errorf("import %s: %w", cat, err)
		}
		res.Categories++
		res.Entries += n
	}

	storyFiles, _ := filepath.Glob(filepath.Join(src, "eventStory", "event_*.json"))
	for _, f := range storyFiles {
		eventID := eventIDFromPath(f)
		if eventID <= 0 {
			continue
		}
		story, err := legacy.LoadEventStory(f)
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("event %d: %v", eventID, err))
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
			return res, fmt.Errorf("import event %d: %w", eventID, err)
		}
		res.EventStories++
	}

	s.NotifyChange()
	return res, nil
}

func eventIDFromPath(path string) int {
	base := strings.TrimSuffix(filepath.Base(path), ".json")
	base = strings.TrimPrefix(base, "event_")
	n, err := strconv.Atoi(base)
	if err != nil {
		return -1
	}
	return n
}

// ValidateDir checks that src looks like a translations backup (has at least one
// recognizable category file) before a destructive restore.
func ValidateDir(src string) error {
	for _, cat := range model.SupportedCategories {
		if _, err := os.Stat(filepath.Join(src, cat+".json")); err == nil {
			return nil
		}
		if _, err := os.Stat(filepath.Join(src, cat+".full.json")); err == nil {
			return nil
		}
	}
	return fmt.Errorf("no recognizable translation files found in %s", src)
}

var _ = json.Marshal
