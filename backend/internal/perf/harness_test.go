package perf

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// server is the real binary started on a scratch directory (never the
// production STORAGE_ROOT/.env: everything lives under FM_PERF_DIR).
type server struct {
	cmd     *exec.Cmd
	base    string
	metrics string
	storage string
	home    string
	log     *bytes.Buffer
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// startServer launches bin serve; cpus == 1 pins it to CPU 0 with taskset
// (the original 1 vCPU target, SPEC §1.6) and GOMAXPROCS=1.
func startServer(t *testing.T, bin, dir string, cpus int, adminPassword string) *server {
	t.Helper()
	storage := filepath.Join(dir, "storage")
	if err := os.MkdirAll(filepath.Join(storage, ".system"), 0o750); err != nil {
		t.Fatal(err)
	}
	port, mport := freePort(t), freePort(t)
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + dir,
		"APP_ENV=development", // plain HTTP on loopback: cookies without Secure
		"ALLOW_ROOT=true",
		"APP_BASE_URL=" + base,
		"LISTEN_ADDR=127.0.0.1:" + strconv.Itoa(port),
		"METRICS_ADDR=127.0.0.1:" + strconv.Itoa(mport),
		"TRUSTED_PROXIES=127.0.0.1",
		"STORAGE_ROOT=" + storage,
		"DATA_DIR=" + filepath.Join(storage, ".system"),
		"APP_SECRET=" + randomHex(32),
		"ADMIN_USERNAME=admin",
		"ADMIN_INITIAL_PASSWORD=" + adminPassword,
		"DEFAULT_USER_QUOTA=50GB",
		"LOG_LEVEL=warn",
		"GOMAXPROCS=" + strconv.Itoa(cpus),
		"GOMEMLIMIT=512MiB", // as deploy/filemanager.service (SPEC §1.6)
		// the harness measures latency, not the per-session budgets of
		// SPEC §10.5 (a real client never lists 50× per second)
		"RATE_LIMIT_API=off", "RATE_LIMIT_FILES_LIST=off", "RATE_LIMIT_FILES_SIGN=off",
		"RATE_LIMIT_THUMB=off", "RATE_LIMIT_CSRF=off", "RATE_LIMIT_TUS_CREATE=off",
	}
	args := []string{bin, "serve"}
	if cpus == 1 {
		args = append([]string{"taskset", "-c", "0"}, args...)
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = env
	cmd.Dir = dir
	logBuf := &bytes.Buffer{}
	cmd.Stdout, cmd.Stderr = logBuf, logBuf
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s := &server{cmd: cmd, base: base, metrics: "http://127.0.0.1:" + strconv.Itoa(mport), storage: storage, home: filepath.Join(storage, "admin"), log: logBuf}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
		}
	})
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/healthz") //nolint:noctx // test
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return s
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("server did not start: %s", logBuf.String())
	return nil
}

// memory is one /proc sample of the server process.
type memory struct {
	RSS, Anon, File, HWM int64 // bytes
}

func (s *server) mem() memory {
	f, err := os.Open(fmt.Sprintf("/proc/%d/status", s.cmd.Process.Pid))
	if err != nil {
		return memory{}
	}
	defer func() { _ = f.Close() }()
	var m memory
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		n, _ := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(v), " kB"), 10, 64)
		switch k {
		case "VmRSS":
			m.RSS = n << 10
		case "RssAnon":
			m.Anon = n << 10
		case "RssFile":
			m.File = n << 10
		case "VmHWM":
			m.HWM = n << 10
		}
	}
	return m
}

// heap reads the Go runtime gauges exported on /metrics (Step 21).
func (s *server) heap() map[string]int64 {
	resp, err := http.Get(s.metrics + "/metrics") //nolint:noctx // test
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	out := map[string]int64{}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "go_heap_") && !strings.HasPrefix(line, "go_memory_") {
			continue
		}
		k, v, _ := strings.Cut(line, " ")
		n, _ := strconv.ParseInt(v, 10, 64)
		out[k] = n
	}
	return out
}

// sampler records the peak RSS while a workload runs.
type sampler struct {
	stop chan struct{}
	done chan struct{}
	peak memory
}

func (s *server) sample() *sampler {
	sm := &sampler{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(sm.done)
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		for {
			m := s.mem()
			if m.RSS > sm.peak.RSS {
				sm.peak = m
			}
			select {
			case <-sm.stop:
				return
			case <-tick.C:
			}
		}
	}()
	return sm
}

func (sm *sampler) end() memory {
	close(sm.stop)
	<-sm.done
	return sm.peak
}

// client is a browser-like API client: one keep-alive connection pool,
// cookies, CSRF + Origin on every request and transparent gzip.
type client struct {
	base string
	hc   *http.Client
	csrf string
}

func newClient(base string) *client {
	jar, _ := cookiejar.New(nil)
	return &client{base: base, hc: &http.Client{Jar: jar, Transport: &http.Transport{MaxIdleConnsPerHost: 32}}}
}

func (c *client) req(method, path string, body io.Reader, hdr map[string]string) (*http.Response, error) {
	u := path
	if strings.HasPrefix(path, "/") {
		u = c.base + path
	}
	req, err := http.NewRequest(method, u, body) //nolint:noctx // test
	if err != nil {
		return nil, err
	}
	req.Header.Set("Origin", c.base)
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return c.hc.Do(req)
}

// json sends v (nil = no body) and decodes the answer into out (may be nil).
func (c *client) json(method, path string, v, out any) (int, error) {
	var body io.Reader
	hdr := map[string]string{}
	if v != nil {
		b, _ := json.Marshal(v)
		body = bytes.NewReader(b)
		hdr["Content-Type"] = "application/json"
	}
	resp, err := c.req(method, path, body, hdr)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, err
	}
	if out != nil && len(b) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			return resp.StatusCode, fmt.Errorf("decode %s %s (%d): %w: %.200s", method, path, resp.StatusCode, err, b)
		}
	}
	if resp.StatusCode >= 400 {
		return resp.StatusCode, fmt.Errorf("%s %s: %d %.300s", method, path, resp.StatusCode, b)
	}
	return resp.StatusCode, nil
}

// timed GETs path, reads the whole body and returns the wall time.
func (c *client) timed(path string) (time.Duration, int, error) {
	start := time.Now()
	resp, err := c.req(http.MethodGet, path, nil, nil)
	if err != nil {
		return 0, 0, err
	}
	n, err := io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	d := time.Since(start)
	if resp.StatusCode != http.StatusOK {
		return d, int(n), fmt.Errorf("GET %s: %d", path, resp.StatusCode)
	}
	return d, int(n), err
}

// login signs in as admin and replaces the initial password (SPEC §8.2).
func (c *client) login(t *testing.T, initial, final string) {
	t.Helper()
	var res struct {
		CSRFToken string `json:"csrfToken"`
	}
	if _, err := c.json(http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "admin", "password": initial}, &res); err != nil {
		t.Fatal(err)
	}
	c.csrf = res.CSRFToken
	if _, err := c.json(http.MethodPost, "/api/v1/auth/change-password", map[string]string{"currentPassword": initial, "newPassword": final}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.json(http.MethodGet, "/api/v1/auth/csrf", nil, &res); err != nil {
		t.Fatal(err)
	}
	c.csrf = res.CSRFToken
}

// stats summarises latencies in milliseconds.
type stats struct {
	N                  int
	Min, P50, P95, P99 float64
	Max, Mean          float64
}

func summarize(ds []time.Duration) stats {
	if len(ds) == 0 {
		return stats{}
	}
	s := slices.Clone(ds)
	slices.Sort(s)
	ms := func(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
	q := func(p int) float64 { return ms(s[min(len(s)-1, (len(s)*p+99)/100-1)]) }
	var sum time.Duration
	for _, d := range s {
		sum += d
	}
	return stats{N: len(s), Min: ms(s[0]), P50: q(50), P95: q(95), P99: q(99), Max: ms(s[len(s)-1]), Mean: ms(sum / time.Duration(len(s)))}
}

func (s stats) String() string {
	return fmt.Sprintf("n=%d p50=%.1f p95=%.1f p99=%.1f max=%.1f ms", s.N, s.P50, s.P95, s.P99, s.Max)
}

// parallel runs fn(i) for i in [0,n) with at most width goroutines.
func parallel(n, width int, fn func(i int)) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, width)
	for i := range n {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			fn(i)
		})
	}
	wg.Wait()
}

func mb(b int64) float64 { return float64(b) / (1 << 20) }
