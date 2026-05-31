package api

import (
	"fmt"
	"net/http"

	"moesekai/server/internal/model"
	"moesekai/server/internal/sse"
)

// handleCategories returns per-field counts for all categories.
//
// GET /api/categories
func (s *Server) handleCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := s.store.GetCategories()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if cats == nil {
		cats = []model.CategoryInfo{}
	}
	writeJSON(w, http.StatusOK, cats)
}

// handleEntries returns entries for a category/field with optional source filter.
//
// GET /api/entries?category=&field=&source=
func (s *Server) handleEntries(w http.ResponseWriter, r *http.Request) {
	category := r.URL.Query().Get("category")
	field := r.URL.Query().Get("field")
	source := r.URL.Query().Get("source")
	if category == "" || field == "" {
		writeErr(w, http.StatusBadRequest, "category and field required")
		return
	}
	if !model.IsValidCategory(category) {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("unsupported category: %s", category))
		return
	}
	entries, err := s.store.GetEntries(category, field, source)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if entries == nil {
		entries = []model.EntryWithKey{}
	}
	writeJSON(w, http.StatusOK, entries)
}

// handleUpdateEntry updates one translation entry.
//
// PUT /api/entry {category, field, key, text, source}
func (s *Server) handleUpdateEntry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Category string `json:"category"`
		Field    string `json:"field"`
		Key      string `json:"key"`
		Text     string `json:"text"`
		Source   string `json:"source"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if !model.IsValidCategory(req.Category) {
		writeErr(w, http.StatusBadRequest, "unsupported category")
		return
	}
	if req.Field == "" || req.Key == "" {
		writeErr(w, http.StatusBadRequest, "field and key required")
		return
	}
	status, err := s.store.UpdateEntry(req.Category, req.Field, req.Key, req.Text, req.Source, currentUser(r))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if status == "ok" {
		s.broadcast(sse.EventEntryUpdated, map[string]any{
			"category": req.Category,
			"field":    req.Field,
			"key":      req.Key,
			"text":     req.Text,
			"source":   req.Source,
			"user":     currentUser(r),
		})
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": status})
}
