package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/sse"
)

// EventsPath is the SSE endpoint (excluded from the API timeout).
const EventsPath = "/api/v1/events"

// mountEvents registers GET /events (SPEC §6.4): session + password gate,
// no CSRF (GET), streaming with heartbeat and Last-Event-ID replay.
func (s *Server) mountEvents(api chi.Router) {
	if s.events == nil {
		return
	}
	api.Group(func(g chi.Router) {
		g.Use(s.RequireAuth)
		g.Use(RequirePasswordChanged)
		g.Get("/events", s.handleEvents)
	})
}

// TimeoutExcept is middleware.Timeout for every path but the streaming ones
// (exact paths, or prefixes when the entry ends in "/").
func TimeoutExcept(d time.Duration, except ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, p := range except {
				// an entry ending in "/" is a prefix (tus: /api/v1/upload/tus/…)
				if r.URL.Path == p || (strings.HasSuffix(p, "/") && strings.HasPrefix(r.URL.Path, p)) {
					next.ServeHTTP(w, r)
					return
				}
			}
			ctx, cancel := contextWithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())
	rc := http.NewResponseController(w)
	last := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if last == "" {
		last = strings.TrimSpace(r.URL.Query().Get("lastEventId"))
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	h.Del("Content-Length")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("retry: 3000\n\n")); err != nil {
		return
	}
	_ = rc.Flush()
	sub := s.events.Subscribe(u.ID, last)
	defer sub.Close()
	write := func(ev sse.Event) bool {
		if err := sse.Write(w, ev); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	if sub.Resync {
		if !write(sse.Event{Name: "resync", Data: map[string]any{}}) {
			return
		}
	}
	for _, ev := range sub.Replay {
		if !write(ev) {
			return
		}
	}
	heartbeat := time.NewTicker(sse.Heartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
		case ev, ok := <-sub.Events:
			if !ok {
				// dropped (cap, slow consumer, shutdown): the client reconnects
				s.logger.DebugContext(r.Context(), "sse client dropped", slog.String("user", u.ID))
				return
			}
			if !write(ev) {
				return
			}
		}
	}
}

func contextWithTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, d)
}
