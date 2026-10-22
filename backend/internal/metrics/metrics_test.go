package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRegistryExposition(t *testing.T) {
	reg := New()
	reg.ObserveRequest(http.MethodGet, 200, 10*time.Millisecond)
	reg.ObserveRequest(http.MethodGet, 404, 5*time.Millisecond)
	reg.ObserveRequest(http.MethodGet, 200, 20*time.Millisecond)
	reg.ObserveRequest("PROPFIND", 500, time.Millisecond)
	reg.Gauge("filemanager_tasks_running", "Tasks currently executing.", func() float64 { return 2 })
	reg.Inflight(1)

	rec := httptest.NewRecorder()
	reg.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain; version=0.0.4") {
		t.Fatalf("content type %q", ct)
	}
	for _, want := range []string{
		`filemanager_http_requests_total{method="GET",class="2xx"} 2`,
		`filemanager_http_requests_total{method="GET",class="4xx"} 1`,
		`filemanager_http_requests_total{method="OTHER",class="5xx"} 1`,
		`filemanager_http_request_duration_seconds_sum{method="GET",class="2xx"} 0.03`,
		"filemanager_http_requests_inflight 1",
		"# TYPE filemanager_tasks_running gauge",
		"filemanager_tasks_running 2",
		"process_start_time_seconds ",
		"go_goroutines ",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
	// labels never carry request-specific data
	for _, forbidden := range []string{"path=", "user", "request_id"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("exposition leaks %q", forbidden)
		}
	}
}
