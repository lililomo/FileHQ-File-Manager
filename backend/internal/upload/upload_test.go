package upload

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/quota"
)

// env is a self-contained upload stack: SQLite, one user with a home, the
// quota service, a Service and an httptest server that injects the
// principal the way httpapi does (session/CSRF are httpapi's job).
type env struct {
	t        *testing.T
	storage  string
	dataDir  string
	store    *db.Store
	user     sqlc.User
	quota    *quota.Service
	svc      *Service
	srv      *httptest.Server
	events   []string
	max      int64
	chunk    int64
	now      time.Time
	userName string
}

func newEnv(t *testing.T, quotaBytes int64) *env {
	t.Helper()
	root := t.TempDir()
	storage := filepath.Join(root, "storage")
	dataDir := filepath.Join(storage, ".system")
	for _, d := range []string{"db", "uploads", "tmp", "thumbs", "trash"} {
		if err := os.MkdirAll(filepath.Join(dataDir, d), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	database, err := db.Open(filepath.Join(dataDir, "db", "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.MigrateUp(context.Background()); err != nil {
		t.Fatal(err)
	}
	store := db.NewStore(database)
	now := db.Now()
	u, err := store.CreateUser(context.Background(), sqlc.CreateUserParams{
		ID: db.NewID(), Username: "alice", DisplayName: "Alice", Role: "user", Status: "active",
		PasswordHash: "x", QuotaBytes: quotaBytes, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fsjail.ProvisionHome(storage, "alice", false); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, storage: storage, dataDir: dataDir, store: store, user: u, userName: "alice", max: 10 << 20, chunk: 1 << 20, now: time.Now()}
	e.build()
	return e
}

func (e *env) openJail(_ context.Context, userID string) (*fsjail.Jail, func(), error) {
	if userID != e.user.ID {
		return nil, nil, fmt.Errorf("unknown user %s", userID)
	}
	j, err := fsjail.OpenUser(e.storage, e.userName, userID)
	if err != nil {
		return nil, nil, err
	}
	return j, func() { _ = j.Close() }, nil
}

// build (re)creates the Service and server over the same disk + DB: used
// to simulate a server restart between chunks.
func (e *env) build() {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	e.quota = quota.New(e.store, logger, nil)
	e.quota.MinDiskFree = 0
	locks := fsjail.NewLockManager()
	filesSvc := files.New(files.Options{Repo: e.store, Locks: locks, Logger: logger, Quota: e.quota, BlockedExtensions: []string{"exe"}})
	svc, err := New(Options{
		DataDir: e.dataDir, MaxUploadSize: e.max, ChunkSize: e.chunk, BaseURL: "https://file.example.test",
		Repo: e.store, Quota: e.quota, Files: filesSvc, Locks: locks, OpenJail: e.openJail,
		Publish: func(_ string, name string, data any) {
			raw, _ := json.Marshal(data)
			e.events = append(e.events, name+" "+string(raw))
		},
		Logger: logger,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	e.svc = svc
	r := chi.NewRouter()
	tus := svc.Tus()
	with := func(next http.HandlerFunc) http.HandlerFunc {
		h := http.StripPrefix("/api/v1/upload/tus", tus.Middleware(next))
		return func(w http.ResponseWriter, r *http.Request) {
			ctx := WithPrincipal(r.Context(), Principal{UserID: e.user.ID, Username: e.userName})
			h.ServeHTTP(w, r.WithContext(ctx))
		}
	}
	r.Post("/api/v1/upload/tus/", with(tus.PostFile))
	r.Head("/api/v1/upload/tus/{id}", with(tus.HeadFile))
	r.Patch("/api/v1/upload/tus/{id}", with(tus.PatchFile))
	r.Delete("/api/v1/upload/tus/{id}", with(tus.DelFile))
	if e.srv != nil {
		e.srv.Close()
	}
	e.srv = httptest.NewServer(r)
	e.t.Cleanup(e.srv.Close)
}

func encodeMeta(md map[string]string) string {
	parts := make([]string, 0, len(md))
	for k, v := range md {
		parts = append(parts, k+" "+base64.StdEncoding.EncodeToString([]byte(v)))
	}
	return strings.Join(parts, ",")
}

type tusResp struct {
	status int
	header http.Header
	body   map[string]any
	raw    string
}

func (e *env) do(method, url string, body []byte, hdr map[string]string) tusResp {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Tus-Resumable", "1.0.0")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := tusResp{status: res.StatusCode, header: res.Header, raw: string(raw)}
	if strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
		var envl struct {
			Error map[string]any `json:"error"`
		}
		if json.Unmarshal(raw, &envl) == nil {
			out.body = envl.Error
		}
	}
	return out
}

// create POSTs a new upload and returns the Location (or the error response).
func (e *env) create(size int64, md map[string]string) (string, tusResp) {
	e.t.Helper()
	res := e.do(http.MethodPost, e.srv.URL+"/api/v1/upload/tus/", nil, map[string]string{
		"Upload-Length": strconv.FormatInt(size, 10), "Upload-Metadata": encodeMeta(md),
	})
	return res.header.Get("Location"), res
}

func (e *env) patch(loc string, offset int64, chunk []byte) tusResp {
	e.t.Helper()
	return e.do(http.MethodPatch, e.rel(loc), chunk, map[string]string{
		"Content-Type": "application/offset+octet-stream", "Upload-Offset": strconv.FormatInt(offset, 10),
	})
}

// rel rewrites the absolute Location (APP_BASE_URL) to the test server.
func (e *env) rel(loc string) string {
	return e.srv.URL + strings.TrimPrefix(loc, "https://file.example.test")
}

func (e *env) idOf(loc string) string { return loc[strings.LastIndex(loc, "/")+1:] }

// upload runs the whole protocol in chunks of chunk bytes.
func (e *env) upload(content []byte, md map[string]string, chunk int) (string, tusResp) {
	e.t.Helper()
	loc, res := e.create(int64(len(content)), md)
	if res.status != http.StatusCreated {
		return loc, res
	}
	if len(content) == 0 {
		return loc, res
	}
	var last tusResp
	for off := 0; off < len(content); off += chunk {
		end := min(off+chunk, len(content))
		last = e.patch(loc, int64(off), content[off:end])
		if last.status != http.StatusNoContent {
			return loc, last
		}
	}
	return loc, last
}

func (e *env) session(id string) sqlc.UploadSession {
	e.t.Helper()
	row, err := e.store.GetUploadSession(context.Background(), id)
	if err != nil {
		e.t.Fatalf("session %s: %v", id, err)
	}
	return row
}

func (e *env) usage() quota.Usage {
	e.t.Helper()
	u, err := e.quota.Usage(context.Background(), e.user.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

func (e *env) home(parts ...string) string {
	return filepath.Join(append([]string{e.storage, "alice"}, parts...)...)
}

func (e *env) mkdir(rel string) {
	if err := os.MkdirAll(e.home(rel), 0o750); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) write(rel, content string) {
	if err := os.WriteFile(e.home(rel), []byte(content), 0o640); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) read(rel string) string {
	raw, err := os.ReadFile(e.home(rel))
	if err != nil {
		e.t.Fatalf("read %s: %v", rel, err)
	}
	return string(raw)
}

func (e *env) uploadsDirEntries() []string {
	names, _ := os.ReadDir(filepath.Join(e.dataDir, "uploads"))
	out := []string{}
	for _, n := range names {
		out = append(out, n.Name())
	}
	return out
}

func content(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + i%26)
	}
	return b
}

func TestProtocolRoundTrip(t *testing.T) {
	e := newEnv(t, 10<<20)
	e.mkdir("docs")
	data := content(2<<20 + 123)
	lm := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
	loc, res := e.upload(data, map[string]string{"filename": "laporan.txt", "path": "/docs", "lastModified": strconv.FormatInt(lm.UnixMilli(), 10)}, 1<<20)
	if res.status != http.StatusNoContent {
		t.Fatalf("final PATCH: %d %s", res.status, res.raw)
	}
	if got := res.header.Get(HeaderResult); got != "created" {
		t.Errorf("Upload-Result = %q", got)
	}
	if got := res.header.Get(HeaderPath); got != "/docs/laporan.txt" {
		t.Errorf("Upload-Path = %q", got)
	}
	if got := e.read("docs/laporan.txt"); got != string(data) {
		t.Fatalf("content mismatch (%d bytes)", len(got))
	}
	fi, err := os.Stat(e.home("docs", "laporan.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !fi.ModTime().Equal(lm) {
		t.Errorf("mtime = %v, want %v (lastModified)", fi.ModTime(), lm)
	}
	if fi.Mode().Perm() != 0o640 {
		t.Errorf("mode = %o, want 0640 (SPEC §8.8)", fi.Mode().Perm())
	}
	row := e.session(e.idOf(loc))
	if row.State != StateFinalized || row.FinalizedPath == nil || *row.FinalizedPath != "/docs/laporan.txt" {
		t.Errorf("session = %+v", row)
	}
	if u := e.usage(); u.Used != int64(len(data)) || u.Reserved != 0 {
		t.Errorf("usage = %+v", u)
	}
	if got := e.uploadsDirEntries(); len(got) != 0 {
		t.Errorf("uploads dir not empty: %v", got)
	}
	if len(e.events) == 0 || !strings.Contains(e.events[0], `"reason":"upload"`) || !strings.Contains(e.events[0], `/docs/laporan.txt`) {
		t.Errorf("file.changed not published: %v", e.events)
	}
	acts, err := e.store.ListActivity(context.Background(), e.user.ID, 0, 10)
	if err != nil || len(acts) != 1 || acts[0].Action != files.ActUpload {
		t.Errorf("activity = %v %v", acts, err)
	}
}

func TestEmptyFileFinishesOnCreate(t *testing.T) {
	e := newEnv(t, 0)
	_, res := e.upload(nil, map[string]string{"filename": "kosong.txt", "path": "/"}, 1<<20)
	if res.status != http.StatusCreated {
		t.Fatalf("POST: %d %s", res.status, res.raw)
	}
	if got := res.header.Get(HeaderResult); got != "created" {
		t.Errorf("Upload-Result = %q (empty uploads finish inside POST)", got)
	}
	if _, err := os.Stat(e.home("kosong.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestMetadataValidation(t *testing.T) {
	e := newEnv(t, 0)
	cases := []struct {
		name   string
		size   int64
		md     map[string]string
		status int
		code   string
	}{
		{"missing filename", 10, map[string]string{"path": "/"}, 400, CodeValidationFailed},
		{"slash in name", 10, map[string]string{"filename": "../x", "path": "/"}, 400, CodeNameInvalid},
		{"nul in name", 10, map[string]string{"filename": "a\x00b", "path": "/"}, 400, CodeNameInvalid},
		{"blocked extension", 10, map[string]string{"filename": "virus.EXE", "path": "/"}, 415, CodeUnsupportedType},
		{"dest missing", 10, map[string]string{"filename": "a.txt", "path": "/tidak-ada"}, 404, CodeNotFound},
		{"dest escapes", 10, map[string]string{"filename": "a.txt", "path": "/../.."}, 400, CodePathInvalid},
		{"dest .system", 10, map[string]string{"filename": "a.txt", "path": "/.system"}, 400, CodePathInvalid},
		{"relative path escapes", 10, map[string]string{"filename": "a.txt", "path": "/", "relativePath": "../x"}, 400, CodeNameInvalid},
		{"policy fail", 10, map[string]string{"filename": "a.txt", "path": "/", "onConflict": "fail"}, 400, CodeValidationFailed},
		{"bad lastModified", 10, map[string]string{"filename": "a.txt", "path": "/", "lastModified": "soon"}, 400, CodeValidationFailed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, res := e.create(c.size, c.md)
			if res.status != c.status {
				t.Fatalf("status %d, want %d: %s", res.status, c.status, res.raw)
			}
			if got, _ := res.body["code"].(string); got != c.code {
				t.Errorf("code %q, want %q (%s)", got, c.code, res.raw)
			}
			if _, ok := res.body["details"].(map[string]any); !ok {
				t.Errorf("envelope without details: %s", res.raw)
			}
		})
	}
	t.Run("above MAX_UPLOAD_SIZE", func(t *testing.T) {
		_, res := e.create(e.max+1, map[string]string{"filename": "big.bin", "path": "/"})
		if res.status != http.StatusRequestEntityTooLarge {
			t.Fatalf("status %d: %s", res.status, res.raw)
		}
	})
	t.Run("deferred length", func(t *testing.T) {
		res := e.do(http.MethodPost, e.srv.URL+"/api/v1/upload/tus/", nil, map[string]string{"Upload-Defer-Length": "1", "Upload-Metadata": encodeMeta(map[string]string{"filename": "a", "path": "/"})})
		if res.status != http.StatusBadRequest {
			t.Fatalf("status %d: %s", res.status, res.raw)
		}
	})
	t.Run("no session rows were left behind", func(t *testing.T) {
		n, err := e.store.CountActiveUploadSessions(context.Background(), e.user.ID)
		if err != nil || n != 0 {
			t.Errorf("active sessions = %d %v", n, err)
		}
	})
}

func TestQuotaReservationIsDurable(t *testing.T) {
	e := newEnv(t, 3<<20)
	// 2 MB fits, a second 2 MB does not (the first is only reserved, not written)
	loc1, res := e.create(2<<20, map[string]string{"filename": "a.bin", "path": "/"})
	if res.status != http.StatusCreated {
		t.Fatalf("first create: %d %s", res.status, res.raw)
	}
	if u := e.usage(); u.Reserved != 2<<20 {
		t.Fatalf("reserved = %d, want 2 MiB from the session row", u.Reserved)
	}
	_, res = e.create(2<<20, map[string]string{"filename": "b.bin", "path": "/"})
	if res.status != http.StatusRequestEntityTooLarge || res.body["code"] != CodeQuotaExceeded {
		t.Fatalf("second create: %d %s", res.status, res.raw)
	}
	d, _ := res.body["details"].(map[string]any)
	if d["reason"] != "quota" || d["needed"] != float64(2<<20) {
		t.Errorf("details = %v", d)
	}
	// a fresh Service over the same DB still sees the reservation (restart)
	e.build()
	_, res = e.create(2<<20, map[string]string{"filename": "b.bin", "path": "/"})
	if res.status != http.StatusRequestEntityTooLarge {
		t.Fatalf("after restart: %d %s", res.status, res.raw)
	}
	// terminating the first upload releases it
	if res := e.do(http.MethodDelete, e.rel(loc1), nil, nil); res.status != http.StatusNoContent {
		t.Fatalf("DELETE: %d %s", res.status, res.raw)
	}
	if row := e.session(e.idOf(loc1)); row.State != StateAborted {
		t.Errorf("state after DELETE = %s", row.State)
	}
	if got := e.uploadsDirEntries(); len(got) != 0 {
		t.Errorf("files after DELETE: %v", got)
	}
	if _, res = e.create(2<<20, map[string]string{"filename": "b.bin", "path": "/"}); res.status != http.StatusCreated {
		t.Fatalf("after release: %d %s", res.status, res.raw)
	}
	// 2 MB + 2 MB over a 3 MB limit: a finished upload counts as used
	_, res = e.upload(content(2<<20), map[string]string{"filename": "c.bin", "path": "/"}, 1<<20)
	if res.status != http.StatusRequestEntityTooLarge {
		t.Fatalf("used+reserved > limit must be rejected: %d %s", res.status, res.raw)
	}
}

func TestConflictPolicies(t *testing.T) {
	e := newEnv(t, 0)
	// the original goes through the API so the quota counters know its bytes
	if _, res := e.upload([]byte("lama-lama-lama"), map[string]string{"filename": "x.txt", "path": "/"}, 1<<20); res.status != http.StatusNoContent {
		t.Fatal(res.raw)
	}
	t.Run("default rename", func(t *testing.T) {
		_, res := e.upload([]byte("baru"), map[string]string{"filename": "x.txt", "path": "/"}, 1<<20)
		if p, _ := url.PathUnescape(res.header.Get(HeaderPath)); res.status != http.StatusNoContent || res.header.Get(HeaderResult) != "renamed" || p != "/x (1).txt" {
			t.Fatalf("%d %s %s %s", res.status, res.raw, res.header.Get(HeaderResult), res.header.Get(HeaderPath))
		}
		if e.read("x (1).txt") != "baru" || e.read("x.txt") != "lama-lama-lama" {
			t.Error("rename policy touched the wrong file")
		}
	})
	t.Run("overwrite accounts the delta", func(t *testing.T) {
		before := e.usage().Used
		_, res := e.upload([]byte("ganti"), map[string]string{"filename": "x.txt", "path": "/", "onConflict": "overwrite"}, 1<<20)
		if res.status != http.StatusNoContent || res.header.Get(HeaderResult) != "overwritten" {
			t.Fatalf("%d %s %s", res.status, res.raw, res.header.Get(HeaderResult))
		}
		if e.read("x.txt") != "ganti" {
			t.Error("overwrite did not replace the content")
		}
		if got := e.usage().Used - before; got != int64(len("ganti")-len("lama-lama-lama")) {
			t.Errorf("used delta = %d", got)
		}
	})
	t.Run("skip drops the bytes", func(t *testing.T) {
		before := e.usage().Used
		loc, res := e.upload([]byte("dibuang"), map[string]string{"filename": "x.txt", "path": "/", "onConflict": "skip"}, 1<<20)
		if res.status != http.StatusNoContent || res.header.Get(HeaderResult) != "skipped" || res.header.Get(HeaderPath) != "" {
			t.Fatalf("%d %s %s", res.status, res.raw, res.header.Get(HeaderResult))
		}
		if e.read("x.txt") != "ganti" {
			t.Error("skip changed the file")
		}
		row := e.session(e.idOf(loc))
		if row.State != StateFinalized || row.FinalizedPath != nil {
			t.Errorf("session = %+v", row)
		}
		if e.usage().Used != before || e.usage().Reserved != 0 {
			t.Errorf("usage changed: %+v", e.usage())
		}
		if got := e.uploadsDirEntries(); len(got) != 0 {
			t.Errorf("leftovers: %v", got)
		}
	})
	t.Run("overwrite onto a folder is a type mismatch", func(t *testing.T) {
		e.mkdir("map")
		loc, res := e.upload([]byte("z"), map[string]string{"filename": "map", "path": "/", "onConflict": "overwrite"}, 1<<20)
		if res.status != http.StatusBadRequest || res.body["code"] != CodeValidationFailed {
			t.Fatalf("%d %s", res.status, res.raw)
		}
		if row := e.session(e.idOf(loc)); row.State != StateAborted {
			t.Errorf("state = %s, want aborted (terminal)", row.State)
		}
		if got := e.uploadsDirEntries(); len(got) != 0 {
			t.Errorf("leftovers: %v", got)
		}
	})
}

func TestFolderUploadCreatesStructure(t *testing.T) {
	e := newEnv(t, 0)
	e.mkdir("dest")
	_, res := e.upload([]byte("deep"), map[string]string{"filename": "file.txt", "path": "/dest", "relativePath": "a/b/c"}, 1<<20)
	if res.status != http.StatusNoContent {
		t.Fatalf("%d %s", res.status, res.raw)
	}
	if res.header.Get(HeaderPath) != "/dest/a/b/c/file.txt" || e.read("dest/a/b/c/file.txt") != "deep" {
		t.Fatalf("path = %s", res.header.Get(HeaderPath))
	}
	// the top-level folder of the drop and the leaf folder are both announced
	joined := strings.Join(e.events, "\n")
	if !strings.Contains(joined, `"dir":"/dest/a/b/c"`) || !strings.Contains(joined, `"dir":"/dest"`) {
		t.Errorf("events = %v", e.events)
	}
	// unicode + Windows-incompatible warning survives
	_, res = e.upload([]byte("w"), map[string]string{"filename": "laporan: akhir.txt", "path": "/dest", "relativePath": "Désa/sub"}, 1<<20)
	if res.status != http.StatusNoContent || res.header.Get(HeaderPath) != "/dest/D%C3%A9sa/sub/laporan:%20akhir.txt" {
		t.Fatalf("%d %s %s", res.status, res.raw, res.header.Get(HeaderPath))
	}
}

func TestDestinationRemovedMidUpload(t *testing.T) {
	e := newEnv(t, 0)
	e.mkdir("tmpdir")
	data := content(2 << 20)
	loc, res := e.create(int64(len(data)), map[string]string{"filename": "f.bin", "path": "/tmpdir"})
	if res.status != http.StatusCreated {
		t.Fatal(res.raw)
	}
	if res := e.patch(loc, 0, data[:1<<20]); res.status != http.StatusNoContent {
		t.Fatal(res.raw)
	}
	if err := os.RemoveAll(e.home("tmpdir")); err != nil {
		t.Fatal(err)
	}
	res = e.patch(loc, 1<<20, data[1<<20:])
	if res.status != http.StatusNotFound || res.body["code"] != CodeNotFound {
		t.Fatalf("%d %s", res.status, res.raw)
	}
	if row := e.session(e.idOf(loc)); row.State != StateAborted {
		t.Errorf("state = %s", row.State)
	}
	if got := e.uploadsDirEntries(); len(got) != 0 {
		t.Errorf("leftovers: %v", got)
	}
	if u := e.usage(); u.Used != 0 || u.Reserved != 0 {
		t.Errorf("usage = %+v", u)
	}
}

func TestResumeAcrossRestart(t *testing.T) {
	e := newEnv(t, 0)
	data := content(3 << 20)
	loc, res := e.create(int64(len(data)), map[string]string{"filename": "r.bin", "path": "/"})
	if res.status != http.StatusCreated {
		t.Fatal(res.raw)
	}
	if res := e.patch(loc, 0, data[:1<<20]); res.status != http.StatusNoContent || res.header.Get("Upload-Offset") != strconv.Itoa(1<<20) {
		t.Fatalf("%d %s %s", res.status, res.raw, res.header.Get("Upload-Offset"))
	}
	// wrong offset is refused (409) and does not corrupt the file
	if res := e.patch(loc, 0, data[:1<<20]); res.status != http.StatusConflict {
		t.Fatalf("mismatched offset: %d", res.status)
	}
	// "kill -TERM; start": a new Service over the same disk and DB
	e.build()
	res = e.do(http.MethodHead, e.rel(loc), nil, nil)
	if res.status != http.StatusOK || res.header.Get("Upload-Offset") != strconv.Itoa(1<<20) {
		t.Fatalf("HEAD after restart: %d offset=%s", res.status, res.header.Get("Upload-Offset"))
	}
	for off := 1 << 20; off < len(data); off += 1 << 20 {
		if res = e.patch(loc, int64(off), data[off:off+1<<20]); res.status != http.StatusNoContent {
			t.Fatalf("PATCH %d: %d %s", off, res.status, res.raw)
		}
	}
	if e.read("r.bin") != string(data) {
		t.Fatal("resumed content differs")
	}
	// a finished upload id is gone for HEAD (fingerprint was removed on success)
	if res = e.do(http.MethodHead, e.rel(loc), nil, nil); res.status != http.StatusNotFound {
		t.Errorf("HEAD finished: %d", res.status)
	}
}

func TestCleanExpired(t *testing.T) {
	e := newEnv(t, 0)
	data := content(1 << 20)
	loc, res := e.create(int64(len(data)), map[string]string{"filename": "stale.bin", "path": "/"})
	if res.status != http.StatusCreated {
		t.Fatal(res.raw)
	}
	if res := e.patch(loc, 0, data[:1<<19]); res.status != http.StatusNoContent {
		t.Fatal(res.raw)
	}
	id := e.idOf(loc)
	// a live upload must survive
	if r, err := e.svc.CleanExpired(context.Background()); err != nil || r.Expired != 0 || r.Orphans != 0 {
		t.Fatalf("premature cleanup: %+v %v", r, err)
	}
	// age the session 25 h and the files
	past := db.FormatTime(time.Now().Add(-25 * time.Hour))
	if err := e.store.TouchUploadSession(context.Background(), id, past, past); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(e.dataDir, "uploads", "orphan-old")
	if err := os.WriteFile(orphan, []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-30 * time.Hour)
	for _, f := range []string{orphan} {
		if err := os.Chtimes(f, old, old); err != nil {
			t.Fatal(err)
		}
	}
	fresh := filepath.Join(e.dataDir, "uploads", "orphan-new")
	if err := os.WriteFile(fresh, []byte("y"), 0o640); err != nil {
		t.Fatal(err)
	}
	if u := e.usage(); u.Reserved != int64(len(data)) {
		t.Fatalf("reserved before cleanup = %d", u.Reserved)
	}
	r, err := e.svc.CleanExpired(context.Background())
	if err != nil || r.Expired != 1 || r.Orphans != 1 {
		t.Fatalf("cleanup = %+v %v", r, err)
	}
	if row := e.session(id); row.State != StateExpired {
		t.Errorf("state = %s", row.State)
	}
	if u := e.usage(); u.Reserved != 0 {
		t.Errorf("reserved after cleanup = %d", u.Reserved)
	}
	got := e.uploadsDirEntries()
	if len(got) != 1 || got[0] != "orphan-new" {
		t.Errorf("uploads dir = %v (only the fresh orphan may stay)", got)
	}
	// the expired upload cannot be resumed
	if res := e.do(http.MethodHead, e.rel(loc), nil, nil); res.status != http.StatusNotFound {
		t.Errorf("HEAD expired: %d", res.status)
	}
}

func TestPrecheck(t *testing.T) {
	e := newEnv(t, 5<<20)
	e.mkdir("dest")
	e.write("dest/ada.txt", "existing")
	e.mkdir("dest/folder")
	j, release, err := e.openJail(context.Background(), e.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	res, perr := e.svc.Precheck(context.Background(), j, e.user.ID, "/dest", []PrecheckFile{
		{Name: "ada.txt", Size: 100},
		{Name: "baru.txt", Size: 200, RelativePath: "sub/dir"},
		{Name: "folder", Size: 1},
		{Name: "virus.exe", Size: 1},
		{Name: "besar.bin", Size: e.max + 1},
		{Name: "bad/name", Size: 1},
		{Name: "ok.txt", Size: 1, RelativePath: "../escape"},
	})
	if perr != nil {
		t.Fatal(perr)
	}
	want := map[string]string{"ada.txt": "conflict", "baru.txt": "ok", "folder": "conflict", "virus.exe": "blocked_ext", "besar.bin": "too_large", "bad/name": "name_invalid", "ok.txt": "name_invalid"}
	for _, it := range res.Items {
		if it.Status != want[it.Name] {
			t.Errorf("%s: status %s (%s), want %s", it.Name, it.Status, it.Reason, want[it.Name])
		}
	}
	if res.Items[0].Existing == nil || res.Items[0].Existing.Size != 8 || res.Items[0].Path != "/dest/ada.txt" {
		t.Errorf("conflict stat = %+v", res.Items[0])
	}
	if res.Items[1].Path != "/dest/sub/dir/baru.txt" {
		t.Errorf("relative path target = %s", res.Items[1].Path)
	}
	if res.Items[2].Reason != "type_mismatch" {
		t.Errorf("folder conflict reason = %s", res.Items[2].Reason)
	}
	if res.Needed != 301 || !res.QuotaOK || res.Free != 5<<20 || res.MaxUploadSize != e.max || res.ChunkSize != e.chunk {
		t.Errorf("summary = %+v", res)
	}
	// quota 5 MiB: a 6 MiB file is reported, nothing is reserved
	res, perr = e.svc.Precheck(context.Background(), j, e.user.ID, "/dest", []PrecheckFile{{Name: "x.bin", Size: 6 << 20}})
	if perr != nil || res.QuotaOK || res.QuotaReason != "quota" || res.Needed != 6<<20 {
		t.Errorf("quota precheck = %+v %v", res, perr)
	}
	if _, perr = e.svc.Precheck(context.Background(), j, e.user.ID, "/nope", []PrecheckFile{{Name: "x", Size: 1}}); perr == nil || perr.Code != CodeNotFound {
		t.Errorf("missing dest = %v", perr)
	}
	if _, perr = e.svc.Precheck(context.Background(), j, e.user.ID, "/dest", nil); perr == nil || perr.Code != CodeValidationFailed {
		t.Errorf("empty files = %v", perr)
	}
}
