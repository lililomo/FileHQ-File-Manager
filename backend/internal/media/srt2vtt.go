package media

import (
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Subtitle conversion and sanitising (SPEC §8.14 VideoPlayer, Step 17b).
// Sidecar subtitles are user content shown inside the application origin, so
// the server re-emits every cue: only timing, a short whitelist of cue
// settings and the cue tags <b>, <i>, <u> survive; any other markup
// (<font>, <script>, <c.class>, <v Name>, STYLE/REGION/NOTE blocks) is
// dropped and stray "<", ">", "&" are escaped.

// MaxSubtitleBytes bounds what the subtitle endpoint reads (2 MiB is far
// beyond a feature film's subtitle file).
const MaxSubtitleBytes = 2 << 20

// SubtitleExts are the sidecar formats recognised next to a video.
var SubtitleExts = []string{".vtt", ".srt"}

var (
	// 00:01:02,345 / 00:01:02.345 / 01:02.345 (VTT allows omitting hours);
	// one or two hour digits up to many for long recordings.
	timingRe = regexp.MustCompile(`^\s*((?:\d+:)?\d{1,2}:\d{1,2}[,.]\d{1,3})\s*-->\s*((?:\d+:)?\d{1,2}:\d{1,2}[,.]\d{1,3})(.*)$`)
	// WebVTT cue settings kept as-is when well-formed.
	settingRe = regexp.MustCompile(`^(vertical:(rl|lr)|line:-?\d{1,3}(\.\d+)?%?(,(start|center|end))?|position:\d{1,3}(\.\d+)?%(,(line-left|center|line-right))?|size:\d{1,3}(\.\d+)?%|align:(start|center|end|left|right))$`)
	tagRe     = regexp.MustCompile(`^</?([a-zA-Z]+)[^<>]*>`)
	stampRe   = regexp.MustCompile(`^<\d[\d:.]*>`)
	entityRe  = regexp.MustCompile(`^&(amp|lt|gt|nbsp|lrm|rlm);`)
)

// Cue is one subtitle entry.
type Cue struct {
	Start, End string // WebVTT timestamps hh:mm:ss.ttt
	Settings   string
	Text       string // sanitised cue text (may span lines)
}

// SRTToVTT converts SubRip text (already decoded to UTF-8) into a sanitised
// WebVTT document.
func SRTToVTT(src string) string { return renderVTT(parseCues(src)) }

// SanitizeVTT re-emits a WebVTT document keeping only its cues.
func SanitizeVTT(src string) string { return renderVTT(parseCues(src)) }

// parseCues reads SRT or VTT blocks: every block with a timing line becomes
// a cue (index and identifier lines are ignored). Header, NOTE, STYLE and
// REGION blocks have no timing line and are therefore skipped.
func parseCues(src string) []Cue {
	src = strings.TrimPrefix(src, "\uFEFF")
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = strings.ReplaceAll(src, "\r", "\n")
	var cues []Cue
	lines := strings.Split(src, "\n")
	for i := 0; i < len(lines); i++ {
		m := timingRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		start, ok1 := normTime(m[1])
		end, ok2 := normTime(m[2])
		var text []string
		j := i + 1
		for ; j < len(lines) && strings.TrimSpace(lines[j]) != ""; j++ {
			if timingRe.MatchString(lines[j]) {
				break // missing blank line between cues
			}
			text = append(text, lines[j])
		}
		i = j - 1
		if !ok1 || !ok2 {
			continue
		}
		body := sanitizeCueText(strings.Join(text, "\n"))
		cues = append(cues, Cue{Start: start, End: end, Settings: cueSettings(m[3]), Text: body})
	}
	return cues
}

func renderVTT(cues []Cue) string {
	var b strings.Builder
	b.WriteString("WEBVTT\n\n")
	for _, c := range cues {
		b.WriteString(c.Start)
		b.WriteString(" --> ")
		b.WriteString(c.End)
		if c.Settings != "" {
			b.WriteByte(' ')
			b.WriteString(c.Settings)
		}
		b.WriteByte('\n')
		if c.Text != "" {
			b.WriteString(c.Text)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// normTime turns "1:02:03,4" / "02:03.456" into "01:02:03.400".
func normTime(s string) (string, bool) {
	s = strings.ReplaceAll(s, ",", ".")
	main, frac, _ := strings.Cut(s, ".")
	parts := strings.Split(main, ":")
	if len(parts) == 2 {
		parts = append([]string{"0"}, parts...)
	}
	if len(parts) != 3 || len(parts[0]) > 4 {
		return "", false
	}
	for len(frac) < 3 {
		frac += "0"
	}
	pad := func(v string) string {
		if len(v) < 2 {
			return strings.Repeat("0", 2-len(v)) + v
		}
		return v
	}
	if mm, ss := atoi2(parts[1]), atoi2(parts[2]); mm > 59 || ss > 59 {
		return "", false
	}
	return pad(parts[0]) + ":" + pad(parts[1]) + ":" + pad(parts[2]) + "." + frac, true
}

func atoi2(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}

// cueSettings keeps the well-formed WebVTT settings of a timing line; SRT
// coordinates ("X1:… Y1:…") and anything else are dropped.
func cueSettings(rest string) string {
	var out []string
	for _, f := range strings.Fields(rest) {
		if settingRe.MatchString(f) {
			out = append(out, f)
		}
	}
	return strings.Join(out, " ")
}

// allowedCueTags may appear in sanitised cue text (opening and closing).
var allowedCueTags = []string{"b", "i", "u"}

// sanitizeCueText keeps text plus <b>/<i>/<u>; every other tag is removed,
// lone "<"/">" and unknown entities are escaped (so "-->" cannot survive
// as a literal and end the cue early).
func sanitizeCueText(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		switch s[i] {
		case '<':
			if m := stampRe.FindString(s[i:]); m != "" {
				i += len(m) // karaoke timestamp tags are dropped
				continue
			}
			if m := tagRe.FindStringSubmatch(s[i:]); m != nil {
				name := strings.ToLower(m[1])
				if slices.Contains(allowedCueTags, name) {
					if strings.HasPrefix(m[0], "</") {
						b.WriteString("</" + name + ">")
					} else {
						b.WriteString("<" + name + ">")
					}
				}
				i += len(m[0])
				continue
			}
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '&':
			if m := entityRe.FindString(s[i:]); m != "" {
				b.WriteString(m)
				i += len(m)
				continue
			}
			b.WriteString("&amp;")
		default:
			b.WriteByte(s[i])
		}
		i++
	}
	// blank lines would end the cue: collapse them
	lines := strings.Split(b.String(), "\n")
	kept := lines[:0]
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			kept = append(kept, strings.TrimRight(l, " \t"))
		}
	}
	return strings.Join(kept, "\n")
}

// Sidecar is a subtitle file found next to a video.
type Sidecar struct {
	Name   string // file name in the video's folder
	Lang   string // "en", "id", "pt-BR" or "" (no language suffix)
	Label  string // what the track menu shows
	Format string // "vtt" or "srt"
}

var langRe = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})?$`)

// FindSidecars picks the subtitles of video among the names of its folder:
// <base>.vtt, <base>.srt and <base>.<label>.(vtt|srt) (label ≤ 32 bytes; a
// language tag such as "en" or "pt-BR" becomes Lang). Matching is
// case-insensitive on the extension and exact on the base name. A .vtt wins
// over an .srt with the same label. Sorted: no label first, then by label.
func FindSidecars(video string, names []string) []Sidecar {
	dot := strings.LastIndexByte(video, '.')
	if dot <= 0 {
		return nil
	}
	base := video[:dot]
	byLabel := map[string]Sidecar{}
	for _, n := range names {
		if len(n) < len(base)+4 || !strings.HasPrefix(n, base) || n[len(base)] != '.' {
			continue
		}
		ext := ""
		for _, e := range SubtitleExts {
			if strings.HasSuffix(strings.ToLower(n), e) {
				ext = e
			}
		}
		if ext == "" {
			continue
		}
		// "film.srt" → "", "film.en.srt" → "en"
		label := strings.TrimPrefix(n[len(base):len(n)-len(ext)], ".")
		if len(label) > 32 || strings.ContainsAny(label, "/\\") {
			continue
		}
		sc := Sidecar{Name: n, Label: label, Format: ext[1:]}
		if langRe.MatchString(label) {
			sc.Lang = label
		}
		if prev, ok := byLabel[strings.ToLower(label)]; ok && prev.Format == "vtt" {
			continue
		}
		byLabel[strings.ToLower(label)] = sc
	}
	out := make([]Sidecar, 0, len(byLabel))
	for _, sc := range byLabel {
		out = append(out, sc)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Label) < strings.ToLower(out[j].Label) })
	return out
}

// IsSubtitleName reports whether name has a sidecar subtitle extension.
func IsSubtitleName(name string) bool {
	l := strings.ToLower(name)
	for _, e := range SubtitleExts {
		if strings.HasSuffix(l, e) {
			return true
		}
	}
	return false
}
