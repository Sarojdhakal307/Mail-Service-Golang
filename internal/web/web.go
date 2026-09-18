package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed static
var static embed.FS

// PublicPaths lists the routes served here, which must not require an API key.
// Entries ending in "/" cover the whole subtree.
func PublicPaths() []string {
	return []string{"/", "/assets/", "/docs", "/docs/", "/admin", "/admin/"}
}

// Register serves the public site at /, the API docs at /docs/ and the admin UI at /admin/.
func Register(mux *http.ServeMux) {
	files, err := fs.Sub(static, "static")
	if err != nil {
		panic(err)
	}
	h := withSecurityHeaders(serveFiles(files))

	mux.Handle("GET /{$}", h)
	mux.Handle("GET /assets/", h)
	mux.Handle("GET /docs/", h)
	mux.Handle("GET /admin/", h)
}

// serveFiles serves embedded files. Directories are served only through their
// index.html, so nothing is ever listed.
func serveFiles(files fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" || strings.HasSuffix(r.URL.Path, "/") {
			name = path.Join(name, "index.html")
		}
		info, err := fs.Stat(files, name)
		if err != nil || info.IsDir() {
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(name, ".html") {
			w.Header().Set("Cache-Control", "no-cache")
		}
		http.ServeFileFS(w, r, files, name)
	})
}

func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; "+
				"connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}
