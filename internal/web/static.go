package web

import (
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

func StaticHandler() http.Handler {
	dist, err := fs.Sub(workbenchAssets, "ui/dist")
	if err != nil {
		panic("web workbench assets are not embedded: " + err.Error())
	}
	return staticHandler(dist)
}

func staticHandler(dist fs.FS) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/api/") {
			writeJSONError(writer, http.StatusNotFound, "not_found", "API route not found", RequestID(request.Context()))
			return
		}
		name := strings.TrimPrefix(path.Clean(request.URL.Path), "/")
		if name == "." || name == "" || name == "index.html" {
			serveEmbeddedFile(writer, request, dist, "index.html", "no-store")
			return
		}
		if strings.HasPrefix(name, "assets/") {
			if _, err := fs.Stat(dist, name); err != nil {
				http.NotFound(writer, request)
				return
			}
			serveEmbeddedFile(writer, request, dist, name, "public, max-age=31536000, immutable")
			return
		}
		serveEmbeddedFile(writer, request, dist, "index.html", "no-store")
	})
}

func serveEmbeddedFile(writer http.ResponseWriter, request *http.Request, dist fs.FS, name, cacheControl string) {
	data, err := fs.ReadFile(dist, name)
	if err != nil {
		http.NotFound(writer, request)
		return
	}
	contentType := mime.TypeByExtension(path.Ext(name))
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	writer.Header().Set("Content-Type", contentType)
	writer.Header().Set("Cache-Control", cacheControl)
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(data)
}
