package files

import "testing"

func TestLookup(t *testing.T) {
	cases := []struct {
		name  string
		ext   string
		mime  string
		cat   Category
		label string
		thumb bool
	}{
		{"photo.JPG", "jpg", "image/jpeg", CatImage, "type.image.jpeg", true},
		{"movie.mkv", "mkv", "video/x-matroska", CatVideo, "type.video.mkv", true},
		{"backup.tar.gz", "tar.gz", "application/gzip", CatArchive, "type.archive.targz", false},
		{"site.tgz", "tgz", "application/gzip", CatArchive, "type.archive.targz", false},
		{"Dockerfile", "", "text/plain", CatCode, "type.code.dockerfile", false},
		{".bashrc", "", "", CatOther, "type.other", false},
		{".env", "", "text/plain", CatCode, "type.code.env", false},
		{"README", "", "text/plain", CatText, "type.text.plain", false},
		{"report.pdf", "pdf", "application/pdf", CatPDF, "type.pdf.pdf", true},
		{"data.CSV", "csv", "text/csv", CatSpreadsheet, "type.spreadsheet.csv", false},
		{"noext", "", "", CatOther, "type.other", false},
		{"trailing.", "", "", CatOther, "type.other", false},
		{"weird.xyzq", "xyzq", "", CatOther, "type.other", false},
	}
	for _, c := range cases {
		got := Lookup(c.name)
		if got.Ext != c.ext || got.Mime != c.mime || got.Category != c.cat || got.Label != c.label || got.Thumb != c.thumb {
			t.Errorf("%s: got %+v, want ext=%s mime=%s cat=%s label=%s thumb=%v", c.name, got, c.ext, c.mime, c.cat, c.label, c.thumb)
		}
	}
	if Folder().Label != "type.folder" || Folder().Category != CatFolder {
		t.Fatal("folder type")
	}
	if _, ok := ParseCategory("image"); !ok {
		t.Fatal("image category")
	}
	if _, ok := ParseCategory("nope"); ok {
		t.Fatal("unknown category accepted")
	}
}
