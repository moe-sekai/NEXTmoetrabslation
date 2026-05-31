package files

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"moesekai/server/internal/model"
	"moesekai/server/internal/store"
)

// Generator rebuilds the public, CDN-cacheable JSON files from the DB so the
// consumer site (pjsk.moe) keeps consuming the exact same formats as before.
type Generator struct {
	store      *store.Store
	eventStore *store.EventStore
	outDir     string // root containing translation/ and data/
}

func NewGenerator(s *store.Store, es *store.EventStore, outDir string) *Generator {
	return &Generator{store: s, eventStore: es, outDir: outDir}
}

// WithOutDir returns a copy of the generator that writes under a different root
// directory. Used by backup to materialize translations into scratch space.
func (g *Generator) WithOutDir(outDir string) *Generator {
	clone := *g
	clone.outDir = outDir
	return &clone
}

// MarshalIndentCompat marshals with two-space indent and HTML-escaping ON,
// matching the legacy category files which were written with the standard
// library's json.MarshalIndent (so &, <, > appear as &, <, >).
func MarshalIndentCompat(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

// marshalIndentNoEscape marshals with two-space indent and HTML-escaping OFF,
// matching the legacy event-story files which keep &, <, > literal. Byte-stable
// output avoids needless CDN cache churn on regeneration.
func marshalIndentNoEscape(v any) ([]byte, error) {
	compact, err := marshalNoEscape(v)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, compact, "", "  "); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// marshalNoEscape produces compact JSON without HTML escaping. The encoder
// appends a trailing newline, which is trimmed.
func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// CategoryFlatJSON returns the flat-format bytes for a category (X.json).
func (g *Generator) CategoryFlatJSON(category string) ([]byte, error) {
	flat, err := g.store.FlatData(category)
	if err != nil {
		return nil, err
	}
	return MarshalIndentCompat(flat)
}

// CategoryFullJSON returns the full-format bytes for a category (X.full.json).
func (g *Generator) CategoryFullJSON(category string) ([]byte, error) {
	cat, err := g.store.CategoryData(category)
	if err != nil {
		return nil, err
	}
	return MarshalIndentCompat(cat)
}

// EventStoryJSON returns the event_N.json bytes in the public, seed-compatible
// shape: meta + episodes (in order), each episode = {scenarioId, title,
// talkData} with talkData lines in story order. Source/speaker tracking lives
// in the DB and is exposed via the console API, not the public file.
func (g *Generator) EventStoryJSON(eventID int) ([]byte, error) {
	od, err := g.eventStore.OrderedDetail(eventID)
	if err != nil {
		return nil, err
	}
	root := newOrderedMap()

	meta := newOrderedMap()
	meta.set("source", od.Meta.Source)
	meta.set("version", od.Meta.Version)
	meta.set("last_updated", od.Meta.LastUpdated)
	root.set("meta", meta)

	episodes := newOrderedMap()
	for _, ep := range od.Episodes {
		epObj := newOrderedMap()
		epObj.set("scenarioId", ep.ScenarioID)
		epObj.set("title", ep.Title)
		talk := newOrderedMap()
		for _, jp := range ep.TalkKeys {
			talk.set(jp, ep.TalkData[jp])
		}
		epObj.set("talkData", talk)
		episodes.set(ep.EpisodeNo, epObj)
	}
	root.set("episodes", episodes)

	return marshalIndentNoEscape(root)
}

// WriteAll regenerates the full translation/ tree under outDir. Returns the
// number of files written.
func (g *Generator) WriteAll() (int, error) {
	transDir := filepath.Join(g.outDir, "translation")
	if err := os.MkdirAll(transDir, 0o755); err != nil {
		return 0, err
	}
	written := 0
	for _, cat := range model.SupportedCategories {
		flat, err := g.CategoryFlatJSON(cat)
		if err != nil {
			return written, fmt.Errorf("flat %s: %w", cat, err)
		}
		if err := writeAtomic(filepath.Join(transDir, cat+".json"), flat); err != nil {
			return written, err
		}
		written++
		full, err := g.CategoryFullJSON(cat)
		if err != nil {
			return written, fmt.Errorf("full %s: %w", cat, err)
		}
		if err := writeAtomic(filepath.Join(transDir, cat+".full.json"), full); err != nil {
			return written, err
		}
		written++
	}

	esDir := filepath.Join(transDir, "eventStory")
	if err := os.MkdirAll(esDir, 0o755); err != nil {
		return written, err
	}
	summaries, err := g.eventStore.List()
	if err != nil {
		return written, err
	}
	for _, sum := range summaries {
		b, err := g.EventStoryJSON(sum.EventID)
		if err != nil {
			return written, fmt.Errorf("event %d: %w", sum.EventID, err)
		}
		path := filepath.Join(esDir, "event_"+strconv.Itoa(sum.EventID)+".json")
		if err := writeAtomic(path, b); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}

func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
