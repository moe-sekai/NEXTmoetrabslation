// Package filesvc serves the public, CDN-cacheable translation files under
// /files/*. Content is generated from the DB and held in memory with strong
// ETags; regeneration is debounced and triggered by DB changes. Responses carry
// long max-age + stale-while-revalidate so a CDN can cache aggressively while
// the ETag lets clients revalidate cheaply.
package filesvc

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"moesekai/server/internal/files"
	"moesekai/server/internal/model"
	"moesekai/server/internal/store"
)

type asset struct {
	body        []byte
	etag        string
	contentType string
	modTime     time.Time
}

// Service holds generated assets in memory and serves them.
type Service struct {
	gen      *files.Generator
	store    *store.Store
	events   *store.EventStore
	maxAge   time.Duration
	swr      time.Duration
	debounce time.Duration

	mu     sync.RWMutex
	assets map[string]asset // path key e.g. "translation/cards.json"

	rebuildCh chan struct{}
}

func New(s *store.Store, es *store.EventStore, gen *files.Generator) *Service {
	return &Service{
		gen:       gen,
		store:     s,
		events:    es,
		maxAge:    5 * time.Minute,
		swr:       time.Hour,
		debounce:  2 * time.Second,
		assets:    map[string]asset{},
		rebuildCh: make(chan struct{}, 1),
	}
}

// Start builds assets once and launches the debounced rebuild loop.
func (svc *Service) Start() {
	svc.Rebuild()
	go svc.loop()
}

// Trigger schedules a debounced rebuild (safe to call from DB change hooks).
func (svc *Service) Trigger() {
	select {
	case svc.rebuildCh <- struct{}{}:
	default:
	}
}

func (svc *Service) loop() {
	var timer *time.Timer
	for range svc.rebuildCh {
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(svc.debounce, svc.Rebuild)
	}
}

// Rebuild regenerates all in-memory assets from the DB.
func (svc *Service) Rebuild() {
	next := map[string]asset{}
	now := time.Now()

	for _, cat := range model.SupportedCategories {
		if b, err := svc.gen.CategoryFlatJSON(cat); err == nil {
			next["translation/"+cat+".json"] = makeAsset(b, "application/json; charset=utf-8", now)
		}
		if b, err := svc.gen.CategoryFullJSON(cat); err == nil {
			next["translation/"+cat+".full.json"] = makeAsset(b, "application/json; charset=utf-8", now)
		}
	}
	if summaries, err := svc.events.List(); err == nil {
		for _, sum := range summaries {
			if b, err := svc.gen.EventStoryJSON(sum.EventID); err == nil {
				key := fmt.Sprintf("translation/eventStory/event_%d.json", sum.EventID)
				next[key] = makeAsset(b, "application/json; charset=utf-8", now)
			}
		}
	}

	svc.mu.Lock()
	// Preserve any externally-set assets (e.g. search index) not regenerated here.
	for k, v := range svc.assets {
		if _, ok := next[k]; !ok && !strings.HasPrefix(k, "translation/") {
			next[k] = v
		}
	}
	svc.assets = next
	svc.mu.Unlock()
}

// SetAsset stores a pre-rendered asset (e.g. data/search-index.json) under key.
func (svc *Service) SetAsset(key string, body []byte, contentType string) {
	svc.mu.Lock()
	svc.assets[key] = makeAsset(body, contentType, time.Now())
	svc.mu.Unlock()
}

func makeAsset(body []byte, contentType string, t time.Time) asset {
	sum := sha256.Sum256(body)
	return asset{
		body:        body,
		etag:        `"` + hex.EncodeToString(sum[:16]) + `"`,
		contentType: contentType,
		modTime:     t,
	}
}

// Handler serves GET /files/<path>. Path traversal is impossible because lookup
// is a map key match, not a filesystem path.
func (svc *Service) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		key := strings.TrimPrefix(r.URL.Path, "/files/")
		key = strings.TrimPrefix(key, "/")

		svc.mu.RLock()
		a, ok := svc.assets[key]
		svc.mu.RUnlock()
		if !ok {
			http.NotFound(w, r)
			return
		}

		h := w.Header()
		h.Set("Content-Type", a.contentType)
		h.Set("ETag", a.etag)
		h.Set("Cache-Control", fmt.Sprintf("public, max-age=%d, stale-while-revalidate=%d",
			int(svc.maxAge.Seconds()), int(svc.swr.Seconds())))
		h.Set("Access-Control-Allow-Origin", "*")

		if match := r.Header.Get("If-None-Match"); match != "" && etagMatch(match, a.etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.ServeContent(w, r, key, a.modTime, newReadSeeker(a.body))
	}
}

func etagMatch(ifNoneMatch, etag string) bool {
	for _, candidate := range strings.Split(ifNoneMatch, ",") {
		if strings.TrimSpace(candidate) == etag {
			return true
		}
	}
	return false
}
