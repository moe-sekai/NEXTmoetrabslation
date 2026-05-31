package store

// EventSyncState summarizes one event story for the CN-sync strategy.
type EventSyncState struct {
	EventID       int
	Source        string
	IsOfficialCN  bool
	IsLLM         bool
	PreserveLocal bool // has human/pinned title or line — never overwrite
}

// EventSyncStates returns the sync state of every stored event story plus the
// maximum event id present. Mirrors the legacy loadLocalEventStoryStates, but
// reads source tracking from the DB rather than a .full.json sidecar.
func (s *EventStore) EventSyncStates() (map[int]EventSyncState, int, error) {
	states := map[int]EventSyncState{}
	maxID := 0

	rows, err := s.db.Query(`SELECT event_id, source FROM event_stories`)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var st EventSyncState
		if err := rows.Scan(&st.EventID, &st.Source); err != nil {
			return nil, 0, err
		}
		st.IsOfficialCN = st.Source == "official_cn" || st.Source == "official_cn_legacy"
		st.IsLLM = st.Source == "llm"
		if st.Source == SourceHumanConst || st.Source == SourcePinnedConst {
			st.PreserveLocal = true
		}
		states[st.EventID] = st
		if st.EventID > maxID {
			maxID = st.EventID
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	// Any event with a human/pinned title or line must be preserved, even if
	// the story-level source still reads official_cn/llm.
	editRows, err := s.db.Query(`
		SELECT event_id FROM event_story_lines WHERE source IN ('human','pinned')
		UNION
		SELECT event_id FROM event_story_episodes WHERE title_source IN ('human','pinned')`)
	if err != nil {
		return nil, 0, err
	}
	defer editRows.Close()
	for editRows.Next() {
		var id int
		if err := editRows.Scan(&id); err != nil {
			return nil, 0, err
		}
		if st, ok := states[id]; ok {
			st.PreserveLocal = true
			states[id] = st
		}
	}
	return states, maxID, editRows.Err()
}

// Source-constant aliases (avoid importing model into many call sites).
const (
	SourceHumanConst  = "human"
	SourcePinnedConst = "pinned"
)
