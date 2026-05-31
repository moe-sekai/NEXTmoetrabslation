package api

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	"moesekai/server/internal/model"
	"moesekai/server/internal/sse"
)

// handleEventStories lists event story summaries.
//
// GET /api/event-stories
func (s *Server) handleEventStories(w http.ResponseWriter, r *http.Request) {
	stories, err := s.eventStore.List()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if stories == nil {
		stories = []model.EventStorySummary{}
	}
	writeJSON(w, http.StatusOK, stories)
}

// handleEventStory returns one event story's full detail.
//
// GET /api/event-story?eventId=123
func (s *Server) handleEventStory(w http.ResponseWriter, r *http.Request) {
	id, ok := parseEventID(w, r.URL.Query().Get("eventId"))
	if !ok {
		return
	}
	detail, err := s.eventStore.Detail(id)
	if err == sql.ErrNoRows {
		writeErr(w, http.StatusNotFound, "event story not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// handleUpdateEventStory updates one talk line or episode title.
//
// PUT /api/event-story/update {eventId, episodeNo, jpKey, cnText, source, entryType}
func (s *Server) handleUpdateEventStory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		EventID   int    `json:"eventId"`
		EpisodeNo string `json:"episodeNo"`
		JpKey     string `json:"jpKey"`
		CnText    string `json:"cnText"`
		Source    string `json:"source"`
		EntryType string `json:"entryType"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.EventID <= 0 || req.EpisodeNo == "" {
		writeErr(w, http.StatusBadRequest, "eventId and episodeNo required")
		return
	}
	if req.EntryType != "title" && req.JpKey == "" {
		writeErr(w, http.StatusBadRequest, "jpKey required for talk entries")
		return
	}
	if req.Source == "" {
		req.Source = "human"
	}
	err := s.eventStore.UpdateLine(req.EventID, req.EpisodeNo, req.JpKey, req.CnText, req.Source, req.EntryType)
	if err == sql.ErrNoRows {
		writeErr(w, http.StatusNotFound, "target line not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.store.NotifyChange() // event story files are regenerated too
	s.broadcast(sse.EventStoryUpdated, map[string]any{
		"eventId":   req.EventID,
		"episodeNo": req.EpisodeNo,
		"jpKey":     req.JpKey,
		"cnText":    req.CnText,
		"source":    req.Source,
		"entryType": req.EntryType,
		"user":      currentUser(r),
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handlePromoteEventStoryHuman marks an entire story as human-edited.
//
// POST /api/event-story/promote-human {eventId}
func (s *Server) handlePromoteEventStoryHuman(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id, ok := decodeEventID(w, r)
	if !ok {
		return
	}
	if err := s.eventStore.PromoteHuman(id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.store.NotifyChange()
	s.broadcast(sse.EventStoryUpdated, map[string]any{
		"eventId": id,
		"promote": "human",
		"user":    currentUser(r),
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// parseEventID parses and validates an event id from a string.
func parseEventID(w http.ResponseWriter, raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		writeErr(w, http.StatusBadRequest, "eventId required")
		return 0, false
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid eventId")
		return 0, false
	}
	return id, true
}

// decodeEventID reads {eventId} from a JSON body.
func decodeEventID(w http.ResponseWriter, r *http.Request) (int, bool) {
	var req struct {
		EventID int `json:"eventId"`
	}
	if !decodeBody(w, r, &req) {
		return 0, false
	}
	if req.EventID <= 0 {
		writeErr(w, http.StatusBadRequest, "eventId required")
		return 0, false
	}
	return req.EventID, true
}
