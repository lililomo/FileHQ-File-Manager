package archive

import (
	"path"
	"strings"
	"unicode"

	"kppbwi.app/filemanager/internal/fsjail"
)

// Rejection reasons for entry names (details.reason of ARCHIVE_UNSAFE and
// the per-entry skip list of the result).
const (
	ReasonAbsolute   = "absolute"
	ReasonDotDot     = "dotdot"
	ReasonDrive      = "drive"
	ReasonNUL        = "nul"
	ReasonControl    = "control"
	ReasonTooLong    = "too_long"
	ReasonTooDeep    = "too_deep"
	ReasonInvalid    = "invalid_name"
	ReasonSystem     = "system"
	ReasonEmpty      = "empty"
	ReasonSpecial    = "special_type"
	ReasonLinkEscape = "link_escape"
	ReasonHardlink   = "hardlink"
	ReasonBlockedExt = "blocked_extension"
	ReasonNested     = "nested_archive"
	ReasonDuplicate  = "duplicate"
	ReasonTypeClash  = "type_clash"
)

// SafePath normalises an entry name into a path inside the extraction root
// (SPEC §8.11 step 2/4): backslashes become slashes, "./" prefixes and
// trailing slashes are dropped, then fsjail.Clean validates every segment
// (NFC, no "..", no NUL/control, length and depth limits, ".system" refused
// at the root). The returned reason is "" when the name is acceptable;
// warnings lists Windows-reserved segments (allowed).
func SafePath(raw string) (p fsjail.VPath, reason string, warnings []string) {
	if raw == "" {
		return "", ReasonEmpty, nil
	}
	if strings.ContainsRune(raw, 0) {
		return "", ReasonNUL, nil
	}
	name := strings.ReplaceAll(raw, "\\", "/")
	if len(name) >= 2 && name[1] == ':' && unicode.IsLetter(rune(name[0])) {
		return "", ReasonDrive, nil
	}
	if strings.HasPrefix(name, "/") {
		// a lone leading slash is tolerated by lenient tools; SPEC says refuse
		return "", ReasonAbsolute, nil
	}
	for strings.HasPrefix(name, "./") {
		name = name[2:]
	}
	name = strings.TrimSuffix(name, "/")
	if name == "" || name == "." {
		return "", ReasonEmpty, nil
	}
	for _, seg := range strings.Split(name, "/") {
		switch seg {
		case "..":
			return "", ReasonDotDot, nil
		case "":
			// "a//b": collapse like path.Clean would; harmless
		}
	}
	name = path.Clean(name)
	if name == "." || strings.HasPrefix(name, "../") || name == ".." {
		return "", ReasonDotDot, nil
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return "", ReasonControl, nil
		}
	}
	vp, err := fsjail.Clean("/" + name)
	if err != nil {
		_, details, _ := fsjail.APIError(err)
		r, _ := details["reason"].(string)
		switch r {
		case "too_deep":
			return "", ReasonTooDeep, nil
		case "path_too_long", "segment_too_long":
			return "", ReasonTooLong, nil
		case "reserved":
			return "", ReasonSystem, nil
		case "parent_segment":
			return "", ReasonDotDot, nil
		case "control_char":
			return "", ReasonControl, nil
		}
		return "", ReasonInvalid, nil
	}
	if len(strings.Split(vp.Rel(), "/")) > fsjail.MaxDepth {
		return "", ReasonTooDeep, nil
	}
	for _, seg := range strings.Split(vp.Rel(), "/") {
		w, err := fsjail.ValidateName(seg)
		if err != nil {
			return "", ReasonInvalid, nil
		}
		warnings = append(warnings, w...)
	}
	return vp, "", warnings
}

// LinkInside reports whether a relative link target stays inside the
// extraction root when resolved from the link's directory.
func LinkInside(link fsjail.VPath, target string) bool {
	if target == "" || strings.HasPrefix(target, "/") || strings.HasPrefix(target, "\\") || strings.ContainsRune(target, 0) {
		return false
	}
	target = strings.ReplaceAll(target, "\\", "/")
	if len(target) >= 2 && target[1] == ':' {
		return false
	}
	// resolve relative to the root so ".." past it is visible as a leading ".."
	resolved := path.Clean(path.Join(link.Dir().Rel(), target))
	return resolved != ".." && !strings.HasPrefix(resolved, "../")
}

// Limits bound an extraction (SPEC §8.11 step 3). MaxBytes is already the
// minimum of ARCHIVE_MAX_EXTRACT_SIZE, the free quota and the free disk.
type Limits struct {
	MaxEntries int
	MaxBytes   int64
	// Ratio is the uncompressed/compressed ratio treated as a bomb once the
	// output exceeds RatioMinBytes (SPEC: 200 and 1 GiB).
	Ratio         float64
	RatioMinBytes int64
}

// DefaultRatio and DefaultRatioMinBytes follow SPEC §8.11 step 3.
const (
	DefaultRatio         = 200.0
	DefaultRatioMinBytes = int64(1) << 30
)

// checkIndex validates the pre-scan totals against the limits.
func (l Limits) checkIndex(entries int, total, archiveSize int64, reason string) error {
	if l.MaxEntries > 0 && entries > l.MaxEntries {
		return limitErr("archive has too many entries", map[string]any{"reason": "entries", "entries": entries, "max": l.MaxEntries})
	}
	if l.MaxBytes > 0 && total > l.MaxBytes {
		return limitErr("uncompressed size exceeds the limit", map[string]any{"reason": reason, "size": total, "max": l.MaxBytes})
	}
	if l.Ratio > 0 && archiveSize > 0 && total > l.RatioMinBytes && float64(total)/float64(archiveSize) > l.Ratio {
		return unsafeErr("decompression ratio looks like an archive bomb", map[string]any{"reason": "ratio", "size": total, "archiveSize": archiveSize, "ratio": int64(float64(total) / float64(archiveSize))})
	}
	return nil
}

// budget counts bytes written while streaming and stops a lying header.
type budget struct {
	limits      Limits
	written     int64
	archiveSize int64
	entries     int
	reason      string
}

func (b *budget) addEntry() error {
	b.entries++
	if b.limits.MaxEntries > 0 && b.entries > b.limits.MaxEntries {
		return limitErr("archive has too many entries", map[string]any{"reason": "entries", "entries": b.entries, "max": b.limits.MaxEntries})
	}
	return nil
}

func (b *budget) add(n int64) error {
	b.written += n
	if b.limits.MaxBytes > 0 && b.written > b.limits.MaxBytes {
		return limitErr("uncompressed size exceeds the limit", map[string]any{"reason": b.reason, "size": b.written, "max": b.limits.MaxBytes})
	}
	if b.limits.Ratio > 0 && b.archiveSize > 0 && b.written > b.limits.RatioMinBytes && float64(b.written)/float64(b.archiveSize) > b.limits.Ratio {
		return unsafeErr("decompression ratio looks like an archive bomb", map[string]any{"reason": "ratio", "size": b.written, "archiveSize": b.archiveSize})
	}
	return nil
}
