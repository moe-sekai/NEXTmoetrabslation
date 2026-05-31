package store

import (
	"database/sql"
	"encoding/json"
	"time"

	"moesekai/server/internal/model"
)

// txExecer is the subset of *sql.Tx used by transaction helpers.
type txExecer interface {
	Query(query string, args ...any) (*sql.Rows, error)
	Exec(query string, args ...any) (sql.Result, error)
}

// CNApplyField holds the CN-sync result for one field: jp -> cn text, plus
// jp -> []refID trace data. An empty cn means "known JP text, no official CN
// translation yet" (recorded so the editor can surface it).
type CNApplyField struct {
	Pairs map[string]string   // jp -> cn (cn may be "")
	Trace map[string][]string // jp -> ref ids
}

// ApplyCNCategory applies a CN-sync result to a category in one transaction,
// preserving the legacy precedence rules:
//   - pinned entries keep their text/source; only trace ids are merged
//   - existing non-empty entries keep their text; only trace ids are merged
//   - otherwise text is set with source=cn (or source=unknown when cn is empty)
//
// Returns the number of rows changed. Mirrors the legacy applyCategoryCNOnly.
func (s *Store) ApplyCNCategory(category string, fields map[string]CNApplyField) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	now := time.Now().Unix()
	updated := 0

	for field, data := range fields {
		for jp, cn := range data.Pairs {
			var curText, curSource, curIDs string
			err := tx.QueryRow(
				`SELECT cn_text, source, ids_json FROM entries
				 WHERE category=? AND field=? AND jp_key=?`,
				category, field, jp).Scan(&curText, &curSource, &curIDs)
			exists := err == nil
			if err != nil && err.Error() != "sql: no rows in result set" {
				return updated, err
			}

			var ids []string
			if curIDs != "" {
				_ = json.Unmarshal([]byte(curIDs), &ids)
			}
			newText, newSource := curText, curSource

			switch {
			case cn != "":
				if exists && curSource == model.SourcePinned {
					// keep text/source, only merge ids
				} else {
					newText, newSource = cn, model.SourceCN
				}
			default:
				if exists && curText != "" {
					// keep existing text, only merge ids
				} else {
					newText, newSource = "", model.SourceUnknown
				}
			}

			mergedIDs := mergeIDs(ids, data.Trace[jp])
			idsChanged := !idsEqual(ids, mergedIDs)
			contentChanged := !exists || curText != newText || curSource != newSource

			if !contentChanged && !idsChanged {
				continue
			}
			idsJSON := ""
			if len(mergedIDs) > 0 {
				b, _ := json.Marshal(mergedIDs)
				idsJSON = string(b)
			}
			if exists {
				if _, err := tx.Exec(
					`UPDATE entries SET cn_text=?, source=?, ids_json=?, updated_at=?, updated_by='cn-sync'
					 WHERE category=? AND field=? AND jp_key=?`,
					newText, newSource, idsJSON, now, category, field, jp); err != nil {
					return updated, err
				}
			} else {
				if _, err := tx.Exec(
					`INSERT INTO entries (category, field, jp_key, cn_text, source, ids_json, updated_at, updated_by)
					 VALUES (?, ?, ?, ?, ?, ?, ?, 'cn-sync')`,
					category, field, jp, newText, newSource, idsJSON, now); err != nil {
					return updated, err
				}
			}
			updated++
		}

		// Merge trace ids for entries not present in Pairs.
		for jp, refIDs := range data.Trace {
			if _, ok := data.Pairs[jp]; ok {
				continue
			}
			if len(refIDs) == 0 {
				continue
			}
			var curIDs string
			err := tx.QueryRow(
				`SELECT ids_json FROM entries WHERE category=? AND field=? AND jp_key=?`,
				category, field, jp).Scan(&curIDs)
			if err != nil {
				continue // entry not present; nothing to attach ids to
			}
			var ids []string
			if curIDs != "" {
				_ = json.Unmarshal([]byte(curIDs), &ids)
			}
			merged := mergeIDs(ids, refIDs)
			if idsEqual(ids, merged) {
				continue
			}
			b, _ := json.Marshal(merged)
			if _, err := tx.Exec(
				`UPDATE entries SET ids_json=? WHERE category=? AND field=? AND jp_key=?`,
				string(b), category, field, jp); err != nil {
				return updated, err
			}
			updated++
		}
	}

	if category == "mysekai" {
		n, err := syncMysekaiFlavorFromTagTx(tx)
		if err != nil {
			return updated, err
		}
		updated += n
	}

	if err := tx.Commit(); err != nil {
		return updated, err
	}
	if updated > 0 {
		s.NotifyChange()
	}
	return updated, nil
}

// ApplyAITranslations sets LLM translations for a field, skipping entries that
// already have a protected source (pinned/human/cn) or non-empty non-llm text.
// Returns (translated, skipped). Mirrors the legacy ManualAITranslate apply.
func (s *Store) ApplyAITranslations(category, field string, updates map[string]string) (int, int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	now := time.Now().Unix()
	translated, skipped := 0, 0
	for jp, cn := range updates {
		var curText, curSource string
		err := tx.QueryRow(
			`SELECT cn_text, source FROM entries WHERE category=? AND field=? AND jp_key=?`,
			category, field, jp).Scan(&curText, &curSource)
		exists := err == nil
		if exists {
			if curSource == model.SourcePinned || curSource == model.SourceHuman || curSource == model.SourceCN {
				skipped++
				continue
			}
			if curText != "" && curSource != model.SourceUnknown && curSource != model.SourceLLM {
				skipped++
				continue
			}
			if _, err := tx.Exec(
				`UPDATE entries SET cn_text=?, source=?, updated_at=?, updated_by='ai'
				 WHERE category=? AND field=? AND jp_key=?`,
				cn, model.SourceLLM, now, category, field, jp); err != nil {
				return translated, skipped, err
			}
		} else {
			if _, err := tx.Exec(
				`INSERT INTO entries (category, field, jp_key, cn_text, source, ids_json, updated_at, updated_by)
				 VALUES (?, ?, ?, ?, ?, '', ?, 'ai')`,
				category, field, jp, cn, model.SourceLLM, now); err != nil {
				return translated, skipped, err
			}
		}
		translated++
	}
	if err := tx.Commit(); err != nil {
		return translated, skipped, err
	}
	if translated > 0 {
		s.NotifyChange()
	}
	return translated, skipped, nil
}

// AICandidates returns jp keys in a field that need AI translation: source is
// unknown/llm and text is empty. Capped at limit (<=0 means no cap).
func (s *Store) AICandidates(category, field string, limit int) ([]string, int, error) {
	rows, err := s.db.Query(
		`SELECT jp_key, cn_text, source FROM entries WHERE category=? AND field=?`,
		category, field)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var candidates []string
	skipped := 0
	for rows.Next() {
		var jp, text, source string
		if err := rows.Scan(&jp, &text, &source); err != nil {
			return nil, 0, err
		}
		if source == model.SourceHuman || source == model.SourcePinned || source == model.SourceCN || text != "" {
			skipped++
			continue
		}
		candidates = append(candidates, jp)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates, skipped, nil
}

// syncMysekaiFlavorFromTagTx mirrors syncMysekaiFlavorTextFromTag: copy tag
// text/source onto the matching flavorText entry (same jp key).
func syncMysekaiFlavorFromTagTx(tx txExecer) (int, error) {
	rows, err := tx.Query(
		`SELECT t.jp_key, t.cn_text, t.source
		 FROM entries t
		 JOIN entries f ON f.category='mysekai' AND f.field='flavorText' AND f.jp_key=t.jp_key
		 WHERE t.category='mysekai' AND t.field='tag'
		   AND NOT (t.cn_text='' AND t.source='unknown')
		   AND (f.cn_text <> t.cn_text OR f.source <> t.source)`)
	if err != nil {
		return 0, err
	}
	type upd struct{ jp, text, source string }
	var updates []upd
	for rows.Next() {
		var u upd
		if err := rows.Scan(&u.jp, &u.text, &u.source); err != nil {
			rows.Close()
			return 0, err
		}
		updates = append(updates, u)
	}
	rows.Close()
	for _, u := range updates {
		if _, err := tx.Exec(
			`UPDATE entries SET cn_text=?, source=? WHERE category='mysekai' AND field='flavorText' AND jp_key=?`,
			u.text, u.source, u.jp); err != nil {
			return 0, err
		}
	}
	return len(updates), nil
}

func mergeIDs(existing, add []string) []string {
	if len(add) == 0 {
		return existing
	}
	seen := make(map[string]bool, len(existing))
	out := append([]string{}, existing...)
	for _, id := range existing {
		seen[id] = true
	}
	for _, id := range add {
		if !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}
	return out
}

func idsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
