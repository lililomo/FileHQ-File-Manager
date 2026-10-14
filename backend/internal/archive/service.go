package archive

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"kppbwi.app/filemanager/internal/db"
	fmexec "kppbwi.app/filemanager/internal/exec"
	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/quota"
	"kppbwi.app/filemanager/internal/tasks"
)

// Task types (SPEC §7.1).
const (
	TaskCompress = "compress"
	TaskExtract  = "extract"
	// TaskZipEdit rewrites a zip to add or remove members (Step 14b).
	TaskZipEdit = "zip_edit"
)

// Activity actions recorded by this package.
const (
	ActCompress = "compress"
	ActExtract  = "extract"
	ActZipEdit  = "archive_edit"
)

// TmpPrefix names DATA_DIR/tmp/<prefix><task_id> working folders.
const TmpPrefix = "ar-"

// OrphanAge is how old an abandoned working folder must be before the
// hourly cleanup removes it.
const OrphanAge = 24 * time.Hour

// Limits of the API (SPEC §6.3).
const (
	MaxSources      = 1000
	MaxEntryFilters = 1000
	MaxPasswordLen  = 256
	// DefaultCLITimeout bounds one 7-Zip run (PROMPT: 1 h).
	DefaultCLITimeout = time.Hour
)

// Levels accepted by the API.
const (
	LevelStore  = "store"
	LevelFast   = "fast"
	LevelNormal = "normal"
	LevelMax    = "max"
)

// ParseLevel accepts "", store, fast, normal, max.
func ParseLevel(s string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", LevelNormal:
		return LevelNormal, true
	case LevelStore:
		return LevelStore, true
	case LevelFast:
		return LevelFast, true
	case LevelMax:
		return LevelMax, true
	}
	return "", false
}

// Dest modes of extraction.
const (
	DestHere = "here"
	DestNew  = "new"
)

// CompressPayload is the compress task payload (no secrets; HasPassword
// tells the UI that a retry needs the password again).
type CompressPayload struct {
	Paths        []string `json:"paths"`
	Format       Format   `json:"format"`
	Name         string   `json:"name"`
	Dest         string   `json:"dest"`
	Level        string   `json:"level"`
	HasPassword  bool     `json:"hasPassword,omitempty"`
	EncryptNames bool     `json:"encryptNames,omitempty"`
	DeleteSource bool     `json:"deleteSource,omitempty"`
	SplitMB      int      `json:"splitMb,omitempty"`
}

// CompressResult is the compress task result.
type CompressResult struct {
	Path    string   `json:"path"`
	Paths   []string `json:"paths,omitempty"` // split volumes
	Size    int64    `json:"size"`
	Files   int      `json:"files"`
	Dirs    int      `json:"dirs"`
	Skipped []Skip   `json:"skipped,omitempty"`
	Status  string   `json:"status"` // created|renamed|overwritten
	Deleted int      `json:"deletedSources,omitempty"`
}

// ExtractPayload is the extract task payload (no secrets).
type ExtractPayload struct {
	Archive     string   `json:"archive"`
	Dest        string   `json:"dest"` // here|new|<path>
	Encoding    string   `json:"encoding,omitempty"`
	Policy      string   `json:"policy,omitempty"`
	Entries     []string `json:"entries,omitempty"`
	HasPassword bool     `json:"hasPassword,omitempty"`
}

// ExtractResult is the extract task result.
type ExtractResult struct {
	Dest     string `json:"dest"`
	Format   Format `json:"format"`
	Files    int    `json:"files"`
	Dirs     int    `json:"dirs"`
	Links    int    `json:"links"`
	Bytes    int64  `json:"bytes"`
	Skipped  []Skip `json:"skipped,omitempty"`
	Warnings int    `json:"warnings"`
	Encoding string `json:"encoding,omitempty"`
	// Conflicts counts entries skipped/renamed/overwritten at the destination.
	Renamed     int `json:"renamed"`
	Overwritten int `json:"overwritten"`
	SkippedDest int `json:"skippedDest"`
}

// Skip records one entry the extractor refused (first 100 kept).
type Skip struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// MaxSkipsReported bounds the skip list in results and logs.
const MaxSkipsReported = 100

// Options configures the Service.
type Options struct {
	DataDir string
	// SevenZip is SEVENZIP_PATH ("" = 7z creation/iso disabled).
	SevenZip string
	Runner   *fmexec.Runner
	// MaxExtractSize / MaxEntries are ARCHIVE_MAX_EXTRACT_SIZE / ARCHIVE_MAX_ENTRIES.
	MaxExtractSize int64
	MaxEntries     int
	Ratio          float64
	RatioMinBytes  int64
	Repo           db.Repository
	Quota          *quota.Service
	Files          *files.Service
	Locks          *fsjail.LockManager
	Tasks          *tasks.Engine
	Invalidate     func(userID string, dir fsjail.VPath)
	OnChange       func(ctx context.Context, userID string, path fsjail.VPath, isDir bool)
	Logger         *slog.Logger
	Now            func() time.Time
	CLITimeout     time.Duration
	// ProgressStep is the byte interval between progress reports/ctx checks.
	ProgressStep int64
}

// Service validates and enqueues archive tasks and runs them.
type Service struct {
	o        Options
	logger   *slog.Logger
	now      func() time.Time
	step     int64
	index    *indexCache
	sessions *sessionStore
}

// New builds a Service and registers the runners when Tasks is set.
func New(o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Ratio <= 0 {
		o.Ratio = DefaultRatio
	}
	if o.RatioMinBytes <= 0 {
		o.RatioMinBytes = DefaultRatioMinBytes
	}
	if o.CLITimeout <= 0 {
		o.CLITimeout = DefaultCLITimeout
	}
	if o.ProgressStep <= 0 {
		o.ProgressStep = 256 << 10
	}
	if o.Runner == nil && o.DataDir != "" {
		o.Runner = fmexec.New(path.Join(o.DataDir, "tmp"))
	}
	s := &Service{o: o, logger: o.Logger, now: o.Now, step: o.ProgressStep, index: newIndexCache(o.Now), sessions: newSessionStore(o.Now)}
	if o.Tasks != nil {
		o.Tasks.Register(TaskCompress, tasks.Spec{Unit: tasks.UnitBytes, Run: s.runCompress, Link: compressLink})
		o.Tasks.Register(TaskExtract, tasks.Spec{Unit: tasks.UnitBytes, Run: s.runExtract, Link: extractLink})
		o.Tasks.Register(TaskZipEdit, tasks.Spec{Unit: tasks.UnitBytes, Run: s.runZipEdit, Link: zipEditLink})
	}
	return s
}

func compressLink(t tasks.Task) string {
	var p CompressPayload
	if json.Unmarshal(t.Payload, &p) != nil || p.Dest == "" {
		return ""
	}
	return "/files" + strings.TrimSuffix(p.Dest, "/")
}

func extractLink(t tasks.Task) string {
	var r ExtractResult
	if json.Unmarshal(t.Result, &r) == nil && r.Dest != "" {
		return "/files" + strings.TrimSuffix(r.Dest, "/")
	}
	return ""
}

// SevenZipAvailable reports whether the 7-Zip binary is configured and
// executable.
func (s *Service) SevenZipAvailable() bool {
	return s.o.SevenZip != "" && s.o.Runner != nil && s.o.Runner.Available(s.o.SevenZip)
}

// CapabilitiesResponse is the answer of GET /archives/capabilities.
type CapabilitiesResponse struct {
	Formats []Capability `json:"formats"`
	// SevenZip reports the CLI status; PasswordStdin is the Step 14a spike
	// result (7-Zip 23.01 reads the password from a stdin pipe).
	SevenZip       bool   `json:"sevenZip"`
	SevenZipPath   string `json:"sevenZipPath,omitempty"`
	PasswordStdin  bool   `json:"passwordStdin"`
	MaxExtractSize int64  `json:"maxExtractSize"`
	MaxEntries     int    `json:"maxEntries"`
}

// Capabilities applies the tool availability to the static matrix.
func (s *Service) Capabilities() CapabilitiesResponse {
	cli := s.SevenZipAvailable()
	out := CapabilitiesResponse{SevenZip: cli, PasswordStdin: true, MaxExtractSize: s.o.MaxExtractSize, MaxEntries: s.o.MaxEntries}
	if cli {
		out.SevenZipPath = s.o.SevenZip
	}
	for _, c := range capabilityTable {
		if c.Engine == EngineCLI && !cli {
			switch c.Format {
			case FormatSevenZip:
				c.CanWrite, c.PasswordWrite, c.EncryptNames, c.SplitVolume = false, false, false, false
				c.Reason = "dependency_missing"
			case FormatIso:
				c.CanRead = false
				c.Reason = "dependency_missing"
			}
		}
		out.Formats = append(out.Formats, c)
	}
	return out
}

// Capability returns the (availability-adjusted) entry of one format.
func (s *Service) Capability(f Format) (Capability, bool) {
	for _, c := range s.Capabilities().Formats {
		if c.Format == f {
			return c, true
		}
	}
	return Capability{}, false
}

// CompressRequest is the validated API body of POST /archives/compress.
type CompressRequest struct {
	UserID       string
	Paths        []string
	Format       string
	Name         string
	Dest         string
	Level        string
	Password     string
	EncryptNames bool
	DeleteSource bool
	SplitMB      int
}

// Compress validates the request against a jail and enqueues the task.
func (s *Service) Compress(ctx context.Context, j *fsjail.Jail, req CompressRequest) (tasks.Task, error) {
	if s.o.Tasks == nil {
		return tasks.Task{}, newErr(CodeInternal, "task engine unavailable", http.StatusServiceUnavailable, nil)
	}
	if len(req.Paths) == 0 || len(req.Paths) > MaxSources {
		return tasks.Task{}, validationErr("1..1000 paths per request", map[string]any{"fields": map[string]any{"paths": "1..1000"}})
	}
	format, ok := ParseFormat(req.Format)
	if !ok {
		return tasks.Task{}, validationErr("unknown archive format", map[string]any{"fields": map[string]any{"format": "zip, 7z, tar, tar.gz, tar.bz2, tar.xz, tar.zst, gz, bz2, xz, zst, jar, war"}})
	}
	capa, _ := s.Capability(format)
	if !capa.CanWrite {
		if capa.Reason == "dependency_missing" {
			return tasks.Task{}, newErr(CodeDependency, "7-Zip is not installed on the server", http.StatusNotImplemented, map[string]any{"format": string(format), "tool": "7z"})
		}
		return tasks.Task{}, unsupportedErr("format cannot be written", map[string]any{"format": string(format), "reason": capa.Reason})
	}
	level, ok := ParseLevel(req.Level)
	if !ok {
		return tasks.Task{}, validationErr("unknown level", map[string]any{"fields": map[string]any{"level": "store, fast, normal or max"}})
	}
	if len(req.Password) > MaxPasswordLen {
		return tasks.Task{}, validationErr("password too long", map[string]any{"fields": map[string]any{"password": "max 256"}})
	}
	if req.Password != "" && !capa.PasswordWrite {
		return tasks.Task{}, validationErr("this format cannot be password protected", map[string]any{"fields": map[string]any{"password": "unsupported"}, "format": string(format)})
	}
	if req.EncryptNames && (!capa.EncryptNames || req.Password == "") {
		return tasks.Task{}, validationErr("encrypted names need a 7z archive with a password", map[string]any{"fields": map[string]any{"encryptNames": "7z with password only"}})
	}
	if req.SplitMB < 0 || req.SplitMB > 1<<20 || (req.SplitMB > 0 && !capa.SplitVolume) {
		return tasks.Task{}, validationErr("split volumes are only available for 7z", map[string]any{"fields": map[string]any{"splitVolumeMb": "7z only, 1..1048576"}})
	}
	paths := make([]fsjail.VPath, 0, len(req.Paths))
	seen := map[fsjail.VPath]struct{}{}
	for i, raw := range req.Paths {
		vp, err := fsjail.Clean(raw)
		if err != nil || vp.IsRoot() {
			return tasks.Task{}, validationErr("source path is not valid", map[string]any{"fields": map[string]any{"paths": "invalid"}, "index": i})
		}
		if _, dup := seen[vp]; dup {
			continue
		}
		seen[vp] = struct{}{}
		info, err := j.Lstat(vp)
		if err != nil {
			return tasks.Task{}, newErr(CodeNotFound, "source not found", http.StatusNotFound, map[string]any{"path": string(vp), "index": i})
		}
		if capa.SingleFile && (!info.IsRegular() || len(req.Paths) > 1) {
			return tasks.Task{}, validationErr("this format compresses exactly one file", map[string]any{"fields": map[string]any{"paths": "one regular file"}, "format": string(format)})
		}
		paths = append(paths, vp)
	}
	dest := paths[0].Dir()
	if strings.TrimSpace(req.Dest) != "" {
		vp, err := fsjail.Clean(req.Dest)
		if err != nil {
			return tasks.Task{}, validationErr("destination path is not valid", map[string]any{"fields": map[string]any{"destPath": "invalid"}})
		}
		info, err := j.Lstat(vp)
		if err != nil || !info.IsDir {
			return tasks.Task{}, newErr(CodeNotFound, "destination folder not found", http.StatusNotFound, map[string]any{"path": string(vp)})
		}
		dest = vp
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = defaultArchiveName(paths, format, s.now())
	} else {
		if strings.ContainsAny(name, "/\\") {
			return tasks.Task{}, validationErr("archive name must not contain path separators", map[string]any{"fields": map[string]any{"name": "invalid"}})
		}
		if !strings.HasSuffix(strings.ToLower(name), "."+format.Extension()) {
			name += "." + format.Extension()
		}
		if _, err := fsjail.ValidateName(name); err != nil {
			return tasks.Task{}, validationErr("archive name is not valid", map[string]any{"fields": map[string]any{"name": "invalid"}})
		}
	}
	if s.o.Files != nil && s.o.Files.Blocked(name) {
		return tasks.Task{}, unsupportedErr("file extension is blocked", map[string]any{"reason": "blocked_extension", "name": name})
	}
	out, err := fsjail.Join(dest, name)
	if err != nil {
		return tasks.Task{}, validationErr("archive name is not valid", map[string]any{"fields": map[string]any{"name": "invalid"}})
	}
	for _, p := range paths {
		if out.HasPrefix(p) {
			return tasks.Task{}, validationErr("archive cannot be written inside a source folder", map[string]any{"reason": "into_itself", "path": string(p)})
		}
	}
	strPaths := make([]string, len(paths))
	for i, p := range paths {
		strPaths[i] = string(p)
	}
	payload := CompressPayload{Paths: strPaths, Format: format, Name: name, Dest: string(dest), Level: level, HasPassword: req.Password != "",
		EncryptNames: req.EncryptNames, DeleteSource: req.DeleteSource, SplitMB: req.SplitMB}
	var secrets map[string]string
	if req.Password != "" {
		secrets = map[string]string{"password": req.Password}
	}
	locks := append([]string{string(out)}, strPaths...)
	title := "Kompres " + compressTitle(len(paths), name)
	return s.o.Tasks.Enqueue(ctx, tasks.EnqueueRequest{UserID: req.UserID, Type: TaskCompress, Title: title, Payload: payload, Locks: locks, Secrets: secrets})
}

func compressTitle(n int, name string) string {
	if n == 1 {
		return "1 item ke " + name
	}
	return strings.Join([]string{itoa(n), "item ke", name}, " ")
}

func itoa(n int) string { return strconv.Itoa(n) }

// defaultArchiveName follows SPEC §8.11: one item → <name>.<ext>; several →
// <parent folder>.<ext> or Arsip-<date>.<ext> at the root.
func defaultArchiveName(paths []fsjail.VPath, format Format, now time.Time) string {
	ext := "." + format.Extension()
	if len(paths) == 1 {
		base := paths[0].Base()
		if format.IsSingle() {
			return base + ext
		}
		return base + ext
	}
	parent := paths[0].Dir()
	if parent.IsRoot() {
		return "Arsip-" + now.Format("2006-01-02") + ext
	}
	return parent.Base() + ext
}

// ExtractRequest is the validated API body of POST /archives/extract.
type ExtractRequest struct {
	UserID   string
	Archive  string
	Dest     string
	Password string
	Encoding string
	Policy   string
	Entries  []string
}

// Extract validates the request against a jail and enqueues the task.
func (s *Service) Extract(ctx context.Context, j *fsjail.Jail, req ExtractRequest) (tasks.Task, error) {
	if s.o.Tasks == nil {
		return tasks.Task{}, newErr(CodeInternal, "task engine unavailable", http.StatusServiceUnavailable, nil)
	}
	archive, err := fsjail.Clean(req.Archive)
	if err != nil || archive.IsRoot() {
		return tasks.Task{}, validationErr("archive path is not valid", map[string]any{"fields": map[string]any{"path": "invalid"}})
	}
	info, err := j.Lstat(archive)
	if err != nil {
		return tasks.Task{}, newErr(CodeNotFound, "archive not found", http.StatusNotFound, map[string]any{"path": string(archive)})
	}
	if !info.IsRegular() {
		return tasks.Task{}, unsupportedErr("not a regular file", map[string]any{"path": string(archive), "reason": "not_a_file"})
	}
	f, err := j.Open(archive)
	if err != nil {
		return tasks.Task{}, err
	}
	format, derr := Detect(archive.Base(), f, info.Size)
	_ = f.Close()
	if derr != nil {
		return tasks.Task{}, unsupportedErr("file is not a supported archive", map[string]any{"path": string(archive), "reason": "unknown_format"})
	}
	capa, _ := s.Capability(format)
	if !capa.CanRead {
		return tasks.Task{}, newErr(CodeDependency, "7-Zip is not installed on the server", http.StatusNotImplemented, map[string]any{"format": string(format), "tool": "7z"})
	}
	if len(req.Password) > MaxPasswordLen {
		return tasks.Task{}, validationErr("password too long", map[string]any{"fields": map[string]any{"password": "max 256"}})
	}
	enc, ok := ParseEncoding(req.Encoding)
	if !ok {
		return tasks.Task{}, validationErr("unknown name encoding", map[string]any{"fields": map[string]any{"encoding": "auto, utf-8, gbk, cp437, shift_jis, big5, windows-1252"}})
	}
	if _, ok := files.ParsePolicy(req.Policy); !ok {
		return tasks.Task{}, validationErr("unknown conflict policy", map[string]any{"fields": map[string]any{"onConflict": "fail, overwrite, skip or rename"}})
	}
	dest := strings.TrimSpace(req.Dest)
	switch dest {
	case "", DestNew:
		dest = DestNew
	case DestHere:
	default:
		vp, err := fsjail.Clean(dest)
		if err != nil {
			return tasks.Task{}, validationErr("destination path is not valid", map[string]any{"fields": map[string]any{"destPath": "invalid"}})
		}
		if di, err := j.Lstat(vp); err == nil && !di.IsDir {
			return tasks.Task{}, validationErr("destination is not a folder", map[string]any{"reason": "not_a_folder", "path": string(vp)})
		} else if err != nil {
			if pi, perr := j.Lstat(vp.Dir()); perr != nil || !pi.IsDir {
				return tasks.Task{}, newErr(CodeNotFound, "destination parent not found", http.StatusNotFound, map[string]any{"path": string(vp.Dir())})
			}
		}
		dest = string(vp)
	}
	if len(req.Entries) > MaxEntryFilters {
		return tasks.Task{}, validationErr("too many entries", map[string]any{"fields": map[string]any{"entries": "max 1000"}})
	}
	entries := make([]string, 0, len(req.Entries))
	for _, e := range req.Entries {
		if _, reason, _ := SafePath(e); reason != "" {
			return tasks.Task{}, validationErr("entry name is not valid", map[string]any{"fields": map[string]any{"entries": reason}, "entry": e})
		}
		entries = append(entries, strings.Trim(strings.ReplaceAll(e, "\\", "/"), "/"))
	}
	payload := ExtractPayload{Archive: string(archive), Dest: dest, Encoding: enc, Policy: req.Policy, Entries: entries, HasPassword: req.Password != ""}
	var secrets map[string]string
	if req.Password != "" {
		secrets = map[string]string{"password": req.Password}
	}
	lockPath := string(archive.Dir())
	if dest != DestHere && dest != DestNew {
		lockPath = dest
	}
	title := "Ekstrak " + archive.Base()
	return s.o.Tasks.Enqueue(ctx, tasks.EnqueueRequest{UserID: req.UserID, Type: TaskExtract, Title: title, Payload: payload, Locks: []string{lockPath}, Secrets: secrets})
}

// CleanOrphans removes working folders older than OrphanAge (crash, kill
// during a task). Hourly job.
func (s *Service) CleanOrphans(ctx context.Context) (int, error) {
	if s.o.DataDir == "" {
		return 0, nil
	}
	tmp, err := fsjail.OpenSys(s.o.DataDir, "tmp")
	if err != nil {
		return 0, err
	}
	defer func() { _ = tmp.Close() }()
	names, err := tmp.ReadDirNames(".")
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, name := range names {
		if ctx.Err() != nil {
			return removed, ctx.Err()
		}
		if !strings.HasPrefix(name, TmpPrefix) {
			continue
		}
		info, err := tmp.Lstat(name)
		if err != nil || !info.IsDir() {
			continue
		}
		if s.now().Sub(info.ModTime()) < OrphanAge {
			continue
		}
		if err := tmp.RemoveAll(name); err == nil {
			removed++
		}
	}
	return removed, nil
}

// workspace is DATA_DIR/tmp/ar-<task>.
type workspace struct {
	sys     *fsjail.SysDir
	dataDir string
	id      string
}

func (s *Service) openWork(id string) (*workspace, error) {
	sys, err := fsjail.OpenSys(s.o.DataDir, "tmp/"+TmpPrefix+id)
	if err != nil {
		return nil, err
	}
	return &workspace{sys: sys, dataDir: s.o.DataDir, id: id}, nil
}

// discard closes and removes the working folder.
func (w *workspace) discard() {
	_ = w.sys.Close()
	tmp, err := fsjail.OpenSys(w.dataDir, "tmp")
	if err != nil {
		return
	}
	defer func() { _ = tmp.Close() }()
	_ = tmp.RemoveAll(TmpPrefix + w.id)
}

// jailOn opens a Jail (kernel-enforced root, link policy) on a sub folder
// of the workspace.
func (w *workspace) jailOn(sub string) (*fsjail.Jail, error) {
	if err := w.sys.MkdirAll(sub); err != nil {
		return nil, err
	}
	host, err := w.sys.HostPath(sub)
	if err != nil {
		return nil, err
	}
	return fsjail.Open(host)
}

// helpers shared by both runners

func (s *Service) acquire(ctx context.Context, userID string, mode fsjail.Mode, owner string, paths ...fsjail.VPath) (func(), error) {
	locks := s.o.Locks
	if locks == nil && s.o.Files != nil {
		locks = s.o.Files.Locks()
	}
	if locks == nil {
		return func() {}, nil
	}
	return locks.Acquire(ctx, userID, mode, owner, paths...)
}

func (s *Service) changed(ctx context.Context, userID string, p fsjail.VPath, isDir bool) {
	if s.o.OnChange != nil {
		s.o.OnChange(ctx, userID, p, isDir)
	}
}

func (s *Service) invalidate(userID string, dir fsjail.VPath) {
	if s.o.Invalidate != nil {
		s.o.Invalidate(userID, dir)
	}
}

func (s *Service) activity(ctx context.Context, userID, action, p, p2 string, details map[string]any) {
	if s.o.Files != nil {
		s.o.Files.RecordActivity(ctx, userID, action, p, p2, details)
	}
}
