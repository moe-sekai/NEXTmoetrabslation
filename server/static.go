package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// staticHandler serves the statically-exported Next.js console as a single-page
// app, so the Go server is the only process: no nginx, no Node.js.
//
// Resolution order for a request path:
//  1. the exact file (e.g. /_next/static/chunks/abc.js)
//  2. its ".html" sibling (App Router exports /admin -> admin.html)
//  3. a directory index (/foo/ -> /foo/index.html)
//  4. index.html, so client-side routes and page refreshes resolve
//
// Content-hashed assets under /_next/static are immutable and cached for a year;
// HTML is served no-cache so console updates are picked up immediately.
func staticHandler(root string) http.HandlerFunc {
	root = filepath.Clean(root)
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		upath := r.URL.Path
		if !strings.HasPrefix(upath, "/") {
			upath = "/" + upath
		}
		clean := filepath.Clean(upath)

		// serve writes the file at rel (relative to root) if it exists and is a
		// regular file, applying cache headers. Returns true if it served.
		serve := func(rel string) bool {
			full := filepath.Join(root, filepath.FromSlash(rel))
			// Defense in depth against path traversal (mux already cleans paths).
			if full != root && !strings.HasPrefix(full, root+string(os.PathSeparator)) {
				return false
			}
			info, err := os.Stat(full)
			if err != nil || info.IsDir() {
				return false
			}
			if strings.HasPrefix(clean, "/_next/static/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else if strings.HasSuffix(full, ".html") {
				w.Header().Set("Cache-Control", "no-cache")
			}
			http.ServeFile(w, r, full)
			return true
		}

		switch {
		case clean != "/" && serve(clean): // 1) exact file
		case clean != "/" && serve(clean+".html"): // 2) .html sibling
		case strings.HasSuffix(upath, "/") && serve(clean+"/index.html"): // 3) dir index
		default: // 4) SPA fallback
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFile(w, r, filepath.Join(root, "index.html"))
		}
	}
}
