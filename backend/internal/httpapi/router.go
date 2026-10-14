// Package httpapi wires the chi router, middleware and handlers.
package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"kppbwi.app/filemanager/internal/auth"
	"kppbwi.app/filemanager/internal/config"
	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/download"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/listing"
	"kppbwi.app/filemanager/internal/media"
	"kppbwi.app/filemanager/internal/thumbs"
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
	Assets      http.Handler        // SPA + static assets (webassets.Handler)
	ReadyChecks []ReadyChecker      // readiness probes; FilesystemChecks is added automatically
	Store       db.Repository       // database repositories (nil only in Step 1-style tests)
	Auth        *auth.Service       // authentication service; nil disables the auth routes
	Listing     *listing.Service    // directory listing; built from Cfg when nil
	Locks       *fsjail.LockManager // per-user path locks shared with tasks; created when nil
	Media       *media.Service      // media metadata cache; nil disables media columns/probes
	Thumbs      *thumbs.Service     // thumbnail pipeline; nil disables GET /files/thumb
	Signer      *download.Signer    // signed URLs; built from Cfg.AppSecret when nil
	Warnings    func() []string     // non-fatal readiness warnings (missing optional tools)
}

// Server holds dependencies shared by handlers.
type Server struct {
	cfg         *config.Config
	logger      *slog.Logger
	version     string
	assets      http.Handler
	readyChecks []ReadyChecker
	store       db.Repository
	auth        *auth.Service
	listing     *listing.Service
	locks       *fsjail.LockManager
	jails       *jailCache
	media       *media.Service
	thumbs      *thumbs.Service
	signer      *download.Signer
	warnings    func() []string
	thumbLimit  thumbLimiter
}

// New builds the router. The returned handler is safe for http.Server.
func New(o Options) http.Handler {
	s := &Server{
		cfg:     o.Cfg,
		logger:  o.Logger,
		version: o.Version,
		assets:  o.Assets,
		store:   o.Store,
		auth:    o.Auth,
		listing: o.Listing,
		locks:   o.Locks,
		jails:   newJailCache(nil),
		media:   o.Media,
		thumbs:  o.Thumbs,
		signer:  o.Signer,
	}
	s.warnings = o.Warnings
	if s.signer == nil && len(o.Cfg.AppSecret) > 0 {
		s.signer = download.NewSigner(o.Cfg.AppSecret, nil)
	}
	if s.listing == nil {
		s.listing = listing.New(listing.Options{Secret: o.Cfg.AppSecret, PageSize: o.Cfg.ListingPageSize})
	}
	if s.locks == nil {
		s.locks = fsjail.NewLockManager()
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

// mountAPI registers feature routes; each step adds its group here.
func (s *Server) mountAPI(api chi.Router) {
	if s.auth != nil && s.store != nil {
		s.mountAuth(api)
		s.mountFiles(api)
		s.mountThumbs(api)
	}
	api.Get("/version", func(w http.ResponseWriter, _ *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]any{"version": s.version, "appName": s.cfg.AppName})
	})
}
