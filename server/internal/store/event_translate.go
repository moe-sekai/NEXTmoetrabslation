package store

import (
	"database/sql"
	"time"
)

// EventTranslateTarget is an untranslated title or talk line needing LLM work.
type EventTranslateTarget struct {
	EpisodeNo string
	EntryType string // "title" | "talk"
	JP        string // for talk: the jp key; for title: the jp title text
}

// UntranslatedTargets returns titles/talk lines of an event story whose cn text
// is empty (i.e. JP-pending lines awaiting translation), in stored order.
func (s *EventStore) UntranslatedTargets(eventID int) ([]EventTranslateTarget, error) {
	var targets []EventTranslateTarget

	// Titles with empty text are not translatable (we only have the cn title
	// slot); JP-pending titles store the JP text in `title` with empty source.
	// We translate a title when its source is unknown/empty and text non-empty.
	epRows, err := s.db.Query(
		`SELECT episode_no, title, title_source FROM event_story_episodes
		 WHERE event_id=? ORDER BY position`, eventID)
	if err != nil {
		return nil, err
	}
	defer epRows.Close()
	type titleRow struct{ no, title, src string }
	var titles []titleRow
	for epRows.Next() {
		var tr titleRow
		if err := epRows.Scan(&tr.no, &tr.title, &tr.src); err != nil {
			return nil, err
		}
		titles = append(titles, tr)
	}
	if err := epRows.Err(); err != nil {
		return nil, err
	}
	for _, tr := range titles {
		if tr.title != "" && (tr.src == "" || tr.src == "unknown" || tr.src == "jp_pending") {
			targets = append(targets, EventTranslateTarget{EpisodeNo: tr.no, EntryType: "title", JP: tr.title})
		}
	}

	lineRows, err := s.db.Query(
		`SELECT episode_no, jp_key FROM event_story_lines
		 WHERE event_id=? AND cn_text='' ORDER BY episode_no, position`, eventID)
	if err != nil {
		return nil, err
	}
	defer lineRows.Close()
	for lineRows.Next() {
		var no, jp string
		if err := lineRows.Scan(&no, &jp); err != nil {
			return nil, err
		}
		targets = append(targets, EventTranslateTarget{EpisodeNo: no, EntryType: "talk", JP: jp})
	}
	return targets, lineRows.Err()
}

// ApplyEventTranslations writes LLM results for the given targets. For titles,
// the cn text replaces the title and title_source becomes the source; for talk
// lines, cn_text/source are set. Returns the number of rows changed.
func (s *EventStore) ApplyEventTranslations(eventID int, targets []EventTranslateTarget, cnByIndex []string, source string) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	changed := 0
	for i, tgt := range targets {
		if i >= len(cnByIndex) {
			break
		}
		cn := cnByIndex[i]
		if cn == "" {
			continue
		}
		if tgt.EntryType == "title" {
			if _, err := tx.Exec(
				`UPDATE event_story_episodes SET title=?, title_source=? WHERE event_id=? AND episode_no=?`,
				cn, source, eventID, tgt.EpisodeNo); err != nil {
				return changed, err
			}
		} else {
			if _, err := tx.Exec(
				`UPDATE event_story_lines SET cn_text=?, source=? WHERE event_id=? AND episode_no=? AND jp_key=?`,
				cn, source, eventID, tgt.EpisodeNo, tgt.JP); err != nil {
				return changed, err
			}
		}
		changed++
	}
	if _, err := tx.Exec(`UPDATE event_stories SET last_updated=? WHERE event_id=?`,
		time.Now().Unix(), eventID); err != nil {
		return changed, err
	}
	if err := tx.Commit(); err != nil {
		return changed, err
	}
	return changed, nil
}

// SetStorySource updates an event story's story-level source.
func (s *EventStore) SetStorySource(eventID int, source string) error {
	_, err := s.db.Exec(`UPDATE event_stories SET source=?, last_updated=? WHERE event_id=?`,
		source, time.Now().Unix(), eventID)
	return err
}

// ReorderEpisodeLines updates the stored positions of an episode's talk lines to
// match orderedKeys. Keys not present in orderedKeys keep their relative order
// after the listed ones. Existing translations are untouched.
func (s *EventStore) ReorderEpisodeLines(eventID int, episodeNo string, orderedKeys []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	pos := 0
	seen := map[string]bool{}
	for _, jp := range orderedKeys {
		res, err := tx.Exec(
			`UPDATE event_story_lines SET position=? WHERE event_id=? AND episode_no=? AND jp_key=?`,
			pos, eventID, episodeNo, jp)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			seen[jp] = true
			pos++
		}
	}
	// Append any lines not in orderedKeys, preserving their current order.
	rows, err := tx.Query(
		`SELECT jp_key FROM event_story_lines WHERE event_id=? AND episode_no=? ORDER BY position`,
		eventID, episodeNo)
	if err != nil {
		return err
	}
	var rest []string
	for rows.Next() {
		var jp string
		if err := rows.Scan(&jp); err != nil {
			rows.Close()
			return err
		}
		if !seen[jp] {
			rest = append(rest, jp)
		}
	}
	rows.Close()
	for _, jp := range rest {
		if _, err := tx.Exec(
			`UPDATE event_story_lines SET position=? WHERE event_id=? AND episode_no=? AND jp_key=?`,
			pos, eventID, episodeNo, jp); err != nil {
			return err
		}
		pos++
	}
	return tx.Commit()
}

// EpisodeTalkKeys returns an episode's talk jp keys (for reorder matching).
func (s *EventStore) EpisodeTalkKeys(eventID int, episodeNo string) (map[string]bool, error) {
	rows, err := s.db.Query(
		`SELECT jp_key FROM event_story_lines WHERE event_id=? AND episode_no=?`,
		eventID, episodeNo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var jp string
		if err := rows.Scan(&jp); err != nil {
			return nil, err
		}
		out[jp] = true
	}
	return out, rows.Err()
}

var _ = sql.ErrNoRows
