// Package metrics exposes a small Prometheus text-format endpoint (SPEC §6.3
// "/metrics", §3 METRICS_ADDR) without a client library: request counters
// and latency sums by method/status class, task and SSE gauges. Labels never
// contain user data (no paths, users or request IDs).
package metrics

import (
	"fmt"
	"io"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Gauge is a callback evaluated at scrape time.
type Gauge func() float64

// Registry collects the application metrics.
type Registry struct {
	start    time.Time
	mu       sync.Mutex
	requests map[reqKey]*reqStats
	gauges   []gaugeDef
	inflight atomic.Int64
}

type reqKey struct {
	method string
	class  string // "2xx", "4xx" ...
}

type reqStats struct {
	count       uint64
	durationSum float64
}

type gaugeDef struct {
	name, help string
	fn         Gauge
}

// New builds an empty registry.
func New() *Registry {
	return &Registry{start: time.Now(), requests: map[reqKey]*reqStats{}}
}

// Gauge registers a gauge evaluated on every scrape.
func (r *Registry) Gauge(name, help string, fn Gauge) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gauges = append(r.gauges, gaugeDef{name: name, help: help, fn: fn})
}

// ObserveRequest records one finished HTTP request.
func (r *Registry) ObserveRequest(method string, status int, d time.Duration) {
	k := reqKey{method: normMethod(method), class: strconv.Itoa(status/100) + "xx"}
	r.mu.Lock()
	st := r.requests[k]
	if st == nil {
		st = &reqStats{}
		r.requests[k] = st
	}
	st.count++
	st.durationSum += d.Seconds()
	r.mu.Unlock()
}

// Inflight tracks requests currently being served.
func (r *Registry) Inflight(delta int64) { r.inflight.Add(delta) }

func normMethod(m string) string {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions:
		return m
	}
	return "OTHER"
}

// ServeHTTP renders the text exposition format (version 0.0.4).
func (r *Registry) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	r.Render(w)
}

// Render writes the exposition to w.
func (r *Registry) Render(w io.Writer) {

	r.mu.Lock()
	keys := make([]reqKey, 0, len(r.requests))
	for k := range r.requests {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].method != keys[j].method {
			return keys[i].method < keys[j].method
		}
		return keys[i].class < keys[j].class
	})
	type row struct {
		k reqKey
		s reqStats
	}
	rows := make([]row, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, row{k, *r.requests[k]})
	}
	gauges := append([]gaugeDef(nil), r.gauges...)
	r.mu.Unlock()

	pf(w, "%s\n", "# HELP filemanager_http_requests_total HTTP requests by method and status class.")
	pf(w, "%s\n", "# TYPE filemanager_http_requests_total counter")
	for _, rw := range rows {
		pf(w, "filemanager_http_requests_total{method=%q,class=%q} %d\n", rw.k.method, rw.k.class, rw.s.count)
	}
	pf(w, "%s\n", "# HELP filemanager_http_request_duration_seconds_sum Total request time by method and status class.")
	pf(w, "%s\n", "# TYPE filemanager_http_request_duration_seconds_sum counter")
	for _, rw := range rows {
		pf(w, "filemanager_http_request_duration_seconds_sum{method=%q,class=%q} %s\n", rw.k.method, rw.k.class, fmtFloat(rw.s.durationSum))
	}
	pf(w, "%s\n", "# HELP filemanager_http_requests_inflight Requests currently being served.")
	pf(w, "%s\n", "# TYPE filemanager_http_requests_inflight gauge")
	pf(w, "filemanager_http_requests_inflight %d\n", r.inflight.Load())
	for _, g := range gauges {
		pf(w, "# HELP %s %s\n# TYPE %s gauge\n%s %s\n", g.name, g.help, g.name, g.name, fmtFloat(g.fn()))
	}
	pf(w, "%s\n", "# HELP process_start_time_seconds Start time of the process since unix epoch in seconds.")
	pf(w, "%s\n", "# TYPE process_start_time_seconds gauge")
	pf(w, "process_start_time_seconds %d\n", r.start.Unix())
	pf(w, "%s\n", "# HELP go_goroutines Number of goroutines that currently exist.")
	pf(w, "%s\n", "# TYPE go_goroutines gauge")
	pf(w, "go_goroutines %d\n", runtime.NumGoroutine())
}

func fmtFloat(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

// pf writes one exposition line; write errors on a scrape are not actionable.
func pf(w io.Writer, format string, args ...any) { _, _ = fmt.Fprintf(w, format, args...) }
