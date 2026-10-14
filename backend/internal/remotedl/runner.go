package remotedl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/netguard"
	"kppbwi.app/filemanager/internal/quota"
	"kppbwi.app/filemanager/internal/tasks"
)

const (
	partName = "part"
	metaName = "meta.json"
	rootName = "root"
)

func decodePayload(raw json.RawMessage) (Payload, error) {
	var p Payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, newErr(CodeValidation, "bad payload", http.StatusBadRequest, map[string]any{"reason": "bad_payload"})
	}
	if p.Policy == "" {
		p.Policy = string(files.PolicyFail)
	}
	return p, nil
}

// partMeta describes the part file so a later attempt or retry task can
// resume with Range (SPEC §8.10 step 6/8).
type partMeta struct {
	URL          string `json:"url"`
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"lastModified,omitempty"`
	Total        int64  `json:"total"`
	AcceptRanges bool   `json:"acceptRanges"`
}

// workspace is DATA_DIR/tmp/rd-<root>: the part + meta live in the root
// task's folder; a retry task records that root in its own folder.
type workspace struct {
	data *fsjail.SysDir
	own  *fsjail.SysDir
	root string
	id   string
	dir  string
}

func (s *Service) openWork(rt *tasks.Runtime) (*workspace, error) {
	id := rt.Task.ID
	own, err := fsjail.OpenSys(s.o.DataDir, "tmp/"+TmpPrefix+id)
	if err != nil {
		return nil, err
	}
	root := id
	if prev := rt.Task.RetryOf; prev != "" {
		if r := s.readRoot(prev); r != "" {
			root = r
		}
	}
	if root != id {
		if err := writeSmall(own, rootName, root); err != nil {
			_ = own.Close()
			return nil, err
		}
	}
	data := own
	if root != id {
		data, err = fsjail.OpenSys(s.o.DataDir, "tmp/"+TmpPrefix+root)
		if err != nil {
			_ = own.Close()
			return nil, err
		}
	}
	return &workspace{data: data, own: own, root: root, id: id, dir: s.o.DataDir}, nil
}

func (s *Service) readRoot(taskID string) string {
	sys, err := fsjail.OpenSys(s.o.DataDir, "tmp/"+TmpPrefix+taskID)
	if err != nil {
		return ""
	}
	defer func() { _ = sys.Close() }()
	if b, err := readSmall(sys, rootName); err == nil && b != "" {
		return b
	}
	if _, err := sys.Lstat(partName); err == nil {
		return taskID
	}
	return ""
}

func writeSmall(sys *fsjail.SysDir, name, content string) error {
	_ = sys.Remove(name)
	f, err := sys.Create(name)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(content)
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	return cerr
}

func readSmall(sys *fsjail.SysDir, name string) (string, error) {
	f, err := sys.Open(name)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, 64<<10))
	return strings.TrimSpace(string(b)), err
}

func (w *workspace) close() {
	if w.data != w.own {
		_ = w.data.Close()
	}
	_ = w.own.Close()
}

// discard removes every working folder of this download.
func (w *workspace) discard() {
	w.close()
	tmp, err := fsjail.OpenSys(w.dir, "tmp")
	if err != nil {
		return
	}
	defer func() { _ = tmp.Close() }()
	_ = tmp.RemoveAll(TmpPrefix + w.id)
	if w.root != w.id {
		_ = tmp.RemoveAll(TmpPrefix + w.root)
	}
}

func (w *workspace) partSize() int64 {
	info, err := w.data.Lstat(partName)
	if err != nil || !info.Mode().IsRegular() {
		return 0
	}
	return info.Size()
}

func (w *workspace) meta() (partMeta, bool) {
	raw, err := readSmall(w.data, metaName)
	if err != nil {
		return partMeta{}, false
	}
	var m partMeta
	if json.Unmarshal([]byte(raw), &m) != nil {
		return partMeta{}, false
	}
	return m, true
}

func (w *workspace) saveMeta(m partMeta) {
	b, _ := json.Marshal(m)
	_ = writeSmall(w.data, metaName, string(b))
}

func (w *workspace) truncate() {
	_ = w.data.Remove(partName)
	_ = w.data.Remove(metaName)
}

// downloaded is what fetch leaves in the part file.
type downloaded struct {
	size     int64
	total    int64
	finalURL *url.URL
	cdName   string
	resumed  int64
}

// Run is the remote_download task runner.
func (s *Service) Run(ctx context.Context, rt *tasks.Runtime) (any, error) {
	p, err := decodePayload(rt.Task.Payload)
	if err != nil {
		return nil, err
	}
	st := s.Settings(ctx)
	if !st.Enabled {
		return nil, ErrDisabled
	}
	u, err := url.Parse(p.URL)
	if err != nil || u.Host == "" {
		return nil, newErr(CodeValidation, "url is not valid", http.StatusBadRequest, map[string]any{"reason": "url"})
	}
	guard := s.guardFor(st)
	if err := guard.ValidateURL(u); err != nil {
		return nil, blockedToError(err)
	}
	dest, err := fsjail.Clean(p.Dest)
	if err != nil {
		return nil, err
	}
	policy, ok := files.ParsePolicy(p.Policy)
	if !ok {
		return nil, newErr(CodeValidation, "unknown conflict policy", http.StatusBadRequest, map[string]any{"reason": "bad_policy"})
	}
	j, release, err := rt.Jail(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	if info, err := j.Lstat(dest); err != nil {
		return nil, newErr(CodeNotFound, "destination folder not found", http.StatusNotFound, map[string]any{"path": string(dest)})
	} else if !info.IsDir {
		return nil, newErr(CodeValidation, "destination is not a folder", http.StatusBadRequest, map[string]any{"reason": "not_a_folder", "path": string(dest)})
	}
	ws, err := s.openWork(rt)
	if err != nil {
		return nil, err
	}
	ctx, cancelTotal := context.WithTimeout(ctx, TotalTimeout)
	defer cancelTotal()
	limit := s.limit(ctx, j, rt.Task.UserID, st)
	rt.Log("info", "downloading %s (limit %d bytes)", netguard.Describe(u), limit.max())
	var res *quota.Reservation
	dl, err := s.fetch(ctx, rt, guard, u, ws, limit, &res)
	if err != nil {
		if res != nil {
			res.Release()
		}
		if ctx.Err() != nil || !isRetryable(err) {
			// cancelled, blocked, too large, upstream 4xx: nothing to resume
			ws.discard()
		} else {
			// network failure after the in-task retries: keep the part so
			// "Retry" resumes with Range (SPEC §8.10 step 6)
			ws.close()
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, unwrapRetryable(err)
	}
	out, err := s.finish(ctx, rt, j, ws, p, dest, policy, dl, &res)
	if err != nil {
		if res != nil {
			res.Release()
		}
		ws.discard()
		return nil, err
	}
	ws.discard()
	return out, nil
}

func unwrapRetryable(err error) error {
	var re *retryableError
	if errors.As(err, &re) {
		return newErr(CodeNetwork, "download failed: "+shortErr(re.err), http.StatusBadGateway, map[string]any{"reason": "network"})
	}
	return err
}

func shortErr(err error) string {
	s := err.Error()
	if i := strings.Index(s, "?"); i > 0 { // never keep a query string
		s = s[:i]
	}
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}

// limits implements SPEC §8.10 step 7: the size cap (settings max size,
// MAX_UPLOAD_SIZE) answers FILE_TOO_LARGE; the budget (quota free, disk free
// minus the reserve) answers QUOTA_EXCEEDED. Both bound the stream.
type limits struct {
	size   int64 // FILE_TOO_LARGE above this
	budget int64 // QUOTA_EXCEEDED above this
}

func (l limits) max() int64 { return min(l.size, l.budget) }

func (l limits) exceeded(n int64) error {
	if n > l.size {
		return newErr(CodeFileTooLarge, "remote file exceeds the allowed size", http.StatusRequestEntityTooLarge, map[string]any{"size": n, "max": l.size})
	}
	return &quota.ExceededError{Reason: "quota", Needed: n, Free: l.budget}
}

func (s *Service) limit(ctx context.Context, j *fsjail.Jail, userID string, st Settings) limits {
	l := limits{size: st.MaxSize, budget: 1 << 50}
	if s.o.MaxUploadSize > 0 && (l.size <= 0 || s.o.MaxUploadSize < l.size) {
		l.size = s.o.MaxUploadSize
	}
	if l.size <= 0 {
		l.size = 1 << 50
	}
	if s.o.Quota != nil {
		if u, err := s.o.Quota.Usage(ctx, userID); err == nil && u.Limit > 0 {
			free := u.Free - u.Reserved
			if free < 0 {
				free = 0
			}
			l.budget = min(l.budget, free)
		}
		if free, _, err := j.Statfs(); err == nil {
			avail := int64(free) - s.o.Quota.MinDiskFree //nolint:gosec // free fits int64
			if avail < 0 {
				avail = 0
			}
			l.budget = min(l.budget, avail)
		}
	}
	return l
}

func (s *Service) fetch(ctx context.Context, rt *tasks.Runtime, guard *netguard.Guard, u *url.URL, ws *workspace, limit limits, res **quota.Reservation) (*downloaded, error) {
	client := guard.Client()
	defer client.CloseIdleConnections()
	for attempt := 0; ; attempt++ {
		dl, err := s.attempt(ctx, rt, client, u, ws, limit, res)
		if err == nil {
			return dl, nil
		}
		if ctx.Err() != nil || !isRetryable(err) || attempt >= len(s.backoff) {
			return nil, err
		}
		wait := s.backoff[attempt]
		rt.Log("warn", "attempt %d failed (%s); retrying in %s", attempt+1, shortErr(err), wait)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
}

func (s *Service) attempt(ctx context.Context, rt *tasks.Runtime, client *http.Client, u *url.URL, ws *workspace, limit limits, res **quota.Reservation) (*downloaded, error) {
	offset := ws.partSize()
	m, hasMeta := ws.meta()
	if offset > 0 && (!hasMeta || m.URL != u.String() || !m.AcceptRanges) {
		ws.truncate()
		offset = 0
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, newErr(CodeValidation, "url is not valid", http.StatusBadRequest, map[string]any{"reason": "url"})
	}
	req.Header.Set("User-Agent", s.userAgent())
	req.Header.Set("Accept-Encoding", "identity")
	if offset > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(offset, 10)+"-")
		if m.ETag != "" {
			req.Header.Set("If-Range", m.ETag)
		} else if m.LastModified != "" {
			req.Header.Set("If-Range", m.LastModified)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		if be, ok := netguard.Unwrap(err); ok {
			return nil, blockedToError(be)
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &retryableError{err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	var total int64
	switch {
	case resp.StatusCode == http.StatusOK:
		if offset > 0 {
			ws.truncate()
			offset = 0
		}
		total = resp.ContentLength
	case resp.StatusCode == http.StatusPartialContent && offset > 0:
		start, t, ok := parseContentRange(resp.Header.Get("Content-Range"))
		if !ok || start != offset {
			ws.truncate()
			return nil, &retryableError{err: errors.New("unexpected Content-Range")}
		}
		total = t
	case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable:
		ws.truncate()
		return nil, &retryableError{err: errors.New("range not satisfiable; restarting")}
	case resp.StatusCode >= 500:
		return nil, &retryableError{err: upstreamErr(resp.StatusCode)}
	default:
		return nil, upstreamErr(resp.StatusCode)
	}
	if total > limit.max() {
		return nil, limit.exceeded(total)
	}
	if *res == nil && total > 0 && s.o.Quota != nil {
		r, err := s.o.Quota.Reserve(ctx, nil, rt.Task.UserID, total)
		if err != nil {
			return nil, err
		}
		*res = r
	}
	meta := partMeta{URL: u.String(), ETag: resp.Header.Get("ETag"), LastModified: resp.Header.Get("Last-Modified"), Total: total,
		AcceptRanges: resp.StatusCode == http.StatusPartialContent || strings.EqualFold(resp.Header.Get("Accept-Ranges"), "bytes")}
	ws.saveMeta(meta)
	var f *os.File
	if offset > 0 {
		f, err = ws.data.OpenAppend(partName)
	} else {
		_ = ws.data.Remove(partName)
		f, err = ws.data.Create(partName)
	}
	if err != nil {
		return nil, err
	}
	written, cerr := s.copyBody(ctx, rt, f, resp.Body, offset, total, limit)
	if err := f.Close(); err != nil && cerr == nil {
		cerr = err
	}
	if cerr != nil {
		return nil, cerr
	}
	size := offset + written
	if total >= 0 && size != total {
		return nil, &retryableError{err: fmt.Errorf("incomplete body: %d of %d bytes", size, total)}
	}
	cd := ""
	if _, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil {
		cd = safeName(params["filename"])
	}
	rt.Progress(size, max(total, size))
	return &downloaded{size: size, total: total, finalURL: resp.Request.URL, cdName: cd, resumed: offset}, nil
}

func (s *Service) copyBody(ctx context.Context, rt *tasks.Runtime, f *os.File, body io.Reader, offset, total int64, limit limits) (int64, error) {
	buf := make([]byte, 128<<10)
	var written, sinceReport int64
	limitMax := limit.max()
	lr := io.LimitReader(body, limitMax-offset+1)
	for {
		n, rerr := lr.Read(buf)
		if n > 0 {
			if offset+written+int64(n) > limitMax {
				return written, limit.exceeded(offset + written + int64(n))
			}
			if _, werr := f.Write(buf[:n]); werr != nil {
				return written, werr
			}
			written += int64(n)
			sinceReport += int64(n)
			if sinceReport >= s.step {
				sinceReport = 0
				rt.Progress(offset+written, max(total, 0))
				if ctx.Err() != nil {
					return written, ctx.Err()
				}
			}
		}
		if rerr == io.EOF {
			return written, nil
		}
		if rerr != nil {
			if ctx.Err() != nil {
				return written, ctx.Err()
			}
			return written, &retryableError{err: rerr}
		}
	}
}

func parseContentRange(h string) (start, total int64, ok bool) {
	h = strings.TrimSpace(h)
	if !strings.HasPrefix(h, "bytes ") {
		return 0, -1, false
	}
	rng, tot, found := strings.Cut(strings.TrimPrefix(h, "bytes "), "/")
	if !found {
		return 0, -1, false
	}
	first, _, found := strings.Cut(rng, "-")
	if !found {
		return 0, -1, false
	}
	start, err := strconv.ParseInt(first, 10, 64)
	if err != nil {
		return 0, -1, false
	}
	total = -1
	if tot != "*" {
		if total, err = strconv.ParseInt(tot, 10, 64); err != nil {
			return 0, -1, false
		}
	}
	return start, total, true
}

// safeName keeps only the base name of a server-provided file name and
// drops anything fsjail would refuse.
func safeName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(strings.TrimSpace(name))
	if name == "." || name == "/" || name == ".." || name == "" {
		return ""
	}
	if _, err := fsjail.ValidateName(name); err != nil {
		return ""
	}
	return name
}

// chooseName follows SPEC §8.10: explicit filename → Content-Disposition →
// last URL path segment → download-<unix>.
func (s *Service) chooseName(p Payload, dl *downloaded) string {
	if n := safeName(p.Filename); n != "" {
		return n
	}
	if dl.cdName != "" {
		return dl.cdName
	}
	if dl.finalURL != nil {
		if n := safeName(lastSegment(dl.finalURL)); n != "" {
			return n
		}
	}
	return "download-" + strconv.FormatInt(s.now().Unix(), 10)
}

func (s *Service) finish(ctx context.Context, rt *tasks.Runtime, j *fsjail.Jail, ws *workspace, p Payload, dest fsjail.VPath, policy files.Policy, dl *downloaded, res **quota.Reservation) (*Result, error) {
	name := s.chooseName(p, dl)
	if s.o.Files != nil && s.o.Files.Blocked(name) {
		return nil, newErr(CodeUnsupported, "file extension is blocked", http.StatusUnsupportedMediaType, map[string]any{"reason": "blocked_extension", "name": name})
	}
	if *res == nil && s.o.Quota != nil {
		r, err := s.o.Quota.Reserve(ctx, j, rt.Task.UserID, dl.size)
		if err != nil {
			return nil, err
		}
		*res = r
	}
	target, err := fsjail.Join(dest, name)
	if err != nil {
		return nil, err
	}
	locks := s.o.Locks
	if locks == nil && s.o.Files != nil {
		locks = s.o.Files.Locks()
	}
	if locks != nil {
		unlock, err := locks.Acquire(ctx, rt.Task.UserID, fsjail.Write, "remote:"+rt.Task.ID, target)
		if err != nil {
			return nil, err
		}
		defer unlock()
	}
	out := &Result{Status: "created", Size: dl.size, Resumed: dl.resumed}
	if dl.finalURL != nil {
		out.FinalURL = netguard.Describe(dl.finalURL)
	}
	var replaced int64
	if existing, err := j.Lstat(target); err == nil {
		switch policy {
		case files.PolicySkip:
			out.Status, out.Path = "skipped", string(target)
			if *res != nil {
				(*res).Release()
			}
			rt.Log("info", "destination exists; skipped")
			return out, nil
		case files.PolicyRename:
			if target, err = files.UniqueName(j, dest, name, false); err != nil {
				return nil, err
			}
			out.Status = "renamed"
		case files.PolicyOverwrite:
			if existing.IsDir {
				return nil, newErr(CodeValidation, "cannot overwrite a folder with a file", http.StatusBadRequest, map[string]any{"reason": "type_mismatch", "path": string(target)})
			}
			replaced = existing.Size
			out.Status = "overwritten"
		default:
			return nil, newErr(CodeAlreadyExists, "destination already exists", http.StatusConflict, map[string]any{"path": string(target), "existing": map[string]any{"size": existing.Size, "mtime": existing.ModTime.UTC().Format(time.RFC3339Nano)}})
		}
	} else if !errors.Is(err, fsjail.ErrNotFound) {
		return nil, err
	}
	if out.Status == "overwritten" {
		tmp, err := fsjail.Join(dest, ".fm-remote-"+rt.Task.ID)
		if err != nil {
			return nil, err
		}
		if err := j.MoveIn(ctx, ws.data, partName, tmp); err != nil {
			return nil, err
		}
		if err := j.Rename(tmp, target); err != nil {
			_ = j.Remove(tmp)
			return nil, err
		}
	} else if err := j.MoveIn(ctx, ws.data, partName, target); err != nil {
		if errors.Is(err, fsjail.ErrExists) {
			if target, err = files.UniqueName(j, dest, name, false); err != nil {
				return nil, err
			}
			if err := j.MoveIn(ctx, ws.data, partName, target); err != nil {
				return nil, err
			}
			out.Status = "renamed"
		} else {
			return nil, err
		}
	}
	out.Path = string(target)
	if *res != nil && s.o.Repo != nil {
		if err := (*res).Commit(ctx, s.o.Repo, dl.size-replaced); err != nil {
			s.logger.ErrorContext(ctx, "remote download: quota commit failed", slog.String("task", rt.Task.ID), slog.String("error", err.Error()))
			_ = s.o.Quota.MarkDirty(ctx, rt.Task.UserID)
		}
		*res = nil
	} else if *res != nil {
		(*res).Release()
		*res = nil
	}
	host := ""
	if dl.finalURL != nil {
		host = dl.finalURL.Host
	}
	if s.o.Files != nil {
		s.o.Files.RecordActivity(ctx, rt.Task.UserID, files.ActRemoteDL, string(target), "", map[string]any{"size": dl.size, "host": host, "result": out.Status})
	}
	if s.o.OnChange != nil {
		s.o.OnChange(ctx, rt.Task.UserID, target, false)
	}
	if s.o.Invalidate != nil {
		s.o.Invalidate(rt.Task.UserID, dest)
	}
	rt.Emit(tasks.EventChanged, files.ChangedEvent{Dir: string(dest), Reason: "remote_download", Paths: []string{string(target)}})
	rt.Log("info", "saved %s (%d bytes, %s)", string(target), dl.size, out.Status)
	return out, nil
}

// ProbeResult is the answer of POST /remote-downloads/probe (SPEC §6.3).
type ProbeResult struct {
	FinalURL      string `json:"finalUrl"`
	Size          *int64 `json:"size"`
	Filename      string `json:"filename,omitempty"`
	AcceptsRanges bool   `json:"acceptsRanges"`
	ContentType   string `json:"contentType,omitempty"`
}

// Probe validates the URL and asks the server for size/name/ranges (HEAD,
// then GET with Range 0-0 when HEAD is refused).
func (s *Service) Probe(ctx context.Context, raw string) (*ProbeResult, error) {
	u, err := s.ValidateURL(ctx, raw)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	client := s.guardFor(s.Settings(ctx)).Client()
	defer client.CloseIdleConnections()
	do := func(method string, rng bool) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", s.userAgent())
		req.Header.Set("Accept-Encoding", "identity")
		if rng {
			req.Header.Set("Range", "bytes=0-0")
		}
		return client.Do(req)
	}
	resp, err := do(http.MethodHead, false)
	if err == nil && (resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusNotImplemented || resp.StatusCode >= 500) {
		_ = resp.Body.Close()
		resp, err = do(http.MethodGet, true)
	}
	if err != nil {
		if be, ok := netguard.Unwrap(err); ok {
			return nil, blockedToError(be)
		}
		return nil, newErr(CodeNetwork, "remote server unreachable: "+shortErr(err), http.StatusBadGateway, map[string]any{"reason": "network"})
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.CopyN(io.Discard, resp.Body, 1)
	if resp.StatusCode >= 400 {
		return nil, upstreamErr(resp.StatusCode)
	}
	out := &ProbeResult{FinalURL: netguard.Describe(resp.Request.URL), ContentType: resp.Header.Get("Content-Type"),
		AcceptsRanges: resp.StatusCode == http.StatusPartialContent || strings.EqualFold(resp.Header.Get("Accept-Ranges"), "bytes")}
	if resp.StatusCode == http.StatusPartialContent {
		if _, total, ok := parseContentRange(resp.Header.Get("Content-Range")); ok && total >= 0 {
			out.Size = &total
		}
	} else if resp.ContentLength >= 0 {
		cl := resp.ContentLength
		out.Size = &cl
	}
	if _, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil {
		out.Filename = safeName(params["filename"])
	}
	if out.Filename == "" {
		out.Filename = safeName(lastSegment(resp.Request.URL))
	}
	return out, nil
}
