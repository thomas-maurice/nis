package http

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:ui/dist
var uiFS embed.FS

// GetUIFileSystem returns the embedded UI filesystem
func GetUIFileSystem() (http.FileSystem, error) {
	// Strip the "ui/dist" prefix from the embedded filesystem
	distFS, err := fs.Sub(uiFS, "ui/dist")
	if err != nil {
		return nil, err
	}
	return http.FS(distFS), nil
}

// SPAHandler handles single-page application routing
// It serves the index.html for all routes that don't match static assets
type SPAHandler struct {
	staticFS http.FileSystem
	indexPath string
}

// NewSPAHandler creates a new SPA handler
func NewSPAHandler(staticFS http.FileSystem) *SPAHandler {
	return &SPAHandler{
		staticFS: staticFS,
		indexPath: "index.html",
	}
}

func (h *SPAHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Try to serve the requested file
	path := r.URL.Path
	if path == "" || path == "/" {
		path = h.indexPath
	}

	f, err := h.staticFS.Open(path)
	if err != nil {
		// File not found, serve index.html for client-side routing
		indexFile, err := h.staticFS.Open(h.indexPath)
		if err != nil {
			http.Error(w, "index.html not found", http.StatusInternalServerError)
			return
		}
		defer func() { _ = indexFile.Close() }()

		stat, err := indexFile.Stat()
		if err != nil {
			http.Error(w, "Failed to stat index.html", http.StatusInternalServerError)
			return
		}

		http.ServeContent(w, r, h.indexPath, stat.ModTime(), indexFile)
		return
	}
	defer func() { _ = f.Close() }()

	// Check if it's a directory
	stat, err := f.Stat()
	if err != nil {
		http.Error(w, "Failed to stat file", http.StatusInternalServerError)
		return
	}

	if stat.IsDir() {
		// Serve index.html for directory requests
		indexFile, err := h.staticFS.Open(h.indexPath)
		if err != nil {
			http.Error(w, "index.html not found", http.StatusInternalServerError)
			return
		}
		defer func() { _ = indexFile.Close() }()

		indexStat, err := indexFile.Stat()
		if err != nil {
			http.Error(w, "Failed to stat index.html", http.StatusInternalServerError)
			return
		}

		http.ServeContent(w, r, h.indexPath, indexStat.ModTime(), indexFile)
		return
	}

	// Serve the file
	http.ServeContent(w, r, path, stat.ModTime(), f)
}
