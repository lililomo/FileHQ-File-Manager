package archive

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"kppbwi.app/filemanager/internal/fsjail"
)

// Archive browser (SPEC §8.11 "Archive browser", PROMPT Step 14b): list one
// level of an archive from the cached index and stream a single member
// without extracting anything to disk.

// Limits of the browser API.
const (
	// MaxEntryContent bounds one streamed member (PROMPT 14b: 50 MB).
	MaxEntryContent = 50 << 20
	// EntriesPageMax is the page size of GET /archives/entries.
	EntriesPageMax = 1000
	// MaxModifySize is the largest zip the editor rewrites (SPEC §8.11).
	MaxModifySize = 2 << 30
)

// EntriesRequest is the validated query of GET /archives/entries.
type EntriesRequest struct {
	UserID   string
	Archive  string
	Inner    string
	Password string
	Encoding string
	Cursor   string
	Limit    int
}

// EntriesResponse is one page of one archive folder.
type EntriesResponse struct {
	Archive      string       `json:"archive"`
	Inner        string       `json:"inner"`
	Format       Format       `json:"format"`
	Items        []IndexEntry `json:"items"`
	NextCursor   string       `json:"nextCursor,omitempty"`
	Total        int          `json:"total"`
	TotalEntries int          `json:"totalEntries"`
	Size         int64        `json:"size"`
	Encrypted    bool         `json:"encrypted"`
	Encoding     string       `json:"encoding,omitempty"`
	// SupportsModify is true for an unencrypted zip ≤ MaxModifySize.
	SupportsModify bool   `json:"supportsModify"`
	ModifyReason   string `json:"modifyReason,omitempty"`
	// Session carries the archive session id when a password was accepted.
	Session string `json:"session,omitempty"`
	Unsafe  int    `json:"unsafe,omitempty"`
}

// opened is an archive file resolved inside a jail.
type opened struct {
	f       *os.File
	info    fsjail.Info
	format  Format
	archive fsjail.VPath
}

func (o *opened) close() { _ = o.f.Close() }

// openArchive resolves, opens and detects an archive path.
func (s *Service) openArchive(j *fsjail.Jail, raw string) (*opened, error) {
	archive, err := fsjail.Clean(raw)
	if err != nil || archive.IsRoot() {
		return nil, validationErr("archive path is not valid", map[string]any{"fields": map[string]any{"path": "invalid"}})
	}
	info, err := j.Lstat(archive)
	if err != nil {
		return nil, newErr(CodeNotFound, "archive not found", http.StatusNotFound, map[string]any{"path": string(archive)})
	}
	if !info.IsRegular() {
		return nil, unsupportedErr("not a regular file", map[string]any{"path": string(archive), "reason": "not_a_file"})
	}
	f, err := j.Open(archive)
	if err != nil {
		return nil, err
	}
	format, err := Detect(archive.Base(), f, info.Size)
	if err != nil {
		_ = f.Close()
		return nil, unsupportedErr("file is not a supported archive", map[string]any{"path": string(archive), "reason": "unknown_format"})
	}
	if capa, _ := s.Capability(format); !capa.CanRead {
		_ = f.Close()
		return nil, newErr(CodeDependency, "7-Zip is not installed on the server", http.StatusNotImplemented, map[string]any{"format": string(format), "tool": "7z"})
	}
	info.Path = archive
	return &opened{f: f, info: info, format: format, archive: archive}, nil
}

// source opens the Go reader of an archive (nil, ErrCLIOnly for iso).
func (s *Service) source(op *opened, password, encoding string) (Source, error) {
	return Open(op.f, op.info.Size, op.format, OpenOptions{Name: op.archive.Base(), Password: password, Encoding: encoding})
}

// indexFor returns the cached index of an archive version, building it on
// a miss. The password only matters for containers whose headers are
// encrypted (7z -mhe, rar -hp).
func (s *Service) indexFor(ctx context.Context, j *fsjail.Jail, userID string, op *opened, password, encoding string) (*Index, error) {
	key := indexKey(userID, string(op.archive), op.info.Size, op.info.ModTime.UnixNano(), encoding)
	if ix, ok := s.index.get(key); ok {
		if ix.HeaderEncrypted && password == "" {
			// the names were only readable with the password that built
			// the index; a later request without one must not see them
			return nil, ErrPasswordRequired
		}
		return ix, nil
	}
	var (
		ix  *Index
		err error
	)
	src, serr := s.source(op, password, encoding)
	switch {
	case errors.Is(serr, ErrCLIOnly), serr != nil && op.format == FormatSevenZip && IsCode(serr, CodeCorrupt) && s.SevenZipAvailable():
		ix, err = s.indexCLI(ctx, j, op, password)
	case serr != nil:
		return nil, serr
	default:
		ix, err = BuildIndex(ctx, src, s.o.MaxEntries)
	}
	if err != nil {
		return nil, err
	}
	s.index.put(key, ix)
	return ix, nil
}

// indexCLI lists iso (and 7z the library refuses) through `7z l -slt`.
func (s *Service) indexCLI(ctx context.Context, j *fsjail.Jail, op *opened, password string) (*Index, error) {
	host, err := j.AbsForCLI(op.archive)
	if err != nil {
		return nil, err
	}
	rows, err := s.list7z(ctx, host, password)
	if err != nil {
		return nil, err
	}
	if s.o.MaxEntries > 0 && len(rows) > s.o.MaxEntries {
		return nil, limitErr("archive has too many entries", map[string]any{"reason": "entries", "limit": s.o.MaxEntries, "entries": len(rows)})
	}
	entries := make([]Entry, 0, len(rows))
	for _, r := range rows {
		e := Entry{Name: r.Path, Size: r.Size, CompressedSize: -1, ModTime: r.ModTime, IsDir: r.IsDir}
		if r.Link != "" {
			e.IsSymlink, e.LinkTarget = true, r.Link
		}
		entries = append(entries, e)
	}
	// the CLI path cannot tell plain from encrypted headers; when a password
	// was needed to get here, listing again without one is refused
	ix := &Index{Format: op.format, HeaderEncrypted: password != "" && op.format == FormatSevenZip, Encrypted: password != "" && op.format == FormatSevenZip, built: time.Now()}
	ix.addAll(entries)
	return ix, nil
}

// verifyPassword opens the first encrypted member and reads from it so a
// wrong password is reported before a session is created.
func (s *Service) verifyPassword(ctx context.Context, op *opened, ix *Index, password, encoding string) error {
	if _, err := op.f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	src, err := s.source(op, password, encoding)
	if err != nil {
		if errors.Is(err, ErrCLIOnly) {
			return nil // iso: the CLI listing already ran with the password
		}
		return err
	}
	var target string
	for _, e := range ix.Entries {
		if e.Encrypted && !e.IsDir && !e.IsSymlink {
			target = e.Name
			break
		}
	}
	if target == "" {
		return nil
	}
	errDone := errors.New("done")
	err = src.Walk(ctx, func(e *Entry) error {
		name, _ := NormalizeInner(e.Name)
		if name != target || e.Open == nil {
			return nil
		}
		rc, err := e.Open()
		if err != nil {
			return err
		}
		defer func() { _ = rc.Close() }()
		var probe [64]byte
		if _, err := io.ReadFull(rc, probe[:]); err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			return err
		}
		return errDone
	})
	if err != nil && !errors.Is(err, errDone) {
		return err
	}
	return nil
}

// modifiable reports whether the archive can be edited in place.
func modifiable(ix *Index, format Format, size int64) (bool, string) {
	switch {
	case !format.IsZip():
		return false, "format"
	case ix.Encrypted:
		return false, "encrypted"
	case size > MaxModifySize:
		return false, "too_large_to_modify"
	}
	return true, ""
}

// Entries lists one folder of an archive (SPEC §6.3 GET /archives/entries).
func (s *Service) Entries(ctx context.Context, j *fsjail.Jail, req EntriesRequest) (EntriesResponse, error) {
	if len(req.Password) > MaxPasswordLen {
		return EntriesResponse{}, validationErr("password too long", map[string]any{"fields": map[string]any{"password": "max 256"}})
	}
	enc, ok := ParseEncoding(req.Encoding)
	if !ok {
		return EntriesResponse{}, validationErr("unknown name encoding", map[string]any{"fields": map[string]any{"encoding": "auto, utf-8, gbk, cp437, shift_jis, big5, windows-1252"}})
	}
	inner, ok := NormalizeInner(req.Inner)
	if !ok {
		return EntriesResponse{}, validationErr("inner path is not valid", map[string]any{"fields": map[string]any{"inner": "invalid"}})
	}
	limit := req.Limit
	if limit <= 0 || limit > EntriesPageMax {
		limit = EntriesPageMax
	}
	op, err := s.openArchive(j, req.Archive)
	if err != nil {
		return EntriesResponse{}, err
	}
	defer op.close()
	ix, err := s.indexFor(ctx, j, req.UserID, op, req.Password, enc)
	if err != nil {
		return EntriesResponse{}, err
	}
	children, ok := ix.Children(inner)
	if !ok {
		return EntriesResponse{}, newErr(CodeNotFound, "folder not found in archive", http.StatusNotFound, map[string]any{"inner": inner})
	}
	page, next, err := Page(children, req.Cursor, limit)
	if err != nil {
		return EntriesResponse{}, err
	}
	resp := EntriesResponse{Archive: string(op.archive), Inner: inner, Format: ix.Format, Items: page, NextCursor: next, Total: len(children),
		TotalEntries: ix.Total(), Size: op.info.Size, Encrypted: ix.Encrypted, Encoding: ix.Encoding, Unsafe: ix.Unsafe}
	if resp.Items == nil {
		resp.Items = []IndexEntry{}
	}
	resp.SupportsModify, resp.ModifyReason = modifiable(ix, op.format, op.info.Size)
	if req.Password != "" && ix.Encrypted {
		id, found := s.sessions.Find(req.UserID, string(op.archive), req.Password, enc)
		if !found {
			if err := s.verifyPassword(ctx, op, ix, req.Password, enc); err != nil {
				return EntriesResponse{}, err
			}
			if id, err = s.sessions.Put(req.UserID, string(op.archive), req.Password, enc); err != nil {
				return EntriesResponse{}, err
			}
		}
		resp.Session = id
	}
	return resp, nil
}

// Session resolves an archive session id for a user and archive path.
func (s *Service) Session(id, userID, archive string) (ArchiveSession, bool) {
	if id == "" {
		return ArchiveSession{}, false
	}
	return s.sessions.Get(id, userID, archive)
}

// EntryStream is one member being streamed out of its archive.
type EntryStream struct {
	Entry IndexEntry
	Body  io.ReadCloser
}

// ErrEntryNotFound: the inner path names no file member.
var ErrEntryNotFound = &Error{Code: CodeNotFound, Message: "entry not found in archive", Status: http.StatusNotFound, Details: map[string]any{"reason": "entry_not_found"}}

type pipeBody struct {
	r      *io.PipeReader
	cancel context.CancelFunc
	done   chan struct{}
}

func (b *pipeBody) Read(p []byte) (int, error) { return b.r.Read(p) }

func (b *pipeBody) Close() error {
	b.cancel()
	_ = b.r.Close()
	<-b.done
	return nil
}

// OpenEntry streams one file member. The body is produced by a walk in a
// goroutine (stream containers only expose bodies inside the walk); errors
// of the reader side (wrong password, corrupt data) surface on Read, so a
// caller that wants a clean status code peeks before writing headers.
func (s *Service) OpenEntry(ctx context.Context, j *fsjail.Jail, userID, rawArchive, rawInner, password, encoding string) (*EntryStream, error) {
	inner, ok := NormalizeInner(rawInner)
	if !ok || inner == "" {
		return nil, validationErr("inner path is not valid", map[string]any{"fields": map[string]any{"inner": "invalid"}})
	}
	enc, ok := ParseEncoding(encoding)
	if !ok {
		enc = "auto"
	}
	op, err := s.openArchive(j, rawArchive)
	if err != nil {
		return nil, err
	}
	ix, err := s.indexFor(ctx, j, userID, op, password, enc)
	if err != nil {
		op.close()
		return nil, err
	}
	e, found := ix.Lookup(inner)
	if !found || e.IsDir || e.IsSymlink {
		op.close()
		return nil, ErrEntryNotFound
	}
	if e.Size > MaxEntryContent {
		op.close()
		return nil, newErr("FILE_TOO_LARGE", "entry is too large to preview", http.StatusRequestEntityTooLarge, map[string]any{"limit": MaxEntryContent, "size": e.Size})
	}
	if e.Encrypted && password == "" {
		op.close()
		return nil, ErrPasswordRequired
	}
	if _, err := op.f.Seek(0, io.SeekStart); err != nil {
		op.close()
		return nil, err
	}
	src, err := s.source(op, password, enc)
	if err != nil {
		op.close()
		if errors.Is(err, ErrCLIOnly) {
			return nil, unsupportedErr("this format can only be extracted, not previewed", map[string]any{"reason": "cli_only", "format": string(op.format)})
		}
		return nil, err
	}
	wctx, cancel := context.WithCancel(ctx)
	pr, pw := io.Pipe()
	done := make(chan struct{})
	errFound := errors.New("found")
	go func() {
		defer close(done)
		defer op.close()
		walkErr := src.Walk(wctx, func(m *Entry) error {
			name, _ := NormalizeInner(m.Name)
			if name != inner || m.IsDir || m.IsSymlink {
				return nil
			}
			if m.Open == nil {
				return ErrEntryNotFound
			}
			rc, err := m.Open()
			if err != nil {
				return err
			}
			defer func() { _ = rc.Close() }()
			if _, err := io.Copy(pw, io.LimitReader(rc, MaxEntryContent+1)); err != nil {
				return err
			}
			return errFound
		})
		switch {
		case errors.Is(walkErr, errFound):
			_ = pw.Close()
		case walkErr == nil:
			pw.CloseWithError(ErrEntryNotFound)
		default:
			pw.CloseWithError(walkErr)
		}
	}()
	return &EntryStream{Entry: e, Body: &pipeBody{r: pr, cancel: cancel, done: done}}, nil
}

// MembersOf expands inner paths (files or folders) to the member names an
// extract-entries request should include; unknown names are reported.
func (s *Service) MembersOf(ctx context.Context, j *fsjail.Jail, userID, rawArchive, password, encoding string, inner []string) (known []string, unknown []string, err error) {
	op, err := s.openArchive(j, rawArchive)
	if err != nil {
		return nil, nil, err
	}
	defer op.close()
	enc, ok := ParseEncoding(encoding)
	if !ok {
		enc = "auto"
	}
	ix, err := s.indexFor(ctx, j, userID, op, password, enc)
	if err != nil {
		return nil, nil, err
	}
	for _, raw := range inner {
		n, ok := NormalizeInner(raw)
		if !ok || n == "" {
			unknown = append(unknown, raw)
			continue
		}
		if _, found := ix.Lookup(n); !found {
			unknown = append(unknown, raw)
			continue
		}
		known = append(known, n)
	}
	return known, unknown, nil
}

// EntryBaseName is the file name a streamed member is served as.
func EntryBaseName(inner string) string {
	b := path.Base(inner)
	if b == "." || b == "/" || strings.TrimSpace(b) == "" {
		return "entry"
	}
	return b
}
