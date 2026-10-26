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

// Handler returns a handler serving the embedded frontend. Existing files are
// served as-is; any other path falls back to index.html so that client-side
// (hash) routing keeps working. API paths are left to the API router.
func Handler() http.Handler {
	files := http.FileServer(http.FS(assets))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			http.NotFound(w, r)
			return
		}

		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		if info, err := fs.Stat(assets, name); err == nil && !info.IsDir() {
			files.ServeHTTP(w, r)
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
