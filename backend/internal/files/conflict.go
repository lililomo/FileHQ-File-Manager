package files

import (
	"fmt"
	"strings"

	"kppbwi.app/filemanager/internal/fsjail"
)

// Policy decides what happens when a destination already exists.
type Policy string

// Conflict policies (SPEC §8.5, PROMPT Step 9a on_conflict).
const (
	PolicyFail      Policy = "fail"
	PolicyOverwrite Policy = "overwrite"
	PolicySkip      Policy = "skip"
	PolicyRename    Policy = "rename"
)

// ParsePolicy accepts "", fail, ask (alias of fail), overwrite, skip, rename.
func ParsePolicy(s string) (Policy, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "fail", "ask":
		return PolicyFail, true
	case "overwrite":
		return PolicyOverwrite, true
	case "skip":
		return PolicySkip, true
	case "rename":
		return PolicyRename, true
	}
	return "", false
}

// ConflictPolicy is the request-level default plus per-item overrides keyed
// by source path.
type ConflictPolicy struct {
	Default Policy
	PerItem map[string]Policy
}

// For returns the policy for a source path.
func (c ConflictPolicy) For(src fsjail.VPath) Policy {
	if p, ok := c.PerItem[string(src)]; ok && p != "" {
		return p
	}
	if c.Default == "" {
		return PolicyFail
	}
	return c.Default
}

// statOf converts an Info to the API triple.
func statOf(info fsjail.Info) Stat {
	return Stat{Size: info.Size, Mtime: info.ModTime.UTC().Format("2006-01-02T15:04:05.000Z"), IsDir: info.IsDir}
}

// SplitExt splits name into stem and extension ("" when none), compound
// extensions included (a.tar.gz → "a", ".tar.gz"). Folders have no extension.
func SplitExt(name string, isDir bool) (stem, ext string) {
	if isDir {
		return name, ""
	}
	e := Ext(name)
	if e == "" || len(e)+1 >= len(name) {
		return name, ""
	}
	// keep the original spelling of the extension (Ext lowercases)
	cut := len(name) - len(e) - 1
	return name[:cut], name[cut:]
}

// UniqueName returns the first "name (n).ext" (n ≥ 1) that does not exist in
// dir (SPEC §8.5 rename policy, duplicate).
func UniqueName(j *fsjail.Jail, dir fsjail.VPath, name string, isDir bool) (fsjail.VPath, error) {
	stem, ext := SplitExt(name, isDir)
	for n := 1; n <= 10000; n++ {
		candidate := fmt.Sprintf("%s (%d)%s", stem, n, ext)
		p, err := fsjail.Join(dir, candidate)
		if err != nil {
			return "", err
		}
		if _, err := j.Lstat(p); err != nil {
			if isNotFound(err) {
				return p, nil
			}
			return "", err
		}
	}
	return "", &ValidationError{Reason: "no_free_name", Path: string(dir)}
}
