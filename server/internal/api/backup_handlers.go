package api

import "net/http"

// handleBackupStatus reports backup/restore state.
//
// GET /api/backup/status
func (s *Server) handleBackupStatus(w http.ResponseWriter, r *http.Request) {
	if s.backup == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	writeJSON(w, http.StatusOK, s.backup.Status())
}

// handleBackupPush triggers an immediate backup to all enabled targets.
//
// POST /api/backup/push
func (s *Server) handleBackupPush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.backup == nil {
		writeErr(w, http.StatusServiceUnavailable, "backup not configured")
		return
	}
	results, err := s.backup.BackupAll()
	if err != nil {
		// Partial success still returns the per-target breakdown.
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error":   err.Error(),
			"results": results,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "results": results})
}

// handleBackupRestore restores translations from a target ("s3" or "git").
//
// POST /api/backup/restore {target}
func (s *Server) handleBackupRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.backup == nil {
		writeErr(w, http.StatusServiceUnavailable, "backup not configured")
		return
	}
	var req struct {
		Target string `json:"target"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Target == "" {
		writeErr(w, http.StatusBadRequest, "target required (s3 or git)")
		return
	}
	res, err := s.backup.RestoreFrom(req.Target)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":       "ok",
		"categories":   res.Categories,
		"entries":      res.Entries,
		"eventStories": res.EventStories,
		"warnings":     res.Warnings,
	})
}
