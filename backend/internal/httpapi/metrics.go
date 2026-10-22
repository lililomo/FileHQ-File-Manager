package httpapi

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"kppbwi.app/filemanager/internal/metrics"
)

// Metrics records request count/latency by method and status class into
// reg (SPEC §6.3 "/metrics"). A nil registry disables the middleware.
func Metrics(reg *metrics.Registry) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if reg == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww, ok := w.(middleware.WrapResponseWriter)
			if !ok {
				ww = middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			}
			reg.Inflight(1)
			defer func() {
				reg.Inflight(-1)
				status := ww.Status()
				if status == 0 {
					status = http.StatusOK
				}
				reg.ObserveRequest(r.Method, status, time.Since(start))
			}()
			next.ServeHTTP(ww, r)
		})
	}
}
