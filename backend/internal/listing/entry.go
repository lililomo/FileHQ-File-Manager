// Package listing builds sorted, paged directory listings on top of fsjail
// (SPEC §8.3, ADR-11): natural sort in the server, opaque signed cursors and
// a short-lived LRU cache so that folders with 10 000+ entries stay fast.
package listing

import (
	"io/fs"
	"strconv"
	"time"

	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
)

// Media is the cached metadata of an image/audio/video file (SPEC §4.2
// media_metadata; filled by media.Service.Annotate, nil when unknown).
type Media struct {
	Mime       string            `json:"mime"`
	Width      int               `json:"width"`
	Height     int               `json:"height"`
	DurationMs int64             `json:"durationMs"`
	CodecVideo string            `json:"codecVideo,omitempty"`
	CodecAudio string            `json:"codecAudio,omitempty"`
	HasCover   bool              `json:"hasCover"`
	Tags       map[string]string `json:"tags,omitempty"`
}

// Item is the API representation of one directory entry (SPEC §6.3 "Item").
// Field names are the JSON contract; unexported fields hold sort keys.
type Item struct {
	Name          string  `json:"name"`
	Path          string  `json:"path"`
	Type          string  `json:"type"` // dir | file | symlink
	IsDir         bool    `json:"isDir"`
	IsSymlink     bool    `json:"isSymlink"`
	SymlinkTarget string  `json:"symlinkTarget,omitempty"`
	Broken        bool    `json:"broken,omitempty"`
	BrokenReason  string  `json:"brokenReason,omitempty"`
	Size          int64   `json:"size"`
	ItemCount     *int64  `json:"itemCount"`
	Mime          *string `json:"mime"`
	TypeLabel     string  `json:"typeLabel"`
	Category      string  `json:"category"`
	Ext           string  `json:"ext"`
	Mtime         string  `json:"mtime"`
	Ctime         string  `json:"ctime"`
	Btime         *string `json:"btime"`
	Atime         string  `json:"atime"`
	Mode          string  `json:"mode"`      // rwxr-xr-x
	ModeOctal     string  `json:"modeOctal"` // 755
	Note          *string `json:"note"`
	Tag           *string `json:"tag"`
	Favorite      bool    `json:"favorite"`
	Pinned        bool    `json:"pinned"`
	Shared        string  `json:"shared"` // none | public | users | both
	Media         *Media  `json:"media"`
	Thumb         bool    `json:"thumb"`
	Etag          string  `json:"etag"`

	mtime, atime, ctime time.Time
	btime               *time.Time
	modeNum             uint32
	nlink               uint64
	etagSize            int64 // lstat size (symlinks: link length; Size is 0)
	nameKey             NatKey
	folded              string
}

// ISO formats a time the way the API does everywhere (UTC, milliseconds).
func ISO(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// FromInfo converts an fsjail entry to an Item without touching the DB
// (metadata slots stay null/false until a MetaProvider fills them).
func FromInfo(info fsjail.Info) *Item {
	it := newItem(info)
	it.fill()
	return it
}

// newItem keeps what sorting, filtering and the MetaProviders need; the
// display strings (times, mode, etag) are formatted by fill only for the
// items of the page being sent (Step 21: a 10 000 entry folder formats 500
// instead of 10 000 × 6 strings on every cold build).
func newItem(info fsjail.Info) *Item {
	it := &Item{
		Name:          info.Name,
		Path:          string(info.Path),
		IsDir:         info.IsDir,
		IsSymlink:     info.IsSymlink,
		SymlinkTarget: info.SymlinkTarget,
		Broken:        info.SymlinkBroken,
		BrokenReason:  info.SymlinkReason,
		Shared:        "none",
		mtime:         info.ModTime,
		atime:         info.AccessTime,
		ctime:         info.ChangeTime,
		btime:         info.BirthTime,
		modeNum:       uint32(info.Mode.Perm()),
		nlink:         info.Nlink,
		etagSize:      info.Size,
	}
	switch {
	case info.IsDir:
		it.Type = "dir"
		ti := files.Folder()
		it.TypeLabel, it.Category = ti.Label, string(ti.Category)
	default:
		if info.IsSymlink {
			it.Type = "symlink"
		} else {
			it.Type = "file"
			it.Size = info.Size
		}
		ti := files.Lookup(info.Name)
		it.Ext, it.TypeLabel, it.Category, it.Thumb = ti.Ext, ti.Label, string(ti.Category), ti.Thumb && !info.IsSymlink
		if ti.Mime != "" {
			m := ti.Mime
			it.Mime = &m
		}
	}
	return it
}

// fill formats the display fields from the raw stat values. It is
// idempotent and only ever runs on an item no other request can see (a page
// clone or a fresh FromInfo).
func (it *Item) fill() {
	if it.Mtime != "" {
		return
	}
	it.Mtime, it.Ctime, it.Atime = ISO(it.mtime), ISO(it.ctime), ISO(it.atime)
	it.Mode = permString(fs.FileMode(it.modeNum))
	it.ModeOctal = strconv.FormatUint(uint64(it.modeNum), 8)
	if it.btime != nil {
		s := ISO(*it.btime)
		it.Btime = &s
	}
	if it.IsDir {
		it.Etag = `W/"` + strconv.FormatInt(it.mtime.UnixNano(), 10) + `"`
	} else {
		// symlinks report size 0 in the API but keep their lstat size in the etag
		it.Etag = `"` + strconv.FormatInt(it.etagSize, 10) + "-" + strconv.FormatInt(it.mtime.UnixNano(), 10) + `"`
	}
}

// permString renders the nine permission bits ("rwxr-xr-x").
func permString(m fs.FileMode) string {
	const rwx = "rwxrwxrwx"
	var b [9]byte
	perm := m.Perm()
	for i := range 9 {
		if perm&(1<<uint(8-i)) != 0 {
			b[i] = rwx[i]
		} else {
			b[i] = '-'
		}
	}
	return string(b[:])
}

// ModTime returns the entry's modification time (validity key of
// media_metadata and thumbnails).
func (it *Item) ModTime() time.Time { return it.mtime }

// StatView is the extra "stat" object of GET /files/stat (SPEC §6.3).
func (it *Item) StatView() map[string]any {
	v := map[string]any{
		"mode":      it.Mode,
		"modeOctal": it.ModeOctal,
		"atime":     it.Atime,
		"mtime":     it.Mtime,
		"ctime":     it.Ctime,
		"btime":     it.Btime,
		"nlink":     it.nlink,
	}
	return v
}

// clone returns a shallow copy so per-request annotations (itemCount) never
// mutate the cached slice; the copy gets its display fields.
func (it *Item) clone() *Item {
	c := *it
	c.fill()
	return &c
}
