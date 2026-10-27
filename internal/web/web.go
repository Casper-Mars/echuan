// Package web serves the embedded single-page frontend.
//
// The frontend sources live in web/ and are built by Task-14. Until then
// internal/web/dist holds a placeholder page so that the binary always embeds a
// usable index.html.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/Casper-Mars/echuan/internal/api"
)

//go:embed dist
var embedded embed.FS

// assets is the embedded dist directory rooted at "dist".
var assets = mustSub(embedded, "dist")

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		// The directory is embedded at compile time, so this cannot happen.
		panic("web: embedded dist missing: " + err.Error())
	}
	return sub
}

// isStaticRequest reports whether a path that does not exist in the embedded
// bundle should be treated as a request for a static file rather than as a
// client-side route.
//
// Two rules, both meaning "the browser asked for a file":
//   - the path lives in the assets/ directory (build artifacts), or
//   - the final path segment contains a ".", i.e. it carries a file extension.
//
// The SPA never routes to such paths, so a miss must surface as 404. Answering
// with index.html would return 200 text/html for a missing script or
// stylesheet (for example /assets/index-<oldhash>.js after a redeploy), which
// misleads browsers and troubleshooting tools and hides the real failure.
func isStaticRequest(name string) bool {
	if name == "assets" || strings.HasPrefix(name, "assets/") {
		return true
	}
	last := name
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		last = name[i+1:]
	}
	return strings.Contains(last, ".")
}

// Handler returns a handler serving the embedded frontend. Existing files are
// served as-is; extension-less paths outside assets/ fall back to index.html so
// that client-side (hash) routing keeps working. Missing static files are
// answered with 404 and API paths are left to the API router.
func Handler() http.Handler {
	files := http.FileServer(http.FS(assets))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// API paths never fall back to the SPA, including typo variants such
		// as "/healthz/" or "/HEALTHZ": a 200 HTML page would make a broken
		// health probe look healthy.
		if api.IsReservedPath(r.URL.Path) {
			http.NotFound(w, r)
			return
		}

		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		info, statErr := fs.Stat(assets, name)
		if statErr == nil && info.IsDir() {
			// Directories in the bundle have no listing of their own; never
			// answer them with the SPA.
			http.NotFound(w, r)
			return
		}
		if statErr == nil {
			files.ServeHTTP(w, r)
			return
		}

		// Missing path: only client-side routes get the SPA fallback.
		if isStaticRequest(name) {
			http.NotFound(w, r)
			return
		}

		index, err := fs.ReadFile(assets, "index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(index)
	})
}
