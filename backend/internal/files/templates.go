package files

import (
	"embed"
	"io/fs"
	"sort"
	"strings"
)

// Templates for "Baru → file dari template" (PROMPT Step 5, SPEC §6.3
// files/create). Each file is tiny and contains no secrets.
//
//go:embed templates/*.tmpl
var templateFS embed.FS

var templateNames = func() []string {
	entries, err := fs.ReadDir(templateFS, "templates")
	if err != nil {
		panic(err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, strings.TrimSuffix(e.Name(), ".tmpl"))
	}
	sort.Strings(out)
	return out
}()

// TemplateNames lists the available template identifiers (= extension).
func TemplateNames() []string { return append([]string(nil), templateNames...) }

// Template returns the content of a template; ok is false for unknown names.
func Template(name string) (content []byte, ok bool) {
	for _, n := range templateNames {
		if n == name {
			b, err := templateFS.ReadFile("templates/" + name + ".tmpl")
			if err != nil {
				return nil, false
			}
			return b, true
		}
	}
	return nil, false
}
