package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func RegisterWeb(mux *http.ServeMux, directory string) {
	fileServer := http.FileServer(http.Dir(directory))
	mux.HandleFunc("GET /", func(writer http.ResponseWriter, request *http.Request) {
		path := strings.TrimPrefix(request.URL.Path, "/")
		if path == "" {
			path = "index.html"
		} else {
			path = filepath.ToSlash(filepath.Clean(path))
		}
		if path == ".." || strings.HasPrefix(path, "../") {
			http.NotFound(writer, request)
			return
		}
		fullPath := filepath.Join(directory, filepath.FromSlash(path))
		if info, err := os.Stat(fullPath); err == nil && !info.IsDir() {
			fileServer.ServeHTTP(writer, request)
			return
		}
		if filepath.Ext(path) != "" {
			http.NotFound(writer, request)
			return
		}
		http.ServeFile(writer, request, filepath.Join(directory, "index.html"))
	})
}
