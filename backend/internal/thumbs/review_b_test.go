package thumbs

import (
	"context"
	"strings"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/media"
)

// Review B (checklist g): a tool failure message carries the absolute host
// path it was given; the stored thumb_error (and the log line built from
// the same text) must not reveal STORAGE_ROOT.
func TestReviewBThumbFailureHidesHostPath(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(e.ctx)
	defer cancel()
	e.svc.Start(ctx)
	info := e.write(t, "rusak.jpg", []byte("this is not a jpeg at all"))
	if st := e.svc.Request(ctx, Job{UserID: e.userID, Username: e.user, Path: info.Path}, info, Size128); st == StatusReady {
		t.Fatal("a corrupt file cannot be ready")
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if row, ok := e.svc.media.Get(e.ctx, e.userID, info); ok && row.ThumbState == media.ThumbFailed {
			msg := deref(row.ThumbError)
			if msg == "" {
				t.Fatal("thumb_error must explain the failure")
			}
			if strings.Contains(msg, e.storage) {
				t.Fatalf("thumb_error leaks the host path: %s", msg)
			}
			if !strings.Contains(msg, "<root>/rusak.jpg") {
				t.Fatalf("expected the redacted path in the message: %s", msg)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("thumbnail job did not fail in time")
}
