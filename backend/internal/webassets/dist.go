// Package webassets embeds the built frontend (web/dist copied into ./dist by
// `make build`) and serves it as a single-page application.
package webassets

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// placeholder is served when the frontend has not been built into the binary.
const placeholder = `<!doctype html>
<html lang="id"><head><meta charset="utf-8"><title>FileManager — scaffold</title>
<meta name="viewport" content="width=device-width, initial-scale=1"></head>
<body style="font-family:system-ui;margin:2rem;color:#0F172A">
<h1 style="font-size:1.25rem">FileManager — scaffold</h1>
<p>Frontend belum di-build ke dalam binary ini. Jalankan <code>make build</code>.</p>
</body></html>
`

// FS returns the embedded dist filesystem rooted at dist/.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}

// HasIndex reports whether a real frontend build is embedded.
func HasIndex() bool {
	_, err := fs.Stat(FS(), "index.html")
	return err == nil
}

// Handler serves static files from the embedded build with cache headers and
// falls back to index.html (or the placeholder) for SPA routes. Dotfiles are
// never served.
func Handler() http.Handler {
	root := FS()
	fileServer := http.FileServerFS(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		p := path.Clean("/" + r.URL.Path)
		for _, seg := range strings.Split(p, "/") {
			if strings.HasPrefix(seg, ".") {
				http.NotFound(w, r)
				return
			}
		}
		rel := strings.TrimPrefix(p, "/")
		if rel != "" && rel != "index.html" {
			if fi, err := fs.Stat(root, rel); err == nil && !fi.IsDir() {
				if strings.HasPrefix(rel, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "public, max-age=3600")
				}
				r2 := r.Clone(r.Context())
				r2.URL.Path = p
				fileServer.ServeHTTP(w, r2)
				return
			}
		}
		serveIndex(w, r, root)
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request, root fs.FS) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	b, err := fs.ReadFile(root, "index.html")
	if err != nil {
		b = []byte(placeholder)
	}
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(b)
}
