package perf

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/fsjail"
)

const (
	gib       = int64(1) << 30
	chunkSize = 16 << 20 // UPLOAD_CHUNK_SIZE default (SPEC §3.3)
)

// result is written to FM_PERF_DIR/results.json and copied into docs/PERF.md.
type result struct {
	Label      string    `json:"label"`
	CPUs       int       `json:"cpus"`
	HostCPUs   int       `json:"hostCpus"`
	GoVersion  string    `json:"goVersion"`
	At         time.Time `json:"at"`
	Fixture    Stats     `json:"fixture"`
	ListTotal  int       `json:"listTotal"`
	PageBytes  int       `json:"pageBytes"`
	PageGzip   int       `json:"pageGzipBytes"`
	ColdStart  float64   `json:"coldStartMs"`
	ListCold   stats     `json:"listCold"`
	ListCold2  stats     `json:"listColdMtime"`
	ListWarm   stats     `json:"listWarm"`
	Pages      stats     `json:"pagesWarm"`
	PagesTotal float64   `json:"pagesTotalMs"`
	Navigation stats     `json:"navigationWarm"`
	Thumbs     struct {
		First20Ms     float64 `json:"first20Ms"`
		AllMs         float64 `json:"allMs"`
		Count         int     `json:"count"`
		PerSecond     float64 `json:"perSecond"`
		ListDuring    stats   `json:"listDuring"`
		Unsupported   int     `json:"unsupported"`
		ThumbRequests int     `json:"requests"`
	} `json:"thumbs"`
	Memory struct {
		IdleStart  memory           `json:"idleStart"`
		IdleLogin  memory           `json:"idleAfterLogin"`
		AfterList  memory           `json:"afterListing"`
		AfterThumb memory           `json:"afterThumbs"`
		Baseline   memory           `json:"baseline"`
		Upload     memory           `json:"uploadPeak"`
		Zip        memory           `json:"zipPeak"`
		Extract    memory           `json:"extractPeak"`
		IdleEnd    memory           `json:"idleEnd"`
		HeapIdle   map[string]int64 `json:"heapIdle"`
		HeapUpload map[string]int64 `json:"heapAfterUpload"`
		HeapList   map[string]int64 `json:"heapAfterListing"`
	} `json:"memory"`
	UploadMs  float64  `json:"uploadMs"`
	ZipMs     float64  `json:"zipMs"`
	ZipBytes  int64    `json:"zipBytes"`
	ExtractMs float64  `json:"extractMs"`
	Failures  []string `json:"failures"`
}

// TestPerf is the Step 21 harness (`make perf`). It needs FM_PERF_BIN (the
// built binary) and FM_PERF_DIR (a scratch folder under the repo,
// .perfdata/…, removed afterwards unless FM_PERF_KEEP=1).
func TestPerf(t *testing.T) {
	bin, dir := os.Getenv("FM_PERF_BIN"), os.Getenv("FM_PERF_DIR")
	if bin == "" || dir == "" {
		t.Skip("FM_PERF_BIN/FM_PERF_DIR not set (run `make perf`)")
	}
	cpus, _ := strconv.Atoi(os.Getenv("FM_PERF_CPUS"))
	if cpus <= 0 {
		cpus = runtime.NumCPU()
	}
	assert := os.Getenv("FM_PERF_ASSERT") != "0"
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("FM_PERF_KEEP") != "1" {
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
	}
	initial, final := "Perf-"+randomHex(8)+"-Init!", "Perf-"+randomHex(12)+"-Final!"
	srv := startServer(t, bin, dir, cpus, initial)
	res := &result{Label: os.Getenv("FM_PERF_LABEL"), CPUs: cpus, HostCPUs: runtime.NumCPU(), GoVersion: runtime.Version(), At: time.Now().UTC()}
	fail := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		res.Failures = append(res.Failures, msg)
		if assert {
			t.Error(msg)
		} else {
			t.Log("target missed: " + msg)
		}
	}
	defer func() {
		b, _ := json.MarshalIndent(res, "", "  ")
		out := os.Getenv("FM_PERF_OUT")
		if out == "" {
			out = filepath.Join(filepath.Dir(dir), "results-"+strconv.Itoa(cpus)+"cpu.json")
		}
		_ = os.WriteFile(out, b, 0o600)
		t.Logf("results → %s", out)
	}()

	time.Sleep(3 * time.Second)
	res.Memory.IdleStart = srv.mem()
	c := newClient(srv.base)
	c.login(t, initial, final)
	st, err := Generate(srv.home, DefaultSpec)
	if err != nil {
		t.Fatal(err)
	}
	res.Fixture = st
	time.Sleep(4 * time.Second) // argon2 memory release is debounced by 2 s
	res.Memory.IdleLogin = srv.mem()
	res.Memory.HeapIdle = srv.heap()
	report(t, "fixture: %d files + %d folders in %s, %d images in %s; idle RSS start %.1f MB, after login %.1f MB (anon %.1f, file %.1f)",
		st.Files, st.Folders, BigDir, st.Images, ImageDir, mb(res.Memory.IdleStart.RSS), mb(res.Memory.IdleLogin.RSS), mb(res.Memory.IdleLogin.Anon), mb(res.Memory.IdleLogin.File))
	if res.Memory.IdleLogin.RSS >= 100<<20 {
		fail("idle RSS %.1f MB ≥ 100 MB", mb(res.Memory.IdleLogin.RSS))
	}

	measureListing(t, srv, c, res, fail)
	res.Memory.AfterList = srv.mem()
	res.Memory.HeapList = srv.heap()
	report(t, "Go runtime after listing: %v", res.Memory.HeapList)
	if os.Getenv("FM_PERF_ONLY") == "listing" {
		return
	}
	measureThumbs(t, srv, c, res)
	res.Memory.AfterThumb = srv.mem()
	report(t, "RSS after listing %.1f MB (VmHWM %.1f MB), after thumbnails %.1f MB (VmHWM %.1f MB)",
		mb(res.Memory.AfterList.RSS), mb(res.Memory.AfterList.HWM), mb(res.Memory.AfterThumb.RSS), mb(res.Memory.AfterThumb.HWM))
	measureTransfers(t, srv, c, res, fail)

	time.Sleep(10 * time.Second)
	res.Memory.IdleEnd = srv.mem()
	report(t, "idle RSS after workloads + 10 s: %.1f MB (anon %.1f), VmHWM %.1f MB", mb(res.Memory.IdleEnd.RSS), mb(res.Memory.IdleEnd.Anon), mb(res.Memory.IdleEnd.HWM))
}

func report(t *testing.T, format string, args ...any) {
	t.Helper()
	msg := fmt.Sprintf(format, args...)
	t.Log(msg)
	fmt.Println("[perf] " + msg)
}

// touch changes the folder mtime from outside the server, which is how a
// cold listing happens in practice (any write in the folder; SPEC §8.3 cache
// validity is dirMtime + 15 s).
func touch(t *testing.T, home string) {
	t.Helper()
	j, err := fsjail.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = j.Close() }()
	p := fsjail.VPath(BigDir + "/.perf-touch")
	f, err := j.Create(p, false)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err := j.Remove(p); err != nil {
		t.Fatal(err)
	}
}

func listPath(dir string, extra string) string {
	return "/api/v1/files/list?path=" + url.QueryEscape(dir) + extra
}

func measureListing(t *testing.T, srv *server, c *client, res *result, fail func(string, ...any)) {
	big := listPath(BigDir, "")
	d, n, err := c.timed(big)
	if err != nil {
		t.Fatal(err)
	}
	res.ColdStart, res.PageBytes = float64(d.Microseconds())/1000, n
	var page struct {
		Total int `json:"total"`
	}
	if _, err := c.json(http.MethodGet, big, nil, &page); err != nil {
		t.Fatal(err)
	}
	res.ListTotal = page.Total
	if r, err := c.req(http.MethodGet, big, nil, map[string]string{"Accept-Encoding": "gzip"}); err == nil {
		// an explicit Accept-Encoding disables the transport's transparent gunzip: wire size
		n, _ := io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		if r.Header.Get("Content-Encoding") == "gzip" {
			res.PageGzip = int(n)
		}
	}
	if page.Total != DefaultSpec.Files+DefaultSpec.Folders-DefaultSpec.Files/101 {
		t.Logf("total %d (hidden dotfiles excluded)", page.Total)
	}

	runs := func(path string, n int, cold bool) []time.Duration {
		out := make([]time.Duration, 0, n)
		for range n {
			if cold {
				touch(t, srv.home)
			}
			d, _, err := c.timed(path)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, d)
		}
		return out
	}
	res.ListCold = summarize(runs(big, 50, true))
	res.ListCold2 = summarize(runs(listPath(BigDir, "&sort=mtime&dir=desc"), 20, true))
	runs(big, 1, false) // build the cache entry for the warm runs
	res.ListWarm = summarize(runs(big, 50, false))
	report(t, "files/list %d entries (first page %d KB JSON, %d KB gzip on the wire): first request %.1f ms; cold %s; cold sort=mtime %s; warm %s",
		res.ListTotal, res.PageBytes/1024, res.PageGzip/1024, res.ColdStart, res.ListCold, res.ListCold2, res.ListWarm)
	if res.ListCold.P95 >= 500 {
		fail("cold list p95 %.1f ms ≥ 500 ms", res.ListCold.P95)
	}
	if res.ListWarm.P95 >= 100 {
		fail("warm list p95 %.1f ms ≥ 100 ms", res.ListWarm.P95)
	}

	// infinite scroll: every page through the cursor (warm cache)
	var pages []time.Duration
	cursor, start := "", time.Now()
	for {
		path := big
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		t0 := time.Now()
		var p struct {
			NextCursor *string `json:"nextCursor"`
		}
		if _, err := c.json(http.MethodGet, path, nil, &p); err != nil {
			t.Fatal(err)
		}
		pages = append(pages, time.Since(t0))
		if p.NextCursor == nil {
			break
		}
		cursor = *p.NextCursor
	}
	res.PagesTotal = float64(time.Since(start).Microseconds()) / 1000
	res.Pages = summarize(pages)
	report(t, "all %d pages via cursor: %.0f ms total, per page %s", len(pages), res.PagesTotal, res.Pages)

	// repeated navigation between visited folders (warm)
	nav := []string{"/", BigDir + "/folder-1", BigDir + "/folder-2", ImageDir, BigDir, BigDir + "/folder-3"}
	for _, p := range nav {
		if _, _, err := c.timed(listPath(p, "")); err != nil {
			t.Fatal(err)
		}
	}
	var navs []time.Duration
	for i := range 120 {
		d, _, err := c.timed(listPath(nav[i%len(nav)], ""))
		if err != nil {
			t.Fatal(err)
		}
		navs = append(navs, d)
	}
	res.Navigation = summarize(navs)
	report(t, "navigation between %d visited folders (warm): %s", len(nav), res.Navigation)
}

func measureThumbs(t *testing.T, srv *server, c *client, res *result) {
	var page struct {
		Items []struct {
			Path string `json:"path"`
		} `json:"items"`
	}
	if _, err := c.json(http.MethodGet, listPath(ImageDir, "&limit=500"), nil, &page); err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, len(page.Items))
	for _, it := range page.Items {
		paths = append(paths, it.Path)
	}
	var signed struct {
		URLs []struct {
			URL string `json:"url"`
		} `json:"urls"`
	}
	if _, err := c.json(http.MethodPost, "/api/v1/files/sign", map[string]any{"paths": paths, "purpose": "thumb", "size": 256}, &signed); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	requests, unsupported := 0, 0
	fetch := func(u string) {
		for range 600 {
			resp, err := c.req(http.MethodGet, u, nil, nil)
			if err != nil {
				t.Error(err)
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			mu.Lock()
			requests++
			mu.Unlock()
			switch resp.StatusCode {
			case http.StatusOK:
				return
			case http.StatusAccepted:
				time.Sleep(200 * time.Millisecond) // the UI waits Retry-After (2 s); poll faster to time the queue
			case http.StatusNotFound:
				mu.Lock()
				unsupported++
				mu.Unlock()
				return
			default:
				t.Errorf("thumb %d", resp.StatusCode)
				return
			}
		}
	}
	// a listing keeps answering while the single worker renders: the UI never waits for thumbnails
	stop := make(chan struct{})
	var during []time.Duration
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			case <-time.After(100 * time.Millisecond):
			}
			path := listPath(BigDir, "")
			if i%2 == 0 {
				path = listPath(BigDir+"/folder-"+strconv.Itoa(i%400+1), "")
			}
			d, _, err := c.timed(path)
			if err == nil {
				during = append(during, d)
			}
		}
	})
	start := time.Now()
	first := min(20, len(signed.URLs))
	parallel(first, 20, func(i int) { fetch(signed.URLs[i].URL) })
	res.Thumbs.First20Ms = float64(time.Since(start).Microseconds()) / 1000
	parallel(len(signed.URLs)-first, 20, func(i int) { fetch(signed.URLs[first+i].URL) })
	all := time.Since(start)
	close(stop)
	wg.Wait()
	res.Thumbs.AllMs = float64(all.Microseconds()) / 1000
	res.Thumbs.Count = len(signed.URLs)
	res.Thumbs.PerSecond = float64(len(signed.URLs)) / all.Seconds()
	res.Thumbs.ListDuring = summarize(during)
	res.Thumbs.Unsupported, res.Thumbs.ThumbRequests = unsupported, requests
	report(t, "thumbnails: first 20 (parallel) ready in %.0f ms, all %d in %.0f ms = %.1f/s (%d requests, %d unsupported); files/list meanwhile %s",
		res.Thumbs.First20Ms, res.Thumbs.Count, res.Thumbs.AllMs, res.Thumbs.PerSecond, requests, unsupported, res.Thumbs.ListDuring)
}

func tusMeta(md map[string]string) string {
	parts := make([]string, 0, len(md))
	for k, v := range md {
		parts = append(parts, k+" "+base64.StdEncoding.EncodeToString([]byte(v)))
	}
	return strings.Join(parts, ",")
}

func measureTransfers(t *testing.T, srv *server, c *client, res *result, fail func(string, ...any)) {
	time.Sleep(3 * time.Second)
	res.Memory.Baseline = srv.mem()
	limit := res.Memory.Baseline.RSS + 100<<20

	// 1 GiB tus upload in 16 MiB PATCHes
	sm := srv.sample()
	start := time.Now()
	resp, err := c.req(http.MethodPost, "/api/v1/upload/tus/", nil, map[string]string{
		"Tus-Resumable": "1.0.0", "Upload-Length": strconv.FormatInt(gib, 10),
		"Upload-Metadata": tusMeta(map[string]string{"filename": "besar-1g.bin", "path": "/", "onConflict": "rename"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	loc := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusCreated || loc == "" {
		t.Fatalf("tus create: %d %q", resp.StatusCode, loc)
	}
	src := Stream(gib, 7)
	for off := int64(0); off < gib; off += chunkSize {
		n := min(chunkSize, gib-off)
		r, err := c.req(http.MethodPatch, loc, io.LimitReader(src, n), map[string]string{
			"Tus-Resumable": "1.0.0", "Content-Type": "application/offset+octet-stream",
			"Upload-Offset": strconv.FormatInt(off, 10), "Content-Length": strconv.FormatInt(n, 10),
		})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		if r.StatusCode != http.StatusNoContent {
			t.Fatalf("tus patch at %d: %d", off, r.StatusCode)
		}
	}
	res.UploadMs = float64(time.Since(start).Microseconds()) / 1000
	res.Memory.Upload = sm.end()
	res.Memory.HeapUpload = srv.heap()
	report(t, "upload 1 GiB (tus, 16 MiB chunks): %.1f s = %.0f MB/s; RSS peak %.1f MB (baseline %.1f MB, %+.1f MB)",
		res.UploadMs/1000, 1024/(res.UploadMs/1000), mb(res.Memory.Upload.RSS), mb(res.Memory.Baseline.RSS), mb(res.Memory.Upload.RSS-res.Memory.Baseline.RSS))
	if res.Memory.Upload.RSS > limit {
		fail("upload RSS peak %.1f MB > baseline + 100 MB (%.1f MB)", mb(res.Memory.Upload.RSS), mb(limit))
	}

	// 1 GiB zip stream of that file
	var z struct {
		URL string `json:"url"`
	}
	if _, err := c.json(http.MethodPost, "/api/v1/files/zip", map[string]any{"paths": []string{"/besar-1g.bin"}, "method": "store"}, &z); err != nil {
		t.Fatal(err)
	}
	sm = srv.sample()
	start = time.Now()
	r, err := c.req(http.MethodGet, z.URL, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	res.ZipBytes, _ = io.Copy(io.Discard, r.Body)
	_ = r.Body.Close()
	res.ZipMs = float64(time.Since(start).Microseconds()) / 1000
	res.Memory.Zip = sm.end()
	report(t, "zip stream 1 GiB: %d bytes in %.1f s = %.0f MB/s; RSS peak %.1f MB (%+.1f MB vs baseline)",
		res.ZipBytes, res.ZipMs/1000, mb(res.ZipBytes)/(res.ZipMs/1000), mb(res.Memory.Zip.RSS), mb(res.Memory.Zip.RSS-res.Memory.Baseline.RSS))
	if r.StatusCode != http.StatusOK || res.ZipBytes < gib {
		t.Fatalf("zip: %d, %d bytes", r.StatusCode, res.ZipBytes)
	}
	if res.Memory.Zip.RSS > limit {
		fail("zip RSS peak %.1f MB > baseline + 100 MB (%.1f MB)", mb(res.Memory.Zip.RSS), mb(limit))
	}

	// 1 GiB extract (Store zip, 8 members) as a task
	if err := WriteStoreZip(srv.home, "/arsip-1g.zip", gib, 8); err != nil {
		t.Fatal(err)
	}
	var acc struct {
		Task struct {
			ID string `json:"id"`
		} `json:"task"`
	}
	sm = srv.sample()
	start = time.Now()
	if _, err := c.json(http.MethodPost, "/api/v1/archives/extract", map[string]any{"path": "/arsip-1g.zip", "dest": "new"}, &acc); err != nil {
		t.Fatal(err)
	}
	for {
		var got struct {
			Task struct {
				State string `json:"state"`
				Error any    `json:"error"`
			} `json:"task"`
		}
		if _, err := c.json(http.MethodGet, "/api/v1/tasks/"+acc.Task.ID, nil, &got); err != nil {
			t.Fatal(err)
		}
		task := got.Task
		if task.State == "done" {
			break
		}
		if task.State == "failed" || task.State == "cancelled" || task.State == "interrupted" {
			t.Fatalf("extract task %s: %v", task.State, task.Error)
		}
		time.Sleep(100 * time.Millisecond)
	}
	res.ExtractMs = float64(time.Since(start).Microseconds()) / 1000
	res.Memory.Extract = sm.end()
	report(t, "extract 1 GiB zip (task): %.1f s = %.0f MB/s; RSS peak %.1f MB (%+.1f MB vs baseline)",
		res.ExtractMs/1000, 1024/(res.ExtractMs/1000), mb(res.Memory.Extract.RSS), mb(res.Memory.Extract.RSS-res.Memory.Baseline.RSS))
	if res.Memory.Extract.RSS > limit {
		fail("extract RSS peak %.1f MB > baseline + 100 MB (%.1f MB)", mb(res.Memory.Extract.RSS), mb(limit))
	}
}
