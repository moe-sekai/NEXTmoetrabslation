// Package sse provides a Server-Sent Events hub for pushing realtime updates to
// the console: translation edits, sync/translate progress, event-story changes,
// and backup status. SSE is one-directional (server -> client), proxy- and
// CDN-friendly, and far simpler than WebSockets for this read-mostly use case.
package sse

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Event types broadcast to clients. Kept as constants so the frontend and
// backend agree on the wire vocabulary.
const (
	EventEntryUpdated      = "entry.updated"
	EventStoryUpdated      = "eventstory.updated"
	EventSyncProgress      = "sync.progress"
	EventTranslateProgress = "translate.progress"
	EventBackupStatus      = "backup.status"
	EventUpstreamStatus    = "upstream.status"
	EventPing              = "ping"
)

// Message is a single SSE payload. Data is marshaled to JSON.
type Message struct {
	Event string `json:"event"`
	Data  any    `json:"data"`
}

type client struct {
	id   uint64
	user string
	ch   chan Message
}

// Hub fans out messages to all connected clients.
type Hub struct {
	mu      sync.RWMutex
	clients map[uint64]*client
	nextID  atomic.Uint64
}

func NewHub() *Hub {
	return &Hub{clients: map[uint64]*client{}}
}

// Broadcast sends a message to every connected client. Slow clients that cannot
// keep up are dropped rather than blocking the broadcast.
func (h *Hub) Broadcast(event string, data any) {
	msg := Message{Event: event, Data: data}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, c := range h.clients {
		select {
		case c.ch <- msg:
		default:
			// Client buffer full; drop this message for them. The next full
			// page load reconciles state, so a dropped realtime hint is benign.
		}
	}
}

// ClientCount returns the number of connected clients (for status/debug).
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

func (h *Hub) add(user string) *client {
	c := &client{id: h.nextID.Add(1), user: user, ch: make(chan Message, 32)}
	h.mu.Lock()
	h.clients[c.id] = c
	h.mu.Unlock()
	return c
}

func (h *Hub) remove(id uint64) {
	h.mu.Lock()
	if c, ok := h.clients[id]; ok {
		delete(h.clients, id)
		close(c.ch)
	}
	h.mu.Unlock()
}

// Handler streams events to one client over SSE. The caller must wrap this with
// auth middleware (the username is read from the request context if present).
func (h *Hub) Handler(usernameFn func(*http.Request) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		h.setSSEHeaders(w)

		user := ""
		if usernameFn != nil {
			user = usernameFn(r)
		}
		c := h.add(user)
		defer h.remove(c.id)

		// Initial comment + retry hint so the browser reconnects quickly.
		fmt.Fprintf(w, ": connected\nretry: 3000\n\n")
		flusher.Flush()

		// Heartbeat keeps intermediaries from closing an idle connection.
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()

		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !writeEvent(w, Message{Event: EventPing, Data: time.Now().Unix()}) {
					return
				}
				flusher.Flush()
			case msg, ok := <-c.ch:
				if !ok {
					return
				}
				if !writeEvent(w, msg) {
					return
				}
				flusher.Flush()
			}
		}
	}
}

func (h *Hub) setSSEHeaders(w http.ResponseWriter) {
	hd := w.Header()
	hd.Set("Content-Type", "text/event-stream")
	hd.Set("Cache-Control", "no-store")
	hd.Set("Connection", "keep-alive")
	// Disable proxy buffering (nginx) so events flush immediately.
	hd.Set("X-Accel-Buffering", "no")
}

// writeEvent serializes one SSE frame. Returns false if the write failed.
func writeEvent(w http.ResponseWriter, msg Message) bool {
	payload, err := json.Marshal(msg.Data)
	if err != nil {
		return false
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", msg.Event, payload); err != nil {
		return false
	}
	return true
}
