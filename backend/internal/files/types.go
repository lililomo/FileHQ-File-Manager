// Package files holds file-type knowledge shared by listing, viewers and the
// UI (SPEC §8.3 typeLabel, §8.14 MIME → viewer) plus the "new file" templates.
package files

import (
	"strings"
)

// Category groups file types for icons, the types= filter and viewers.
type Category string

// Categories (SPEC §8.3 "types=").
const (
	CatFolder       Category = "folder"
	CatImage        Category = "image"
	CatVideo        Category = "video"
	CatAudio        Category = "audio"
	CatArchive      Category = "archive"
	CatCode         Category = "code"
	CatDocument     Category = "document"
	CatSpreadsheet  Category = "spreadsheet"
	CatPresentation Category = "presentation"
	CatPDF          Category = "pdf"
	CatText         Category = "text"
	CatFont         Category = "font"
	CatOther        Category = "other"
)

// Categories lists every category accepted by the types= filter.
func Categories() []Category {
	return []Category{CatFolder, CatImage, CatVideo, CatAudio, CatArchive, CatCode, CatDocument,
		CatSpreadsheet, CatPresentation, CatPDF, CatText, CatFont, CatOther}
}

// ParseCategory validates a types= token.
func ParseCategory(s string) (Category, bool) {
	for _, c := range Categories() {
		if string(c) == s {
			return c, true
		}
	}
	return "", false
}

// TypeInfo is what the listing derives from a file name alone (no I/O).
type TypeInfo struct {
	Ext      string   // lowercase extension without dot ("gz" for x.tar.gz; "" when none)
	Mime     string   // "" when unknown (serialized as null)
	Category Category // never empty
	Label    string   // i18n key, e.g. type.image.jpeg, type.folder, type.other
	Thumb    bool     // server can produce a thumbnail (image/video/pdf)
}

type typeDef struct {
	mime string
	cat  Category
	key  string // label suffix; defaults to the extension
}

// Extension table. Keys are lowercase extensions; compound keys (tar.gz) are
// matched before single ones. Label keys follow type.<category>.<key>.
var byExt = map[string]typeDef{
	// images
	"jpg": {"image/jpeg", CatImage, "jpeg"}, "jpeg": {"image/jpeg", CatImage, "jpeg"},
	"png": {"image/png", CatImage, "png"}, "gif": {"image/gif", CatImage, "gif"},
	"webp": {"image/webp", CatImage, "webp"}, "avif": {"image/avif", CatImage, "avif"},
	"bmp": {"image/bmp", CatImage, "bmp"}, "ico": {"image/x-icon", CatImage, "ico"},
	"svg": {"image/svg+xml", CatImage, "svg"}, "tif": {"image/tiff", CatImage, "tiff"},
	"tiff": {"image/tiff", CatImage, "tiff"}, "heic": {"image/heic", CatImage, "heic"},
	"heif": {"image/heif", CatImage, "heif"}, "psd": {"image/vnd.adobe.photoshop", CatImage, "psd"},
	// video
	"mp4": {"video/mp4", CatVideo, "mp4"}, "m4v": {"video/x-m4v", CatVideo, "mp4"},
	"webm": {"video/webm", CatVideo, "webm"}, "mkv": {"video/x-matroska", CatVideo, "mkv"},
	"mov": {"video/quicktime", CatVideo, "mov"}, "avi": {"video/x-msvideo", CatVideo, "avi"},
	"ogv": {"video/ogg", CatVideo, "ogv"}, "wmv": {"video/x-ms-wmv", CatVideo, "wmv"},
	"flv": {"video/x-flv", CatVideo, "flv"}, "3gp": {"video/3gpp", CatVideo, "3gp"},
	"mpg": {"video/mpeg", CatVideo, "mpeg"}, "mpeg": {"video/mpeg", CatVideo, "mpeg"},
	// audio
	"mp3": {"audio/mpeg", CatAudio, "mp3"}, "m4a": {"audio/mp4", CatAudio, "m4a"},
	"aac": {"audio/aac", CatAudio, "aac"}, "ogg": {"audio/ogg", CatAudio, "ogg"},
	"oga": {"audio/ogg", CatAudio, "ogg"}, "opus": {"audio/opus", CatAudio, "opus"},
	"wav": {"audio/wav", CatAudio, "wav"}, "flac": {"audio/flac", CatAudio, "flac"},
	"wma": {"audio/x-ms-wma", CatAudio, "wma"}, "aiff": {"audio/aiff", CatAudio, "aiff"},
	"mid": {"audio/midi", CatAudio, "midi"}, "midi": {"audio/midi", CatAudio, "midi"},
	// archives
	"zip": {"application/zip", CatArchive, "zip"}, "7z": {"application/x-7z-compressed", CatArchive, "7z"},
	"rar": {"application/vnd.rar", CatArchive, "rar"}, "tar": {"application/x-tar", CatArchive, "tar"},
	"gz": {"application/gzip", CatArchive, "gzip"}, "tgz": {"application/gzip", CatArchive, "targz"},
	"tar.gz": {"application/gzip", CatArchive, "targz"}, "bz2": {"application/x-bzip2", CatArchive, "bzip2"},
	"tbz2": {"application/x-bzip2", CatArchive, "tarbz2"}, "tar.bz2": {"application/x-bzip2", CatArchive, "tarbz2"},
	"xz": {"application/x-xz", CatArchive, "xz"}, "txz": {"application/x-xz", CatArchive, "tarxz"},
	"tar.xz": {"application/x-xz", CatArchive, "tarxz"}, "zst": {"application/zstd", CatArchive, "zstd"},
	"tzst": {"application/zstd", CatArchive, "tarzst"}, "tar.zst": {"application/zstd", CatArchive, "tarzst"},
	"lz4": {"application/x-lz4", CatArchive, "lz4"}, "war": {"application/java-archive", CatArchive, "war"},
	"jar": {"application/java-archive", CatArchive, "jar"}, "iso": {"application/x-iso9660-image", CatArchive, "iso"},
	"deb": {"application/vnd.debian.binary-package", CatArchive, "deb"}, "rpm": {"application/x-rpm", CatArchive, "rpm"},
	// code & config
	"js": {"text/javascript", CatCode, "javascript"}, "mjs": {"text/javascript", CatCode, "javascript"},
	"cjs": {"text/javascript", CatCode, "javascript"}, "jsx": {"text/jsx", CatCode, "jsx"},
	"ts": {"text/typescript", CatCode, "typescript"}, "tsx": {"text/tsx", CatCode, "tsx"},
	"go": {"text/x-go", CatCode, "go"}, "py": {"text/x-python", CatCode, "python"},
	"rb": {"text/x-ruby", CatCode, "ruby"}, "php": {"application/x-httpd-php", CatCode, "php"},
	"java": {"text/x-java-source", CatCode, "java"}, "c": {"text/x-c", CatCode, "c"},
	"h": {"text/x-c", CatCode, "c"}, "cpp": {"text/x-c++", CatCode, "cpp"},
	"cc": {"text/x-c++", CatCode, "cpp"}, "hpp": {"text/x-c++", CatCode, "cpp"},
	"cs": {"text/x-csharp", CatCode, "csharp"}, "rs": {"text/x-rust", CatCode, "rust"},
	"swift": {"text/x-swift", CatCode, "swift"}, "kt": {"text/x-kotlin", CatCode, "kotlin"},
	"scala": {"text/x-scala", CatCode, "scala"}, "sh": {"application/x-sh", CatCode, "shell"},
	"bash": {"application/x-sh", CatCode, "shell"}, "zsh": {"application/x-sh", CatCode, "shell"},
	"ps1": {"text/x-powershell", CatCode, "powershell"}, "bat": {"text/x-batch", CatCode, "batch"},
	"cmd": {"text/x-batch", CatCode, "batch"}, "lua": {"text/x-lua", CatCode, "lua"},
	"pl": {"text/x-perl", CatCode, "perl"}, "r": {"text/x-r", CatCode, "r"},
	"sql": {"application/sql", CatCode, "sql"}, "vue": {"text/x-vue", CatCode, "vue"},
	"svelte": {"text/x-svelte", CatCode, "svelte"}, "css": {"text/css", CatCode, "css"},
	"scss": {"text/x-scss", CatCode, "scss"}, "sass": {"text/x-sass", CatCode, "sass"},
	"less": {"text/x-less", CatCode, "less"}, "html": {"text/html", CatCode, "html"},
	"htm": {"text/html", CatCode, "html"}, "xml": {"application/xml", CatCode, "xml"},
	"json": {"application/json", CatCode, "json"}, "yaml": {"application/yaml", CatCode, "yaml"},
	"yml": {"application/yaml", CatCode, "yaml"}, "toml": {"application/toml", CatCode, "toml"},
	"ini": {"text/plain", CatCode, "ini"}, "conf": {"text/plain", CatCode, "conf"},
	"cfg": {"text/plain", CatCode, "conf"}, "env": {"text/plain", CatCode, "env"},
	"vb": {"text/x-vb", CatCode, "vb"}, "vbs": {"text/x-vbscript", CatCode, "vbscript"},
	"v": {"text/x-verilog", CatCode, "verilog"}, "dockerfile": {"text/plain", CatCode, "dockerfile"},
	"makefile": {"text/x-makefile", CatCode, "makefile"}, "htaccess": {"text/plain", CatCode, "apache"},
	// documents
	"doc":  {"application/msword", CatDocument, "word"},
	"docx": {"application/vnd.openxmlformats-officedocument.wordprocessingml.document", CatDocument, "word"},
	"odt":  {"application/vnd.oasis.opendocument.text", CatDocument, "odt"},
	"rtf":  {"application/rtf", CatDocument, "rtf"}, "pages": {"application/vnd.apple.pages", CatDocument, "pages"},
	"xls":  {"application/vnd.ms-excel", CatSpreadsheet, "excel"},
	"xlsx": {"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", CatSpreadsheet, "excel"},
	"ods":  {"application/vnd.oasis.opendocument.spreadsheet", CatSpreadsheet, "ods"},
	"csv":  {"text/csv", CatSpreadsheet, "csv"}, "tsv": {"text/tab-separated-values", CatSpreadsheet, "tsv"},
	"ppt":  {"application/vnd.ms-powerpoint", CatPresentation, "powerpoint"},
	"pptx": {"application/vnd.openxmlformats-officedocument.presentationml.presentation", CatPresentation, "powerpoint"},
	"odp":  {"application/vnd.oasis.opendocument.presentation", CatPresentation, "odp"},
	"key":  {"application/vnd.apple.keynote", CatPresentation, "keynote"},
	"pdf":  {"application/pdf", CatPDF, "pdf"},
	// text
	"txt": {"text/plain", CatText, "plain"}, "md": {"text/markdown", CatText, "markdown"},
	"markdown": {"text/markdown", CatText, "markdown"}, "log": {"text/plain", CatText, "log"},
	"nfo": {"text/plain", CatText, "plain"}, "srt": {"application/x-subrip", CatText, "subtitle"},
	"vtt": {"text/vtt", CatText, "subtitle"},
	// fonts
	"ttf": {"font/ttf", CatFont, "ttf"}, "otf": {"font/otf", CatFont, "otf"},
	"woff": {"font/woff", CatFont, "woff"}, "woff2": {"font/woff2", CatFont, "woff2"},
	// other binaries worth a label
	"exe": {"application/vnd.microsoft.portable-executable", CatOther, "exe"},
	"apk": {"application/vnd.android.package-archive", CatOther, "apk"},
	"db":  {"application/vnd.sqlite3", CatOther, "database"}, "sqlite": {"application/vnd.sqlite3", CatOther, "database"},
}

// Names without an extension that still have a known type.
var byName = map[string]string{
	"dockerfile": "dockerfile", "makefile": "makefile", "gnumakefile": "makefile",
	".htaccess": "htaccess", ".env": "env", ".gitignore": "conf", ".editorconfig": "ini", "license": "txt", "readme": "txt",
}

var compoundExts = []string{"tar.gz", "tar.bz2", "tar.xz", "tar.zst"}

// Ext returns the lowercase extension of name without the dot. Dotfiles
// (".bashrc") and names without a dot yield "". Compound archive extensions
// return their full form ("tar.gz").
func Ext(name string) string {
	lower := strings.ToLower(name)
	for _, c := range compoundExts {
		if strings.HasSuffix(lower, "."+c) && len(lower) > len(c)+1 {
			return c
		}
	}
	i := strings.LastIndexByte(lower, '.')
	if i <= 0 || i == len(lower)-1 {
		return ""
	}
	return lower[i+1:]
}

// Lookup classifies a regular file by its name.
func Lookup(name string) TypeInfo {
	ext := Ext(name)
	key := ext
	if key == "" {
		if k, ok := byName[strings.ToLower(name)]; ok {
			key = k
		}
	}
	def, ok := byExt[key]
	if !ok {
		return TypeInfo{Ext: ext, Category: CatOther, Label: "type.other"}
	}
	label := def.key
	if label == "" {
		label = key
	}
	return TypeInfo{
		Ext:      ext,
		Mime:     def.mime,
		Category: def.cat,
		Label:    "type." + string(def.cat) + "." + label,
		Thumb:    def.cat == CatImage || def.cat == CatVideo || def.cat == CatPDF,
	}
}

// Folder is the TypeInfo of a directory.
func Folder() TypeInfo { return TypeInfo{Category: CatFolder, Label: "type.folder"} }
