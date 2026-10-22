// Package httpapi wires the chi router, middleware and handlers.
package httpapi

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"kppbwi.app/filemanager/internal/admin"
	"kppbwi.app/filemanager/internal/archive"
	"kppbwi.app/filemanager/internal/audit"
	"kppbwi.app/filemanager/internal/auth"
	"kppbwi.app/filemanager/internal/config"
	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/download"
	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/listing"
	"kppbwi.app/filemanager/internal/media"
	"kppbwi.app/filemanager/internal/meta"
	"kppbwi.app/filemanager/internal/notify"
	"kppbwi.app/filemanager/internal/quota"
	"kppbwi.app/filemanager/internal/remotedl"
	"kppbwi.app/filemanager/internal/search"
	"kppbwi.app/filemanager/internal/share"
	"kppbwi.app/filemanager/internal/sse"
	"kppbwi.app/filemanager/internal/tasks"
	"kppbwi.app/filemanager/internal/thumbs"
	"kppbwi.app/filemanager/internal/upload"
	"kppbwi.app/filemanager/internal/versions"
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
	Files       *files.Service      // file operations (Step 9a); nil disables the mutating routes
	Quota       *quota.Service      // quota accounting (Step 10a); nil = usage reported as zero
	Audit       *audit.Recorder     // security audit log for delete-permanent / trash empty; built from Store when nil
	Meta        *meta.Service       // favorites/pins/notes/tags (Step 10b); nil disables /meta
	Tasks       *tasks.Engine       // task engine (Step 11); nil keeps the synchronous 413 answers
	Events      *sse.Broker         // SSE broker (Step 11); nil disables GET /events
	Notify      *notify.Service     // in-app notifications (Step 11); nil disables /notifications
	Upload      *upload.Service     // tus uploads + precheck (Step 12a); nil disables /upload
	ZipStore    *download.ZipStore  // zip payloads (Step 12b); opened on Cfg.DataDir/tmp when nil
	Downloads   *download.Limiter   // concurrent download/zip slots per user; default 3
	Remote      *remotedl.Service   // remote downloads (Step 13); nil disables /remote-downloads
	Archives    *archive.Service    // compress/extract tasks (Step 14a); nil disables /archives
	Search      *search.Service     // name/content search + replace (Step 15); nil disables /search
	Versions    *versions.Store     // file versions (Step 15 minimal); nil disables /versions
	Share       *share.Service      // public links (Step 18a); nil disables /shares and /public/shares
	Admin       *admin.Service      // admin panel (Step 19a); nil disables /admin/*
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
	files       *files.Service
	quota       *quota.Service
	audit       *audit.Recorder
	meta        *meta.Service
	tasks       *tasks.Engine
	events      *sse.Broker
	notify      *notify.Service
	upload      *upload.Service
	zips        *download.ZipStore
	dlLimit     *download.Limiter
	remote      *remotedl.Service
	archives    *archive.Service
	search      *search.Service
	versions    *versions.Store
	shares      *share.Service
	admin       *admin.Service
}

// New builds the router. The returned handler is safe for http.Server.
func New(o Options) http.Handler {
	s := &Server{
		cfg:      o.Cfg,
		logger:   o.Logger,
		version:  o.Version,
		assets:   o.Assets,
		store:    o.Store,
		auth:     o.Auth,
		listing:  o.Listing,
		locks:    o.Locks,
		jails:    newJailCache(nil),
		media:    o.Media,
		thumbs:   o.Thumbs,
		signer:   o.Signer,
		files:    o.Files,
		quota:    o.Quota,
		audit:    o.Audit,
		meta:     o.Meta,
		tasks:    o.Tasks,
		events:   o.Events,
		notify:   o.Notify,
		upload:   o.Upload,
		zips:     o.ZipStore,
		dlLimit:  o.Downloads,
		remote:   o.Remote,
		archives: o.Archives,
		search:   o.Search,
		versions: o.Versions,
		shares:   o.Share,
		admin:    o.Admin,
	}
	s.warnings = o.Warnings
	if s.dlLimit == nil {
		s.dlLimit = download.NewLimiter(0)
	}
	if s.zips == nil && o.Cfg.DataDir != "" {
		zs, err := download.NewZipStore(o.Cfg.DataDir, nil)
		if err != nil {
			o.Logger.Warn("zip download disabled: cannot open DATA_DIR/tmp", "error", err.Error())
		} else {
			s.zips = zs
		}
	}
	if s.audit == nil && o.Store != nil {
		s.audit = audit.New(o.Store, o.Logger, nil)
	}
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
		api.Use(TimeoutExcept(APITimeout, EventsPath, TusPrefix, DownloadPath, RawPath, ZipPath, EntryContentPath, ContentSearchPath, PublicSharePrefix))
		api.Use(MaxBytes(JSONBodyLimit, TusPrefix, ContentPath, PublicShareTusMarker))
		api.NotFound(func(w http.ResponseWriter, r *http.Request) {
			WriteError(w, r, CodeNotFound, 0, "endpoint not found", nil)
		})
		api.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
			WriteError(w, r, CodeMethodNotAllowed, 0, "method not allowed", nil)
		})
		s.mountAPI(api)
	})

	if s.assets != nil {
		// public share pages (/s/<token>) never leak the link through Referer
		r.NotFound(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/s/") {
				w.Header().Set("Referrer-Policy", "no-referrer")
			}
			s.assets.ServeHTTP(w, r)
		})
	}
	return r
}

// mountAPI registers feature routes; each step adds its group here.
func (s *Server) mountAPI(api chi.Router) {
	if s.auth != nil && s.store != nil {
		s.mountAuth(api)
		s.mountFiles(api)
		s.mountFileOps(api)
		s.mountThumbs(api)
		s.mountTrash(api)
		s.mountQuota(api)
		s.mountMeta(api)
		s.mountMeActivity(api)
		s.mountTasks(api)
		s.mountEvents(api)
		s.mountUpload(api)
		s.mountDownload(api)
		s.mountRemoteDL(api)
		s.mountArchives(api)
		s.mountSearch(api)
		s.mountVersions(api)
		s.mountEditor(api)
		s.mountSubtitle(api)
		s.mountMediaPosition(api)
		s.mountShares(api)
		s.mountSharedWithMe(api)
		s.mountShareUpload(api)
		s.mountPublicShares(api)
		s.mountAdmin(api)
	}
	api.Get("/version", func(w http.ResponseWriter, _ *http.Request) {
		rt := s.cfg.Runtime()
		WriteJSON(w, http.StatusOK, map[string]any{"version": s.version, "appName": rt.AppName, "logoUrl": logoURL(rt.BrandingLogo), "defaultLanguage": rt.DefaultLanguage})
	})
}
