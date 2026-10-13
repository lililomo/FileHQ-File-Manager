// Package fixture generates large directory trees for listing tests and
// benchmarks (PROMPT Step 5 task 8). It writes through fsjail so it needs no
// lint exception and doubles as an exercise of the jail.
package fixture

import (
	"fmt"
	"strconv"

	"kppbwi.app/filemanager/internal/fsjail"
)

var exts = []string{"txt", "jpg", "go", "md", "pdf", "zip", "mp4", "csv"}

// Generate creates n entries in dir (which must exist): mostly files named
// file<i>.<ext> with rotating extensions, every 50th entry a folder, plus a
// sprinkle of Unicode, mixed-case and dotfile names so sorting is exercised.
func Generate(dir string, n int) error {
	j, err := fsjail.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = j.Close() }()
	for i := 1; i <= n; i++ {
		var name string
		switch {
		case i%50 == 0:
			name = "folder" + strconv.Itoa(i)
			if err := j.Mkdir(fsjail.VPath("/" + name)); err != nil {
				return fmt.Errorf("fixture mkdir %s: %w", name, err)
			}
			continue
		case i%97 == 0:
			name = "é-unicode-" + strconv.Itoa(i) + ".txt"
		case i%89 == 0:
			name = "Zeta" + strconv.Itoa(i) + ".TXT"
		case i%101 == 0:
			name = ".hidden" + strconv.Itoa(i)
		default:
			name = "file" + strconv.Itoa(i) + "." + exts[i%len(exts)]
		}
		f, err := j.Create(fsjail.VPath("/"+name), true)
		if err != nil {
			return fmt.Errorf("fixture create %s: %w", name, err)
		}
		if i%7 == 0 {
			_, _ = f.WriteString("fixture content " + strconv.Itoa(i))
		}
		_ = f.Close()
	}
	return nil
}
