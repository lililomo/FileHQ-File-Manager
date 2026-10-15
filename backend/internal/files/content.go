package files

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path"
	"strconv"
	"strings"

	"kppbwi.app/filemanager/internal/fsjail"
)

// Editor content (SPEC §8.13 "Membuka file"): a regular file within
// EDITOR_MAX_FILE_SIZE is read whole, classified text/binary, decoded and
// its line endings measured; a larger file is only readable in tail mode
// (the last N lines, read backwards in 64 KiB blocks, at most TailMaxBytes).

const (
	// TailDefaultLines is the tail of a large file without an explicit count.
	TailDefaultLines = 1000
	// TailMaxBytes bounds what tail mode reads from the end of a file.
	TailMaxBytes = 2 << 20
	tailBlock    = 64 << 10
)

// BinaryError is returned for content the editor must not open.
type BinaryError struct{ Path string }

func (e *BinaryError) Error() string { return "binary file: " + e.Path }

// ContentOptions tune ReadContent.
type ContentOptions struct {
	MaxSize  int64  // EDITOR_MAX_FILE_SIZE (0 = unlimited)
	Encoding string // override ("" = detect)
	Tail     int    // > 0 = tail mode with this many lines
}

// Content is the decoded file the editor receives.
type Content struct {
	Path             string `json:"path"`
	Content          string `json:"content"`
	Encoding         string `json:"encoding"`
	DetectedEncoding string `json:"detectedEncoding"`
	BOM              bool   `json:"bom"`
	Lossy            bool   `json:"lossy"`
	EOL              string `json:"eol"`
	Language         string `json:"language"`
	Size             int64  `json:"size"`
	Mtime            string `json:"mtime"`
	Etag             string `json:"etag"`
	Mode             string `json:"mode"`
	Lines            int    `json:"lines"`
	Truncated        bool   `json:"truncated"`
	// Tail mode: byte offset the returned content starts at and the lines
	// it holds (TailLines ≤ requested when the whole file fit).
	Offset    int64 `json:"offset,omitempty"`
	TailLines int   `json:"tailLines,omitempty"`
	ReadOnly  bool  `json:"readOnly"`
}

// Etag is the weak identity of a file version: size and mtime (ns), the
// same form the listing and versions use.
func Etag(info fsjail.Info) string {
	return `"` + strconv.FormatInt(info.Size, 10) + "-" + strconv.FormatInt(info.ModTime.UnixNano(), 10) + `"`
}

// ReadContent opens p for the editor.
func ReadContent(j *fsjail.Jail, p fsjail.VPath, o ContentOptions) (*Content, error) {
	info, err := j.Stat(p)
	if err != nil {
		return nil, err
	}
	if info.IsDir {
		return nil, &ValidationError{Reason: "not_a_file", Path: string(p)}
	}
	f, err := j.Open(p)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return ReadContentFile(f, info, string(p), o)
}

// ReadContentFile decodes an already opened regular file (the editor path
// and the version blobs of Step 16b share it). name is the virtual path
// reported in Content.Path; its base name drives the language hint.
func ReadContentFile(f *os.File, info fsjail.Info, name string, o ContentOptions) (*Content, error) {
	p := fsjail.VPath(name)
	c := &Content{Path: string(p), Size: info.Size, Mtime: info.ModTime.UTC().Format("2006-01-02T15:04:05.000000000Z07:00"), Etag: Etag(info), Mode: "0" + strconv.FormatInt(int64(info.Mode.Perm()), 8), Language: LanguageHint(p.Base())}
	var err error
	var raw []byte
	switch {
	case o.MaxSize > 0 && info.Size > o.MaxSize:
		if o.Tail <= 0 {
			return nil, &FileTooLargeError{Size: info.Size, Max: o.MaxSize}
		}
		// the head of the file decides the encoding (BOM lives there)
		head := make([]byte, min(int64(BinarySample), info.Size))
		if _, err := io.ReadFull(f, head); err != nil {
			return nil, err
		}
		det := DetectEncoding(head)
		if !det.BOM && IsBinary(head) {
			return nil, &BinaryError{Path: string(p)}
		}
		tail, offset, lines, err := readTail(f, info.Size, o.Tail)
		if err != nil {
			return nil, err
		}
		raw = tail
		c.Truncated, c.ReadOnly, c.Offset, c.TailLines = true, true, offset, lines
		enc := o.Encoding
		if enc == "" {
			enc = det.Encoding
			if !det.Confident || offset > 0 {
				// the tail alone is what we have: detect on it, keep the
				// head's BOM verdict when it was certain
				if !det.BOM {
					enc = DetectEncoding(raw).Encoding
				}
			}
		}
		c.DetectedEncoding = det.Encoding
		c.BOM = det.BOM
		text, lossy, derr := DecodeText(raw, enc)
		if derr != nil {
			return nil, derr
		}
		c.Content, c.Lossy, c.Encoding = text, lossy, enc
	default:
		raw, err = io.ReadAll(io.LimitReader(f, info.Size+1))
		if err != nil {
			return nil, err
		}
		det := DetectEncoding(raw)
		if !det.BOM && IsBinary(raw) {
			return nil, &BinaryError{Path: string(p)}
		}
		enc := o.Encoding
		if enc == "" {
			enc = det.Encoding
		}
		c.DetectedEncoding, c.BOM = det.Encoding, det.BOM
		text, lossy, derr := DecodeText(raw, enc)
		if derr != nil {
			return nil, derr
		}
		c.Content, c.Lossy, c.Encoding = text, lossy, enc
	}
	c.EOL = DetectEOL(c.Content)
	c.Lines = countLines(c.Content)
	return c, nil
}

// countLines is the editor's line count (a trailing newline does not add one).
func countLines(text string) int {
	if text == "" {
		return 0
	}
	n := strings.Count(text, "\n")
	if !strings.HasSuffix(text, "\n") {
		n++
	}
	return n
}

// readTail returns the last lines of f (size bytes) starting at a line
// boundary, reading backwards in 64 KiB blocks and never more than
// TailMaxBytes. lines reports how many lines the slice holds.
func readTail(f io.ReaderAt, size int64, want int) (data []byte, offset int64, lines int, err error) {
	if size == 0 || want <= 0 {
		return nil, 0, 0, nil
	}
	limit := min(size, int64(TailMaxBytes))
	start := size
	var buf []byte
	for start > size-limit {
		chunk := int64(tailBlock)
		if start-chunk < size-limit {
			chunk = start - (size - limit)
		}
		start -= chunk
		block := make([]byte, chunk)
		if _, err := f.ReadAt(block, start); err != nil && !errors.Is(err, io.EOF) {
			return nil, 0, 0, err
		}
		buf = append(block, buf...)
		if bytes.Count(buf, []byte{'\n'}) > want {
			break
		}
	}
	// keep only the last `want` lines: find the newline that precedes them
	nl := 0
	cut := -1
	for i := len(buf) - 1; i >= 0; i-- {
		if buf[i] == '\n' {
			nl++
			// a trailing newline terminates the last line; count lines, not separators
			if i == len(buf)-1 {
				continue
			}
			if nl > want {
				cut = i
				break
			}
		}
	}
	if cut >= 0 {
		buf = buf[cut+1:]
		offset = size - int64(len(buf))
	} else {
		offset = start
	}
	if offset > 0 && cut < 0 {
		// window cut mid-line: drop the partial first line
		if i := bytes.IndexByte(buf, '\n'); i >= 0 && int64(len(buf))-int64(i+1) > 0 {
			buf = buf[i+1:]
			offset = size - int64(len(buf))
		}
	}
	return buf, offset, countLines(string(buf)), nil
}

// languageByExt is the server-side hint of SPEC §8.13 step 5; the editor
// refines it (shebang, special names). Values are Monaco language ids.
var languageByExt = map[string]string{
	"js": "javascript", "mjs": "javascript", "cjs": "javascript", "jsx": "javascript",
	"ts": "typescript", "mts": "typescript", "cts": "typescript", "tsx": "typescript",
	"json": "json", "jsonc": "json", "json5": "json", "webmanifest": "json",
	"html": "html", "htm": "html", "xhtml": "html", "vue": "html", "svelte": "html", "jsp": "html",
	"css": "css", "scss": "scss", "less": "less",
	"md": "markdown", "markdown": "markdown", "mdx": "mdx",
	"xml": "xml", "xsl": "xml", "xslt": "xml", "svg": "xml", "plist": "xml", "csproj": "xml", "wsdl": "xml",
	"yml": "yaml", "yaml": "yaml",
	"toml": "ini", "ini": "ini", "cfg": "ini", "env": "ini", "properties": "ini", "conf": "ini",
	"sh": "shell", "bash": "shell", "zsh": "shell", "fish": "shell",
	"py": "python", "pyw": "python", "rb": "ruby", "php": "php", "phtml": "php",
	"go": "go", "rs": "rust", "java": "java", "kt": "kotlin", "kts": "kotlin", "scala": "scala",
	"c": "c", "h": "c", "cpp": "cpp", "cc": "cpp", "cxx": "cpp", "hpp": "cpp", "hh": "cpp",
	"cs": "csharp", "fs": "fsharp", "vb": "vb", "vbs": "vb",
	"swift": "swift", "m": "objective-c", "dart": "dart", "lua": "lua", "pl": "perl", "pm": "perl",
	"r": "r", "sql": "sql", "graphql": "graphql", "gql": "graphql", "proto": "protobuf",
	"ps1": "powershell", "psm1": "powershell", "bat": "bat", "cmd": "bat",
	"dockerfile": "dockerfile", "makefile": "makefile", "mk": "makefile",
	"tf": "hcl", "hcl": "hcl", "clj": "clojure", "ex": "elixir", "exs": "elixir", "elm": "elm",
	"txt": "plaintext", "log": "plaintext", "csv": "plaintext", "tsv": "plaintext",
	"v": "systemverilog", "sv": "systemverilog", "tcl": "tcl", "twig": "twig", "hbs": "handlebars", "pug": "pug", "liquid": "liquid",
	"htaccess": "apache", "nginx": "nginx",
}

var languageByName = map[string]string{
	"dockerfile": "dockerfile", "containerfile": "dockerfile", "makefile": "makefile", "gnumakefile": "makefile",
	".env": "ini", ".gitignore": "plaintext", ".gitattributes": "plaintext", ".editorconfig": "ini", ".npmrc": "ini",
	"nginx.conf": "nginx", "mime.types": "nginx", ".htaccess": "apache", "httpd.conf": "apache", "apache2.conf": "apache",
	"composer.json": "json", "package.json": "json", "tsconfig.json": "json", "cmakelists.txt": "plaintext",
}

// LanguageHint maps a file name to a Monaco language id ("plaintext" when
// unknown). Compound names: *.blade.php → php, *.env.* → ini.
func LanguageHint(name string) string {
	lower := strings.ToLower(name)
	if l, ok := languageByName[lower]; ok {
		return l
	}
	if strings.HasPrefix(lower, ".env") {
		return "ini"
	}
	if strings.HasSuffix(lower, ".blade.php") {
		return "php"
	}
	ext := strings.TrimPrefix(path.Ext(lower), ".")
	if l, ok := languageByExt[ext]; ok {
		return l
	}
	return "plaintext"
}
