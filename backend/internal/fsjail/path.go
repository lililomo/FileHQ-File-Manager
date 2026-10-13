package fsjail

import (
	"path"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Limits of SPEC §5.1.
const (
	MaxNameBytes = 255
	MaxPathBytes = 4096
	MaxDepth     = 64
	// ReservedRootName can never be created at the top of a user root
	// (defense in depth: DATA_DIR lives beside user homes, never inside).
	ReservedRootName = ".system"
)

// VPath is a normalized virtual path: NFC, "/"-separated, always starts with
// "/", never ends with "/" (except the root itself), no "." or ".." segments.
type VPath string

// Root is the user's top directory.
const Root VPath = "/"

// Clean normalizes raw client input into a VPath (SPEC §5.1). It never touches
// the filesystem and is deterministic. Rejections carry a reason:
// invalid_utf8, control_char, path_too_long, dot_segment, parent_segment,
// segment_too_long, too_deep, reserved.
func Clean(raw string) (VPath, error) {
	if raw == "" || raw == "/" {
		return Root, nil
	}
	if !utf8.ValidString(raw) {
		return "", invalidPath(raw, "invalid_utf8")
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return "", invalidPath(raw, "control_char")
		}
	}
	s := norm.NFC.String(raw)
	if len(s) > MaxPathBytes {
		return "", invalidPath(raw, "path_too_long")
	}
	segs := make([]string, 0, 8)
	for _, seg := range strings.Split(s, "/") {
		switch seg {
		case "":
			continue
		case ".":
			return "", invalidPath(raw, "dot_segment")
		case "..":
			return "", invalidPath(raw, "parent_segment")
		}
		if len(seg) > MaxNameBytes {
			return "", invalidPath(raw, "segment_too_long")
		}
		segs = append(segs, seg)
	}
	if len(segs) > MaxDepth {
		return "", invalidPath(raw, "too_deep")
	}
	if len(segs) == 0 {
		return Root, nil
	}
	if segs[0] == ReservedRootName {
		return "", invalidPath(raw, "reserved")
	}
	v := VPath("/" + strings.Join(segs, "/"))
	if len(v) > MaxPathBytes {
		return "", invalidPath(raw, "path_too_long")
	}
	return v, nil
}

// Normalize is an alias of Clean (name used by PROMPT Step 4).
func Normalize(raw string) (VPath, error) { return Clean(raw) }

// IsRoot reports whether v is the user's root.
func (v VPath) IsRoot() bool { return v == Root || v == "" }

// Rel returns the path relative to the os.Root: "." for the root itself.
func (v VPath) Rel() string {
	if v.IsRoot() {
		return "."
	}
	return strings.TrimPrefix(string(v), "/")
}

// Dir returns the parent directory (the root's parent is the root).
func (v VPath) Dir() VPath {
	if v.IsRoot() {
		return Root
	}
	return VPath(path.Dir(string(v)))
}

// Base returns the last segment ("" for the root).
func (v VPath) Base() string {
	if v.IsRoot() {
		return ""
	}
	return path.Base(string(v))
}

// Segments splits the path into its components (nil for the root).
func (v VPath) Segments() []string {
	if v.IsRoot() {
		return nil
	}
	return strings.Split(strings.TrimPrefix(string(v), "/"), "/")
}

// Depth is the number of segments.
func (v VPath) Depth() int { return len(v.Segments()) }

// HasPrefix reports whether v equals dir or lies below it, segment-aware:
// "/ab" is not below "/a".
func (v VPath) HasPrefix(dir VPath) bool {
	if dir.IsRoot() {
		return true
	}
	return v == dir || strings.HasPrefix(string(v), string(dir)+"/")
}

// String implements fmt.Stringer.
func (v VPath) String() string { return string(v) }

// Join appends a validated single name to dir. Warnings from ValidateName are
// discarded; use ValidateName first when the UI must show them.
func Join(dir VPath, name string) (VPath, error) {
	if _, err := ValidateName(name); err != nil {
		return "", err
	}
	name = norm.NFC.String(name)
	if dir.IsRoot() {
		if name == ReservedRootName {
			return "", invalidName("reserved")
		}
		return VPath("/" + name), nil
	}
	if dir.Depth()+1 > MaxDepth {
		return "", invalidPath(string(dir)+"/"+name, "too_deep")
	}
	v := VPath(string(dir) + "/" + name)
	if len(v) > MaxPathBytes {
		return "", invalidPath(string(v), "path_too_long")
	}
	return v, nil
}

// Warning codes returned by ValidateName.
const (
	WarnWindowsIncompatible = "windows_incompatible"
)

var windowsReserved = map[string]struct{}{
	"CON": {}, "PRN": {}, "AUX": {}, "NUL": {},
	"COM1": {}, "COM2": {}, "COM3": {}, "COM4": {}, "COM5": {}, "COM6": {}, "COM7": {}, "COM8": {}, "COM9": {},
	"LPT1": {}, "LPT2": {}, "LPT3": {}, "LPT4": {}, "LPT5": {}, "LPT6": {}, "LPT7": {}, "LPT8": {}, "LPT9": {},
}

// ValidateName checks a single file or directory name for create/rename
// (SPEC §5.2). Hard failures return NAME_INVALID with reason: empty,
// dot, slash, nul, control_char, backslash, invalid_utf8, too_long.
// Names that work on Linux but not on Windows yield the warning
// windows_incompatible (reserved device names, trailing dot/space, < > : " | ? *).
func ValidateName(name string) (warnings []string, err error) {
	switch name {
	case "":
		return nil, invalidName("empty")
	case ".", "..":
		return nil, invalidName("dot")
	}
	if !utf8.ValidString(name) {
		return nil, invalidName("invalid_utf8")
	}
	for _, r := range name {
		switch {
		case r == '/':
			return nil, invalidName("slash")
		case r == 0:
			return nil, invalidName("nul")
		case r < 0x20 || r == 0x7f:
			return nil, invalidName("control_char")
		case r == '\\':
			return nil, invalidName("backslash")
		}
	}
	if len(norm.NFC.String(name)) > MaxNameBytes {
		return nil, invalidName("too_long")
	}
	if windowsIncompatible(name) {
		warnings = append(warnings, WarnWindowsIncompatible)
	}
	return warnings, nil
}

func windowsIncompatible(name string) bool {
	if strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return true
	}
	if strings.ContainsAny(name, `<>:"|?*`) {
		return true
	}
	stem := name
	if i := strings.IndexByte(name, '.'); i >= 0 {
		stem = name[:i]
	}
	_, reserved := windowsReserved[strings.ToUpper(stem)]
	return reserved
}

// resolveLink computes where a relative symlink target placed in dir would
// resolve, segment-wise, without touching the filesystem. ok is false when the
// target is absolute or climbs above the root.
func resolveLink(dir VPath, target string) (VPath, bool) {
	if target == "" || strings.HasPrefix(target, "/") {
		return "", false
	}
	segs := dir.Segments()
	for _, seg := range strings.Split(target, "/") {
		switch seg {
		case "", ".":
			continue
		case "..":
			if len(segs) == 0 {
				return "", false
			}
			segs = segs[:len(segs)-1]
		default:
			segs = append(segs, seg)
		}
	}
	if len(segs) == 0 {
		return Root, true
	}
	return VPath("/" + strings.Join(segs, "/")), true
}

// checkVPath re-validates a VPath that a caller may have built by hand
// (defense in depth: every public Jail method calls it, so an unclean
// string such as "/a/.." can never reach os.Root).
func checkVPath(p VPath) error {
	if p == "" || p == Root {
		return nil
	}
	v, err := Clean(string(p))
	if err != nil {
		return err
	}
	if v != p {
		return invalidPath(string(p), "unclean")
	}
	return nil
}
