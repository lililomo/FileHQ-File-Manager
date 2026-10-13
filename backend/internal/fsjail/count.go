package fsjail

import (
	"errors"
	"io"

	"golang.org/x/sys/unix"
)

// CountEntries counts the direct children of directory p using getdents only
// (no stat per entry), which is what the listing uses for the itemCount
// column (SPEC §8.3). When max > 0 and the directory holds more than max
// entries, counting stops early and truncated is true.
func (j *Jail) CountEntries(p VPath, max int) (n int, truncated bool, err error) {
	if err := checkVPath(p); err != nil {
		return 0, false, err
	}
	df, err := j.root.Open(p.Rel())
	if err != nil {
		return 0, false, wrap("count", p, err)
	}
	defer func() { _ = df.Close() }()
	for {
		names, rerr := df.Readdirnames(512)
		n += len(names)
		if max > 0 && n > max {
			return max, true, nil
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				return n, false, nil
			}
			if errors.Is(rerr, unix.ENOTDIR) {
				return 0, false, newErr(CodeNotDir, "count", p, "not_dir", rerr)
			}
			return 0, false, wrap("count", p, rerr)
		}
	}
}
