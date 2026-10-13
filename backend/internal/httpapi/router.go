// Package httpapi wires the chi router, middleware and handlers.
package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"kppbwi.app/filemanager/internal/config"
)

// JSONBodyLimit is the maximum JSON request body on /api/v1 (SPEC §6.2
// PAYLOAD_TOO_LARGE).
const JSONBodyLimit int64 = 1 << 20

// APITimeout bounds non-streaming API handlers.
const APITimeout = 60 * time.Second

// Options configures the server.
type Options struct {
	Cfg         *config.Config
	Logger      *slog.Logger
	Version     string
	Assets      http.Handler   // SPA + static assets (webassets.Handler)
	ReadyChecks []ReadyChecker // readiness probes; FilesystemChecks is added automatically
}

// Server holds dependencies shared by handlers.
type Server struct {
	cfg         *config.Config
	logger      *slog.Logger
	version     string
	assets      http.Handler
	readyChecks []ReadyChecker
}

// New builds the router. The returned handler is safe for http.Server.
func New(o Options) http.Handler {
	s := &Server{
		cfg:     o.Cfg,
		logger:  o.Logger,
		version: o.Version,
		assets:  o.Assets,
	}
	s.readyChecks = append([]ReadyChecker{FilesystemChecks(o.Cfg.DataDir, o.Cfg.StorageRoot)}, o.ReadyChecks...)

	r := chi.NewRouter()
	r.Use(TrustedPeer(o.Cfg))
	r.Use(RequestID)
	r.Use(RealIP(o.Cfg))
	r.Use(AccessLog(o.Logger))
	r.Use(Recoverer(o.Logger))
	r.Use(SecurityHeaders)
	r.Use(middleware.Compress(5, "application/json", "text/html", "text/css", "application/javascript", "image/svg+xml"))

	r.Get("/healthz", s.handleHealthz)
	r.Get("/readyz", s.handleReadyz)

	r.Route("/api/v1", func(api chi.Router) {
		api.Use(middleware.Timeout(APITimeout))
		api.Use(MaxBytes(JSONBodyLimit))
		api.NotFound(func(w http.ResponseWriter, r *http.Request) {
			WriteError(w, r, CodeNotFound, 0, "endpoint not found", nil)
		})
		api.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
			WriteError(w, r, CodeMethodNotAllowed, 0, "method not allowed", nil)
		})
		s.mountAPI(api)
	})

	if s.assets != nil {
		r.NotFound(s.assets.ServeHTTP)
	}
	return r
}

// mountAPI registers feature routes. Step 1 has none; later steps add groups.
func (s *Server) mountAPI(api chi.Router) {
	api.Get("/version", func(w http.ResponseWriter, _ *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]any{"version": s.version, "appName": s.cfg.AppName})
	})
}
