package media

import (
	"reflect"
	"strings"
	"testing"
)

func TestSRTToVTTFormat(t *testing.T) {
	src := "\uFEFF1\r\n00:00:01,000 --> 00:00:02,500\r\nHalo dunia\r\n\r\n" +
		"2\r\n00:00:03,5 --> 00:00:04,250 X1:100 X2:200\r\nBaris satu\r\nBaris dua\r\n\r\n" +
		// missing blank line between cues and hours over 99
		"3\n123:00:05,000 --> 123:00:06,000\nPanjang\n4\n00:00:07,000 --> 00:00:08,000\nTanpa jeda\n"
	got := SRTToVTT(src)
	want := "WEBVTT\n\n" +
		"00:00:01.000 --> 00:00:02.500\nHalo dunia\n\n" +
		"00:00:03.500 --> 00:00:04.250\nBaris satu\nBaris dua\n\n" +
		"123:00:05.000 --> 123:00:06.000\nPanjang\n4\n\n" +
		"00:00:07.000 --> 00:00:08.000\nTanpa jeda\n\n"
	if got != want {
		t.Fatalf("SRTToVTT:\n%q\nwant\n%q", got, want)
	}
}

func TestSRTToVTTSkipsBrokenBlocks(t *testing.T) {
	src := "1\n00:00:01,000 --> 00:00:02,000\nok\n\n2\nnot a timing\ntext\n\n3\n00:61:01,000 --> 00:00:02,000\nbad minutes\n\n"
	got := SRTToVTT(src)
	if strings.Count(got, "-->") != 1 || !strings.Contains(got, "\nok\n") || strings.Contains(got, "bad minutes") {
		t.Fatalf("broken blocks: %q", got)
	}
	if SRTToVTT("") != "WEBVTT\n\n" {
		t.Fatal("empty input")
	}
}

func TestSubtitleSanitize(t *testing.T) {
	cases := map[string]string{
		`<i>miring</i> <b>tebal</b> <u>garis</u>`:                `<i>miring</i> <b>tebal</b> <u>garis</u>`,
		`<I>besar</I> <B >spasi</B > <b/>kosong`:                 `<i>besar</i> <b>spasi</b> <b>kosong`,
		`<font color="#ff0000">merah</font>`:                     `merah`,
		`<script>alert(1)</script>teks`:                          `alert(1)teks`,
		`<img src=x onerror=alert(1)>gambar`:                     `gambar`,
		`<svg/onload=alert(1)><iframe src="https://e"></iframe>`: ``,
		`<a href="javascript:alert(1)">tautan</a>`:               `tautan`,
		`<c.kuning>kelas</c> <v Budi>suara</v>`:                  `kelas suara`,
		`<i onmouseover="alert(1)">atribut</i>`:                  `<i>atribut</i>`,
		"<b\nonclick=x>multi</b>":                                `<b>multi</b>`,
		`<b>"<script>"</b>`:                                      `<b>""</b>`,
		`<<script>script>`:                                       `&lt;script&gt;`,
		`a < b > c`:                                              `a &lt; b &gt; c`,
		`< b> <b </b`:                                            `&lt; b&gt; &lt;b &lt;/b`,
		`AT&T &amp; &lt; &bogus; &#60;`:                          `AT&amp;T &amp; &lt; &amp;bogus; &amp;#60;`,
		`1 &nbsp; &lrm; &rlm; &gt;`:                              `1 &nbsp; &lrm; &rlm; &gt;`,
		`panah --> di tengah`:                                    `panah --&gt; di tengah`,
		`<00:00:01.000>karaoke`:                                  `karaoke`,
		`<!-- komentar --> <![CDATA[x]]>`:                        `&lt;!-- komentar --&gt; &lt;![CDATA[x]]&gt;`,
		`<style>::cue{background:url(x)}</style>`:                `::cue{background:url(x)}`,
		"satu\n\n\ndua   ":                                       "satu\ndua",
		"<i>baris1\nbaris2</i>":                                  "<i>baris1\nbaris2</i>",
		`teks "kutip" 'apostrof' 漢字`:                             `teks "kutip" 'apostrof' 漢字`,
	}
	for in, want := range cases {
		if got := sanitizeCueText(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeVTTDropsBlocksAndSettings(t *testing.T) {
	src := "WEBVTT - judul\nKind: captions\n\nSTYLE\n::cue { background: url(https://evil/x) }\n\n" +
		"REGION\nid:fred width:40%\n\nNOTE komentar <script>\n\n" +
		"intro\n00:01.000 --> 00:04.000 align:start position:10% line:0 region:fred vertical:rl onload=x\n<c.yellow>Halo</c> <v Budi>dunia</v>\n\n" +
		"00:05.000 --> 00:06.000\n<script>alert(1)</script>\n\n"
	got := SanitizeVTT(src)
	want := "WEBVTT\n\n00:00:01.000 --> 00:00:04.000 align:start position:10% line:0 vertical:rl\nHalo dunia\n\n" +
		"00:00:05.000 --> 00:00:06.000\nalert(1)\n\n"
	if got != want {
		t.Fatalf("SanitizeVTT:\n%q\nwant\n%q", got, want)
	}
	for _, bad := range []string{"STYLE", "REGION", "NOTE", "url(", "script", "onload"} {
		if strings.Contains(got, bad) {
			t.Fatalf("%q survived: %q", bad, got)
		}
	}
}

func TestFindSidecars(t *testing.T) {
	names := []string{
		"film.mp4", "film.srt", "film.vtt", "film.en.srt", "film.id.SRT", "film.pt-BR.vtt",
		"film.Komentar Sutradara.srt", "film2.srt", "film.txt", "filmx.srt", "lain.vtt",
		"film.en.vtt", ".srt", "film.srt.bak",
	}
	got := FindSidecars("film.mp4", names)
	want := []Sidecar{
		{Name: "film.vtt", Lang: "", Label: "", Format: "vtt"},
		{Name: "film.en.vtt", Lang: "en", Label: "en", Format: "vtt"},
		{Name: "film.id.SRT", Lang: "id", Label: "id", Format: "srt"},
		{Name: "film.Komentar Sutradara.srt", Lang: "", Label: "Komentar Sutradara", Format: "srt"},
		{Name: "film.pt-BR.vtt", Lang: "pt-BR", Label: "pt-BR", Format: "vtt"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FindSidecars:\n%+v\nwant\n%+v", got, want)
	}
	if FindSidecars("tanpa-ekstensi", names) != nil {
		t.Fatal("video without extension")
	}
	if got := FindSidecars("Klip.MKV", []string{"klip.srt", "Klip.srt"}); len(got) != 1 || got[0].Name != "Klip.srt" {
		t.Fatalf("base name is case-sensitive: %+v", got)
	}
	if !IsSubtitleName("a.SRT") || !IsSubtitleName("a.vtt") || IsSubtitleName("a.txt") {
		t.Fatal("IsSubtitleName")
	}
}
