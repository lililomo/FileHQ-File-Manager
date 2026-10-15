package upload

import (
	"net/http"
	"os"
	"strconv"
	"testing"

	"kppbwi.app/filemanager/internal/fsjail"
)

// Review C probes (PROMPT Step R-C checklist a).

// TestReviewCFinalizeAfterLockConflict: when the final PATCH fails in the
// pre-finish hook with a retryable 423 (another operation holds the target),
// tusd has already stored the last chunk. tusd answers every later PATCH /
// HEAD of a complete upload without calling the hook again, so the retry
// tus-js-client performs must still land the file (Upload-Result present,
// session finalized, bytes in place, no 24 h zombie in DATA_DIR/uploads).
func TestReviewCFinalizeAfterLockConflict(t *testing.T) {
	e := newEnv(t, 0)
	e.mkdir("in")
	body := content(3 << 20)
	loc, res := e.create(int64(len(body)), map[string]string{"filename": "locked.bin", "path": "/in"})
	if res.status != http.StatusCreated {
		t.Fatalf("create: %d %s", res.status, res.raw)
	}
	id := e.idOf(loc)
	if r := e.patch(loc, 0, body[:2<<20]); r.status != http.StatusNoContent {
		t.Fatalf("chunk 1: %d %s", r.status, r.raw)
	}
	release, err := e.svc.o.Locks.TryAcquire(e.user.ID, fsjail.Write, "review-c", fsjail.VPath("/in/locked.bin"))
	if err != nil {
		t.Fatal(err)
	}
	last := e.patch(loc, 2<<20, body[2<<20:])
	if last.status != http.StatusLocked {
		t.Fatalf("final chunk under a foreign lock: want 423, got %d %s", last.status, last.raw)
	}
	if _, err := os.Stat(e.home("in", "locked.bin")); err == nil {
		t.Fatal("file landed although finalisation failed")
	}
	release()
	// tus-js-client retries with HEAD (offset == length → it reports success
	// to the application) and/or an empty PATCH at the final offset
	head := e.do(http.MethodHead, e.rel(loc), nil, nil)
	if head.status != http.StatusOK || head.header.Get("Upload-Offset") != strconv.Itoa(len(body)) {
		t.Fatalf("HEAD after conflict: %d %v", head.status, head.header)
	}
	if head.header.Get(HeaderResult) == "" {
		// the retry PATCH is the other path a client takes
		again := e.patch(loc, int64(len(body)), nil)
		if again.status != http.StatusNoContent {
			t.Fatalf("empty PATCH at the final offset: %d %s", again.status, again.raw)
		}
		if again.header.Get(HeaderResult) == "" {
			t.Fatalf("neither HEAD nor PATCH finalised the complete upload (headers %v / %v)", head.header, again.header)
		}
	}
	got, err := os.ReadFile(e.home("in", "locked.bin"))
	if err != nil || string(got) != string(body) {
		t.Fatalf("file not finalised after the lock was released: %v", err)
	}
	if st := e.session(id).State; st != StateFinalized {
		t.Fatalf("session state %q", st)
	}
	if names := e.uploadsDirEntries(); len(names) != 0 {
		t.Fatalf("partial upload left behind: %v", names)
	}
	if u := e.usage(); u.Reserved != 0 || u.Used != int64(len(body)) {
		t.Fatalf("quota after finalisation: %+v", u)
	}
	// once finalised, the id is gone for the protocol (no second landing)
	if r := e.do(http.MethodHead, e.rel(loc), nil, nil); r.status != http.StatusNotFound && r.status != http.StatusGone {
		t.Fatalf("HEAD of a finalised upload: %d", r.status)
	}
}

// TestReviewCMetadataCannotLeaveRootOrTargetShares: destination/relative
// paths with ".." and share uploads (Step 18a) are refused at creation.
func TestReviewCMetadataCannotLeaveRootOrTargetShares(t *testing.T) {
	e := newEnv(t, 0)
	e.mkdir("in")
	cases := []struct {
		md   map[string]string
		code string
	}{
		{map[string]string{"filename": "x.txt", "path": "/in/../../../etc"}, CodePathInvalid},
		{map[string]string{"filename": "x.txt", "path": "/.system"}, CodePathInvalid},
		{map[string]string{"filename": "x.txt", "path": "/in", "relativePath": "../../up"}, CodeNameInvalid},
		{map[string]string{"filename": "x.txt", "path": "/in", "relativePath": "a/..\\b"}, CodeNameInvalid},
		{map[string]string{"filename": "../x.txt", "path": "/in"}, CodeNameInvalid},
		{map[string]string{"filename": "x.txt", "path": "/in", "share": "abcdef"}, CodeValidationFailed},
		{map[string]string{"filename": "x.txt", "path": "/in", "lastModified": "-5"}, CodeValidationFailed},
	}
	for _, c := range cases {
		_, res := e.create(10, c.md)
		if res.status < 400 || res.status >= 500 {
			t.Fatalf("%v: want 4xx, got %d %s", c.md, res.status, res.raw)
		}
		if got, _ := res.body["code"].(string); got != c.code {
			t.Fatalf("%v: want %s, got %s (%s)", c.md, c.code, got, res.raw)
		}
	}
	if names := e.uploadsDirEntries(); len(names) != 0 {
		t.Fatalf("refused creations left files: %v", names)
	}
	if u := e.usage(); u.Reserved != 0 {
		t.Fatalf("refused creations reserved quota: %+v", u)
	}
}

// TestReviewCTerminateReleasesQuota: DELETE on an active upload frees its
// durable reservation and its bytes.
func TestReviewCTerminateReleasesQuota(t *testing.T) {
	e := newEnv(t, 8<<20)
	loc, res := e.create(5<<20, map[string]string{"filename": "big.bin", "path": "/"})
	if res.status != http.StatusCreated {
		t.Fatalf("create: %d %s", res.status, res.raw)
	}
	if r := e.patch(loc, 0, content(1<<20)); r.status != http.StatusNoContent {
		t.Fatalf("chunk: %d", r.status)
	}
	if u := e.usage(); u.Reserved != 5<<20 {
		t.Fatalf("reserved before terminate: %+v", u)
	}
	// a second upload that would overflow the quota is refused while the first is active
	if _, r := e.create(4<<20, map[string]string{"filename": "second.bin", "path": "/"}); r.status != http.StatusRequestEntityTooLarge {
		t.Fatalf("second upload over the reservation: %d %s", r.status, r.raw)
	}
	if r := e.do(http.MethodDelete, e.rel(loc), nil, nil); r.status != http.StatusNoContent {
		t.Fatalf("terminate: %d %s", r.status, r.raw)
	}
	if u := e.usage(); u.Reserved != 0 || u.Used != 0 {
		t.Fatalf("quota after terminate: %+v", u)
	}
	if st := e.session(e.idOf(loc)).State; st != StateAborted {
		t.Fatalf("session state %q", st)
	}
	if names := e.uploadsDirEntries(); len(names) != 0 {
		t.Fatalf("terminated upload left files: %v", names)
	}
	if _, r := e.create(4<<20, map[string]string{"filename": "second.bin", "path": "/"}); r.status != http.StatusCreated {
		t.Fatalf("second upload after terminate: %d %s", r.status, r.raw)
	}
}
