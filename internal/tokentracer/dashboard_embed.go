package tokentracer

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// dashboardAssets contains the production Vite output. Keeping the web UI in
// the Go binary makes the token tracer server self-contained when launched by
// the agent command.
//
// The directory is intentionally embedded rather than a single index file:
// Vite emits hashed JavaScript and CSS assets alongside index.html.
//
//go:embed dashboard/dist
var dashboardAssets embed.FS

func (s *Server) handler() http.Handler {
	dist, err := fs.Sub(dashboardAssets, "dashboard/dist")
	if err != nil {
		// The subdirectory is part of this package's compile-time embed contract.
		// Panicking here is preferable to silently serving an empty dashboard.
		panic("token tracer dashboard assets are not embedded: " + err.Error())
	}

	static := http.FileServer(http.FS(dist))
	mux := http.NewServeMux()
	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/events", s.handleEvents)
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.Handle("/", dashboardStaticHandler(static))
	return mux
}

func dashboardStaticHandler(static http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Do not expose arbitrary files from the embedded directory. The Vite
		// output only needs its entry point and the /assets tree at runtime.
		if r.URL.Path != "/" && r.URL.Path != "/index.html" && !strings.HasPrefix(r.URL.Path, "/assets/") {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			w.Header().Set("Cache-Control", "no-store")
		}
		if r.URL.Path == "/index.html" {
			// net/http's FileServer redirects an index.html request to the
			// directory root. Serve the explicit entry-point URL directly so
			// asset probes and health checks have a stable 200 response.
			request := r.Clone(r.Context())
			request.URL.Path = "/"
			static.ServeHTTP(w, request)
			return
		}
		static.ServeHTTP(w, r)
	})
}
