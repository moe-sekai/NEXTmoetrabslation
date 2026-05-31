// Package api wires the HTTP routes for the console (JWT-authenticated, no-cache)
// API. Public, cacheable file serving lives in package filesvc and is mounted
// separately under /files/*.
package api

import (
	"encoding/json"
	"io"
	"net/http"

	"moesekai/server/internal/auth"
	"moesekai/server/internal/backup"
	"moesekai/server/internal/config"
	"moesekai/server/internal/sse"
	"moesekai/server/internal/store"
	"moesekai/server/internal/translator"
	"moesekai/server/internal/upstream"
)

// Server holds the dependencies shared by all console handlers.
type Server struct {
	store      *store.Store
	eventStore *store.EventStore
	auth       *auth.Auth
	cfg        *config.Config
	hub        *sse.Hub
	translator *translator.Translator
	upstream   *upstream.Watcher
	backup     *backup.Manager
}

func NewServer(s *store.Store, es *store.EventStore, a *auth.Auth, cfg *config.Config, hub *sse.Hub, tr *translator.Translator, up *upstream.Watcher, bk *backup.Manager) *Server {
	return &Server{store: s, eventStore: es, auth: a, cfg: cfg, hub: hub, translator: tr, upstream: up, backup: bk}
}

// broadcast sends an SSE event if a hub is configured (it may be nil in tests).
func (s *Server) broadcast(event string, data any) {
	if s.hub != nil {
		s.hub.Broadcast(event, data)
	}
}

// writeJSON sends v as JSON with no-store caching (console data is live).
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr sends a JSON error. msg comes from internal code, not user input.
func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// decodeBody decodes a JSON request body into dst, returning false (and writing
// a 400) on failure.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return false
	}
	return true
}

// decodeOptional decodes a JSON body into dst, tolerating an empty body.
func decodeOptional(r *http.Request, dst any) error {
	if r.Body == nil {
		return nil
	}
	err := json.NewDecoder(r.Body).Decode(dst)
	if err == io.EOF {
		return nil
	}
	return err
}

// currentUser returns the authenticated username, or "" if unauthenticated.
func currentUser(r *http.Request) string {
	if claims, ok := auth.FromContext(r.Context()); ok {
		return claims.Username
	}
	return ""
}
