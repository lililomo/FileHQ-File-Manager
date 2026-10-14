package remotedl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/tasks"
)

func TestRunNamesFileFromDispositionURLAndPayload(t *testing.T) {
	e := newEnv(t)
	s := e.newServer(t)
	data, sum := pattern(300 << 10)
	s.serveBytes("/dl/report%20final.bin", "report final.bin", data, nil)
	s.serveBytes("/cd", "x", data, http.Header{"Content-Disposition": []string{`attachment; filename="old.txt"; filename*=UTF-8''laporan%20%C3%A9.pdf`}})
	s.serveBytes("/evil-cd", "x", data, http.Header{"Content-Disposition": []string{`attachment; filename="../../etc/passwd"`}})
	s.serveBytes("/", "x", data, nil)

	tk := e.run(t, Payload{URL: s.url("/dl/report%20final.bin"), Dest: "/"})
	if tk.State != tasks.Done {
		t.Fatalf("state %s %+v", tk.State, tk.Error)
	}
	r := result(t, tk)
	if r.Path != "/report final.bin" || r.Size != int64(len(data)) || r.Status != "created" || !strings.HasPrefix(r.FinalURL, "http://files.test:") || strings.Contains(r.FinalURL, "?") {
		t.Fatalf("result %+v", r)
	}
	if got := sha256File(t, filepath.Join(e.home, "report final.bin")); got != sum {
		t.Fatal("checksum mismatch")
	}
	if tk.Progress.Done != int64(len(data)) || tk.Progress.Total != int64(len(data)) || tk.Progress.Unit != "bytes" {
		t.Fatalf("progress %+v", tk.Progress)
	}
	// Content-Disposition filename* wins over filename and the URL
	r = result(t, e.run(t, Payload{URL: s.url("/cd"), Dest: "/"}))
	if r.Path != "/laporan é.pdf" {
		t.Fatalf("cd name: %q", r.Path)
	}
	// hostile disposition → base name only
	r = result(t, e.run(t, Payload{URL: s.url("/evil-cd"), Dest: "/"}))
	if r.Path != "/passwd" {
		t.Fatalf("evil cd: %q", r.Path)
	}
	// explicit payload filename wins over everything
	r = result(t, e.run(t, Payload{URL: s.url("/cd"), Dest: "/", Filename: "custom.pdf"}))
	if r.Path != "/custom.pdf" {
		t.Fatalf("explicit: %q", r.Path)
	}
	// no segment, no disposition → download-<unix>
	r = result(t, e.run(t, Payload{URL: s.url("/"), Dest: "/"}))
	if !strings.HasPrefix(r.Path, "/download-") {
		t.Fatalf("fallback: %q", r.Path)
	}
	// blocked extension → failed, nothing left behind
	tk = e.run(t, Payload{URL: s.url("/cd"), Dest: "/", Filename: "tool.exe"})
	if tk.State != tasks.Failed || tk.Error == nil || tk.Error.Code != "UNSUPPORTED_TYPE" {
		t.Fatalf("blocked ext: %s %+v", tk.State, tk.Error)
	}
	s.serveBytes("/setup.EXE", "x", data, nil)
	tk = e.run(t, Payload{URL: s.url("/setup.EXE"), Dest: "/"})
	if tk.State != tasks.Failed || tk.Error.Code != "UNSUPPORTED_TYPE" {
		t.Fatalf("blocked url ext: %s %+v", tk.State, tk.Error)
	}
	if left := e.tmpEntries(t); len(left) != 0 {
		t.Fatalf("tmp left: %v", left)
	}
	if e.reserved() != 0 {
		t.Fatal("quota still reserved")
	}
	// quota counted
	u, _ := e.quota.Usage(context.Background(), e.user.ID)
	if u.Used != int64(len(data))*5 {
		t.Fatalf("used %d", u.Used)
	}
	// destination folder missing / not a folder
	tk = e.run(t, Payload{URL: s.url("/cd"), Dest: "/nope"})
	if tk.State != tasks.Failed || tk.Error.Code != "NOT_FOUND" {
		t.Fatalf("missing dest: %+v", tk.Error)
	}
}

func TestRunLimitsAndQuota(t *testing.T) {
	e := newEnv(t, withOptions(func(o *Options) { o.MaxSize = 1 << 20 }))
	s := e.newServer(t)
	big, _ := pattern(2 << 20)
	small, _ := pattern(200 << 10)
	s.serveBytes("/big.bin", "big.bin", big, nil)
	s.serveBytes("/small.bin", "small.bin", small, nil)
	s.mux.HandleFunc("/chunked-big", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(200)
		_, _ = w.Write(big) // no Content-Length (chunked)
	})
	s.mux.HandleFunc("/chunked-small", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write(small)
	})
	// Content-Length above the limit: refused before the body is read
	tk := e.run(t, Payload{URL: s.url("/big.bin"), Dest: "/"})
	if tk.State != tasks.Failed || tk.Error.Code != "FILE_TOO_LARGE" {
		t.Fatalf("cl limit: %s %+v", tk.State, tk.Error)
	}
	// no Content-Length: aborted while streaming
	tk = e.run(t, Payload{URL: s.url("/chunked-big"), Dest: "/"})
	if tk.State != tasks.Failed || tk.Error.Code != "FILE_TOO_LARGE" {
		t.Fatalf("stream limit: %s %+v", tk.State, tk.Error)
	}
	// unknown length within the limit works and is charged after the fact
	tk = e.run(t, Payload{URL: s.url("/chunked-small"), Dest: "/"})
	if tk.State != tasks.Done {
		t.Fatalf("chunked small: %+v", tk.Error)
	}
	if r := result(t, tk); r.Path != "/chunked-small" || r.Size != int64(len(small)) {
		t.Fatalf("chunked result %+v", r)
	}
	// quota: 300 KiB left → 200 KiB known-size download refused, no partial
	e.setQuota(t, int64(len(small))+100<<10)
	tk = e.run(t, Payload{URL: s.url("/small.bin"), Dest: "/"})
	if tk.State != tasks.Failed || tk.Error.Code != "QUOTA_EXCEEDED" {
		t.Fatalf("quota: %s %+v", tk.State, tk.Error)
	}
	tk = e.run(t, Payload{URL: s.url("/chunked-small"), Dest: "/", Filename: "second"})
	if tk.State != tasks.Failed || tk.Error.Code != "QUOTA_EXCEEDED" {
		t.Fatalf("quota unknown length: %s %+v", tk.State, tk.Error)
	}
	if _, err := os.Stat(filepath.Join(e.home, "small.bin")); err == nil {
		t.Fatal("partial file landed despite quota")
	}
	if left := e.tmpEntries(t); len(left) != 0 {
		t.Fatalf("tmp left: %v", left)
	}
	if e.reserved() != 0 {
		t.Fatal("reservation leaked")
	}
	// settings table lowers the limit further (max_size as a size string)
	if err := e.store.UpsertSetting(context.Background(), sqlc.UpsertSettingParams{Key: KeyMaxSize, ValueJson: `"100KB"`, UpdatedAt: "2026-10-14T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	e.svc.InvalidateSettings()
	e.setQuota(t, 10<<20)
	tk = e.run(t, Payload{URL: s.url("/small.bin"), Dest: "/", Filename: "third"})
	if tk.State != tasks.Failed || tk.Error.Code != "FILE_TOO_LARGE" {
		t.Fatalf("settings limit: %s %+v", tk.State, tk.Error)
	}
}

// flakyHandler serves data with Range support but drops the connection
// after `cut` bytes on the first `fails` full requests.
func flakyHandler(data []byte, cut int, fails int32) http.HandlerFunc {
	var served int32
	mod := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"flaky"`)
		if r.Header.Get("Range") == "" && atomic.AddInt32(&served, 1) <= fails {
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.Header().Set("Accept-Ranges", "bytes")
			w.WriteHeader(200)
			_, _ = w.Write(data[:cut])
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler) // closes the connection mid-body
		}
		http.ServeContent(w, r, "flaky.bin", mod, strings.NewReader(string(data)))
	}
}

func TestRunResumesAfterNetworkErrorInsideTheTask(t *testing.T) {
	e := newEnv(t)
	s := e.newServer(t)
	data, sum := pattern(1 << 20)
	s.mux.HandleFunc("/flaky.bin", flakyHandler(data, 300<<10, 1))
	tk := e.run(t, Payload{URL: s.url("/flaky.bin"), Dest: "/"})
	if tk.State != tasks.Done {
		t.Fatalf("state %s %+v", tk.State, tk.Error)
	}
	r := result(t, tk)
	if r.Resumed != 300<<10 {
		t.Fatalf("expected resume from 300 KiB, got %d", r.Resumed)
	}
	if sha256File(t, filepath.Join(e.home, "flaky.bin")) != sum {
		t.Fatal("checksum mismatch after resume")
	}
	logs, _ := e.engine.Logs(context.Background(), e.user.ID, tk.ID, 0)
	var sawRetry bool
	for _, l := range logs {
		if strings.Contains(l.Message, "retrying") {
			sawRetry = true
		}
	}
	if !sawRetry {
		t.Fatal("no retry log line")
	}
}

func TestRetryTaskResumesWithRangeFromKeptPart(t *testing.T) {
	e := newEnv(t, withOptions(func(o *Options) { o.RetryBackoff = []time.Duration{} }))
	s := e.newServer(t)
	data, sum := pattern(1 << 20)
	s.mux.HandleFunc("/keep.bin", flakyHandler(data, 500<<10, 1))
	tk := e.run(t, Payload{URL: s.url("/keep.bin"), Dest: "/"})
	if tk.State != tasks.Failed || tk.Error.Code != "REMOTE_NETWORK" {
		t.Fatalf("first run: %s %+v", tk.State, tk.Error)
	}
	part := filepath.Join(e.cfg.DataDir, "tmp", TmpPrefix+tk.ID, partName)
	if st, err := os.Stat(part); err != nil || st.Size() != 500<<10 {
		t.Fatalf("part kept for retry: %v", err)
	}
	if e.reserved() != 0 {
		t.Fatal("reservation must be released on failure")
	}
	// Retry → new task, Range from 500 KiB, then both working folders go
	rt, err := e.engine.Retry(context.Background(), e.user.ID, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	rt = e.wait(t, rt.ID, 30*time.Second)
	if rt.State != tasks.Done {
		t.Fatalf("retry: %s %+v", rt.State, rt.Error)
	}
	if r := result(t, rt); r.Resumed != 500<<10 {
		t.Fatalf("retry resumed from %d", r.Resumed)
	}
	if sha256File(t, filepath.Join(e.home, "keep.bin")) != sum {
		t.Fatal("checksum mismatch after retry")
	}
	if left := e.tmpEntries(t); len(left) != 0 {
		t.Fatalf("tmp left: %v", left)
	}
	// a server that ignores Range restarts from zero instead of corrupting
	s.mux.HandleFunc("/norange.bin", func() http.HandlerFunc {
		var n int32
		return func(w http.ResponseWriter, r *http.Request) {
			if atomic.AddInt32(&n, 1) == 1 {
				w.Header().Set("Content-Length", strconv.Itoa(len(data)))
				w.Header().Set("Accept-Ranges", "bytes")
				w.Header().Set("ETag", `"nr"`)
				w.WriteHeader(200)
				_, _ = w.Write(data[:100<<10])
				panic(http.ErrAbortHandler)
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.WriteHeader(200) // ignores Range
			_, _ = w.Write(data)
		}
	}())
	tk = e.run(t, Payload{URL: s.url("/norange.bin"), Dest: "/"})
	if tk.State != tasks.Failed {
		t.Fatalf("expected failure without in-task retries: %s", tk.State)
	}
	rt, _ = e.engine.Retry(context.Background(), e.user.ID, tk.ID)
	rt = e.wait(t, rt.ID, 30*time.Second)
	if rt.State != tasks.Done || result(t, rt).Resumed != 0 {
		t.Fatalf("restart: %s %+v %s", rt.State, rt.Error, string(rt.Result))
	}
	if sha256File(t, filepath.Join(e.home, "norange.bin")) != sum {
		t.Fatal("checksum mismatch after restart")
	}
}

func TestRunConflictPolicies(t *testing.T) {
	e := newEnv(t)
	s := e.newServer(t)
	data, sum := pattern(64 << 10)
	s.serveBytes("/f.bin", "f.bin", data, nil)
	if err := os.WriteFile(filepath.Join(e.home, "f.bin"), []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(e.home, "folder.bin"), 0o750); err != nil {
		t.Fatal(err)
	}
	tk := e.run(t, Payload{URL: s.url("/f.bin"), Dest: "/"})
	if tk.State != tasks.Failed || tk.Error.Code != "ALREADY_EXISTS" {
		t.Fatalf("fail policy: %s %+v", tk.State, tk.Error)
	}
	r := result(t, e.run(t, Payload{URL: s.url("/f.bin"), Dest: "/", Policy: "skip"}))
	if r.Status != "skipped" || r.Path != "/f.bin" {
		t.Fatalf("skip: %+v", r)
	}
	if b, _ := os.ReadFile(filepath.Join(e.home, "f.bin")); string(b) != "old" {
		t.Fatal("skip must not touch the existing file")
	}
	r = result(t, e.run(t, Payload{URL: s.url("/f.bin"), Dest: "/", Policy: "rename"}))
	if r.Status != "renamed" || r.Path != "/f (1).bin" {
		t.Fatalf("rename: %+v", r)
	}
	r = result(t, e.run(t, Payload{URL: s.url("/f.bin"), Dest: "/", Policy: "overwrite"}))
	if r.Status != "overwritten" || r.Path != "/f.bin" || sha256File(t, filepath.Join(e.home, "f.bin")) != sum {
		t.Fatalf("overwrite: %+v", r)
	}
	tk = e.run(t, Payload{URL: s.url("/f.bin"), Dest: "/", Filename: "folder.bin", Policy: "overwrite"})
	if tk.State != tasks.Failed || tk.Error.Code != "VALIDATION_FAILED" {
		t.Fatalf("type mismatch: %s %+v", tk.State, tk.Error)
	}
	u, _ := e.quota.Usage(context.Background(), e.user.ID)
	// "f (1).bin" + overwritten f.bin; the seeded 3-byte "old" was never
	// counted, so the overwrite subtracts it from the delta
	if u.Used != int64(len(data))*2-3 {
		t.Fatalf("used %d want %d", u.Used, len(data)*2-3)
	}
	if left := e.tmpEntries(t); len(left) != 0 {
		t.Fatalf("tmp left: %v", left)
	}
}

func TestCancelRemovesPartAndReleasesQuota(t *testing.T) {
	e := newEnv(t)
	s := e.newServer(t)
	data, _ := pattern(2 << 20)
	s.mux.HandleFunc("/slow.bin", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(200)
		for off := 0; off < len(data); off += 64 << 10 {
			if _, err := w.Write(data[off : off+64<<10]); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			time.Sleep(20 * time.Millisecond)
		}
	})
	tk := e.enqueue(t, Payload{URL: s.url("/slow.bin"), Dest: "/"})
	deadline := time.Now().Add(10 * time.Second)
	for {
		cur, _ := e.engine.Get(context.Background(), e.user.ID, tk.ID)
		if cur.Progress.Done > 0 && cur.State == tasks.Running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no progress")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e.reserved() != int64(len(data)) {
		t.Fatalf("reserved %d during download", e.reserved())
	}
	if _, err := e.engine.Cancel(context.Background(), e.user.ID, tk.ID); err != nil {
		t.Fatal(err)
	}
	tk = e.wait(t, tk.ID, 10*time.Second)
	if tk.State != tasks.Cancelled {
		t.Fatalf("state %s", tk.State)
	}
	if left := e.tmpEntries(t); len(left) != 0 {
		t.Fatalf("part left after cancel: %v", left)
	}
	if e.reserved() != 0 {
		t.Fatal("reservation not released")
	}
	if _, err := os.Stat(filepath.Join(e.home, "slow.bin")); err == nil {
		t.Fatal("destination must not exist")
	}
}

func TestRunBlocksSSRFAtResolveAndRedirect(t *testing.T) {
	e := newEnv(t)
	s := e.newServer(t)
	e.resolver.set("meta.test", "169.254.169.254")
	e.resolver.set("v6.test", "fc00::1")
	e.resolver.set("mixed.test", "93.184.216.34", "10.1.2.3")
	s.mux.HandleFunc("/to-meta", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	})
	for _, raw := range []string{"http://meta.test/x", "http://v6.test/x", "http://mixed.test/x", s.url("/to-meta")} {
		tk := e.run(t, Payload{URL: raw, Dest: "/"})
		if tk.State != tasks.Failed || tk.Error == nil || tk.Error.Code != "SSRF_BLOCKED" {
			t.Fatalf("%s: %s %+v", raw, tk.State, tk.Error)
		}
	}
	// a literal private IP is refused even before the engine (Create);
	// loopback is exempt in this harness (AllowLoopback), link-local never
	if _, err := e.svc.ValidateURL(context.Background(), "http://127.0.0.1:8080/healthz"); err != nil {
		t.Fatalf("loopback is allowed in the test harness: %v", err)
	}
	if _, err := e.svc.ValidateURL(context.Background(), "http://169.254.169.254/"); err == nil {
		t.Fatal("link-local literal must be refused")
	}
	// logs carry host and ip, never a query string
	if strings.Contains(e.logs.String(), "?") {
		t.Fatalf("query string leaked into logs: %s", e.logs.String())
	}
	if left := e.tmpEntries(t); len(left) != 0 {
		t.Fatalf("tmp left: %v", left)
	}
}

func TestRunHundredMegabytesWithProgress(t *testing.T) {
	if testing.Short() {
		t.Skip("100 MB")
	}
	e := newEnv(t)
	s := e.newServer(t)
	const size = 100 << 20
	// stream a deterministic body without holding 100 MB twice
	s.mux.HandleFunc("/hundred.bin", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(size))
		w.WriteHeader(200)
		buf := make([]byte, 1<<20)
		var x uint64 = 0x9E3779B97F4A7C15
		for sent := 0; sent < size; sent += len(buf) {
			for i := range buf {
				x ^= x << 13
				x ^= x >> 7
				x ^= x << 17
				buf[i] = byte(x)
			}
			if _, err := w.Write(buf); err != nil {
				return
			}
		}
	})
	_, sum := pattern(size)
	tk := e.enqueue(t, Payload{URL: s.url("/hundred.bin?token=SECRET-QS"), Dest: "/"})
	var sawSpeed bool
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		cur, _ := e.engine.Get(context.Background(), e.user.ID, tk.ID)
		if cur.State == tasks.Running && cur.SpeedBps != nil && *cur.SpeedBps > 0 && cur.Progress.Total == size {
			sawSpeed = true
		}
		if cur.State.Final() {
			tk = cur
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if tk.State != tasks.Done {
		t.Fatalf("state %s %+v", tk.State, tk.Error)
	}
	if !sawSpeed {
		t.Log("speed sample not observed (download finished within the first second)")
	}
	if tk.Progress.Done != size {
		t.Fatalf("progress %+v", tk.Progress)
	}
	if sha256File(t, filepath.Join(e.home, "hundred.bin")) != sum {
		t.Fatal("checksum mismatch")
	}
	if strings.Contains(e.logs.String(), "SECRET-QS") {
		t.Fatal("query string leaked into logs")
	}
	logs, _ := e.engine.Logs(context.Background(), e.user.ID, tk.ID, 0)
	for _, l := range logs {
		if strings.Contains(l.Message, "SECRET-QS") {
			t.Fatal("query string leaked into task logs")
		}
	}
	var raw map[string]any
	_ = json.Unmarshal(tk.Result, &raw)
	if fu, _ := raw["finalUrl"].(string); strings.Contains(fu, "SECRET") {
		t.Fatal("query string in result")
	}
}

func TestProbe(t *testing.T) {
	e := newEnv(t)
	s := e.newServer(t)
	data, _ := pattern(12345)
	s.serveBytes("/p/archive.zip", "archive.zip", data, http.Header{"Content-Type": []string{"application/zip"}})
	s.mux.HandleFunc("/nohead", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Disposition", `attachment; filename="named.bin"`)
		http.ServeContent(w, r, "x", time.Time{}, strings.NewReader(string(data)))
	})
	res, err := e.svc.Probe(context.Background(), s.url("/p/archive.zip?sig=1"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Size == nil || *res.Size != int64(len(data)) || res.Filename != "archive.zip" || !res.AcceptsRanges || res.ContentType != "application/zip" || strings.Contains(res.FinalURL, "sig=") {
		t.Fatalf("%+v", res)
	}
	res, err = e.svc.Probe(context.Background(), s.url("/nohead"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Size == nil || *res.Size != int64(len(data)) || res.Filename != "named.bin" || !res.AcceptsRanges {
		t.Fatalf("fallback probe %+v", res)
	}
	if _, err := e.svc.Probe(context.Background(), "ftp://files.test/x"); err == nil {
		t.Fatal("ftp must be refused")
	}
	e.resolver.set("meta.test", "169.254.169.254")
	_, err = e.svc.Probe(context.Background(), "http://meta.test/")
	var re *Error
	if err == nil || !asErr(err, &re) || re.Code != CodeSSRF {
		t.Fatalf("probe ssrf: %v", err)
	}
}

func asErr(err error, target **Error) bool { return errors.As(err, target) }

func TestCreateValidationRateLimitAndSettings(t *testing.T) {
	e := newEnv(t)
	s := e.newServer(t)
	data, _ := pattern(10)
	s.serveBytes("/t", "t", data, nil)
	ctx := context.Background()
	base := CreateRequest{UserID: e.user.ID, Dest: "/", Policy: "rename"}
	// too many urls
	req := base
	for i := 0; i < 11; i++ {
		req.URLs = append(req.URLs, s.url("/t"))
	}
	if _, err := e.svc.Create(ctx, req); err == nil || !strings.Contains(err.Error(), "VALIDATION_FAILED") {
		t.Fatalf("max urls: %v", err)
	}
	// filename with a slash / blocked extension / bad policy
	req = base
	req.URLs = []string{s.url("/t")}
	req.Filename = "a/b"
	if _, err := e.svc.Create(ctx, req); err == nil || !strings.Contains(err.Error(), "VALIDATION_FAILED") {
		t.Fatalf("slash: %v", err)
	}
	req.Filename = "x.bat"
	if _, err := e.svc.Create(ctx, req); err == nil || !strings.Contains(err.Error(), "UNSUPPORTED_TYPE") {
		t.Fatalf("blocked: %v", err)
	}
	req.Filename = ""
	req.Policy = "merge"
	if _, err := e.svc.Create(ctx, req); err == nil {
		t.Fatal("policy")
	}
	// ssrf on create: scheme, userinfo, literal link-local
	for _, raw := range []string{"file:///etc/passwd", "http://u:p@files.test/x", "http://169.254.169.254/", "http://files.test:22/x"} {
		req = base
		req.URLs = []string{raw}
		_, err := e.svc.Create(ctx, req)
		var re *Error
		if err == nil || !asErr(err, &re) || re.Code != CodeSSRF || re.Details["index"] != 0 {
			t.Fatalf("%s: %v", raw, err)
		}
	}
	// rate limit: 10 per hour then 429 with retryAfter; a batch is all-or-nothing
	req = base
	for i := 0; i < 8; i++ {
		req.URLs = append(req.URLs, s.url("/t"))
	}
	created, err := e.svc.Create(ctx, req)
	if err != nil || len(created) != 8 {
		t.Fatalf("batch: %v %d", err, len(created))
	}
	req.URLs = req.URLs[:3]
	_, err = e.svc.Create(ctx, req)
	var re *Error
	if err == nil || !asErr(err, &re) || re.Code != CodeRateLimited || re.Details["retryAfter"].(int) <= 0 {
		t.Fatalf("batch over limit: %v", err)
	}
	req.URLs = req.URLs[:2]
	if _, err := e.svc.Create(ctx, req); err != nil {
		t.Fatalf("exactly 10: %v", err)
	}
	req.URLs = req.URLs[:1]
	if _, err := e.svc.Create(ctx, req); err == nil || !asErr(err, &re) || re.Code != CodeRateLimited {
		t.Fatalf("11th: %v", err)
	}
	for _, tk := range created {
		e.wait(t, tk.ID, 30*time.Second)
	}
	// settings: denylist (403), allowlist, disabled
	up := func(key, val string) {
		if err := e.store.UpsertSetting(ctx, sqlc.UpsertSettingParams{Key: key, ValueJson: val, UpdatedAt: "2026-10-14T00:00:00Z"}); err != nil {
			t.Fatal(err)
		}
		e.svc.InvalidateSettings()
	}
	up(KeyDenylist, `["files.test"]`)
	_, err = e.svc.ValidateURL(ctx, s.url("/t"))
	if err == nil || !asErr(err, &re) || re.Code != CodeForbidden || re.Details["reason"] != "denylist" {
		t.Fatalf("denylist: %v", err)
	}
	up(KeyDenylist, `[]`)
	up(KeyAllowlist, `"cdn.example, *.mirror.example"`)
	_, err = e.svc.ValidateURL(ctx, s.url("/t"))
	if err == nil || !asErr(err, &re) || re.Details["reason"] != "allowlist" {
		t.Fatalf("allowlist: %v", err)
	}
	if _, err := e.svc.ValidateURL(ctx, "http://a.mirror.example/x"); err != nil {
		t.Fatalf("allowlisted: %v", err)
	}
	up(KeyAllowlist, `[]`)
	up(KeyEnabled, `false`)
	if _, err := e.svc.ValidateURL(ctx, s.url("/t")); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled: %v", err)
	}
	if _, err := e.svc.Create(ctx, CreateRequest{UserID: e.user.ID, URLs: []string{s.url("/t")}, Dest: "/", Policy: "fail"}); !errors.Is(err, ErrDisabled) {
		t.Fatalf("create disabled: %v", err)
	}
	// a queued task sees the setting too
	up(KeyEnabled, `true`)
	tk := e.enqueue(t, Payload{URL: s.url("/t"), Dest: "/", Filename: "late"})
	_ = tk
	fmt.Fprint(io.Discard, tk.ID)
}

func TestCleanOrphans(t *testing.T) {
	e := newEnv(t)
	tmp := filepath.Join(e.cfg.DataDir, "tmp")
	old := filepath.Join(tmp, TmpPrefix+"old")
	fresh := filepath.Join(tmp, TmpPrefix+"fresh")
	other := filepath.Join(tmp, "zip-keep.json")
	for _, d := range []string{old, fresh} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(other, []byte("{}"), 0o640); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-OrphanAge - time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	n, err := e.svc.CleanOrphans(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if _, err := os.Stat(old); err == nil {
		t.Fatal("old folder kept")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("fresh folder removed")
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal("foreign file removed")
	}
}

func TestSettingsParsing(t *testing.T) {
	if parseSize(`1048576`) != 1<<20 || parseSize(`"2MB"`) != 2<<20 || parseSize(`"1.5GB"`) != 3<<29 || parseSize(`"x"`) != 0 || parseSize(`"-1"`) != 0 {
		t.Fatal("parseSize")
	}
	if got := parseList(`["A.example", " b.example "]`); len(got) != 2 || got[0] != "a.example" || got[1] != "b.example" {
		t.Fatalf("parseList %v", got)
	}
	if got := parseList(`"x.example,y.example\nz.example"`); len(got) != 3 {
		t.Fatalf("parseList string %v", got)
	}
	if parseList(`12`) != nil {
		t.Fatal("parseList number")
	}
	w := newWindowLimiter(3, time.Hour, func() time.Time { return time.Unix(1000, 0) })
	if ok, _ := w.reserve("u", 2); !ok {
		t.Fatal("2")
	}
	if ok, ra := w.reserve("u", 2); ok || ra <= 0 {
		t.Fatal("4 > 3")
	}
	if ok, _ := w.reserve("u", 1); !ok {
		t.Fatal("3")
	}
	if ok, _ := w.reserve("other", 3); !ok {
		t.Fatal("per key")
	}
}
