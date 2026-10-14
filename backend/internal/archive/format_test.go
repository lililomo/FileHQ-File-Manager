package archive

import (
	"bytes"
	"testing"
)

func TestDetect(t *testing.T) {
	zipb := buildZip(t, []zipEntry{{name: "a.txt", content: []byte("a")}})
	tarb := buildTarZeros(t, 10)
	cases := []struct {
		name string
		data []byte
		want Format
	}{
		{"a.zip", zipb, FormatZip},
		{"a.jar", zipb, FormatJar},
		{"renamed.bin", zipb, FormatZip},
		{"a.tar", tarb, FormatTar},
		{"a.tar.gz", gzipBytes(t, tarb), FormatTarGz},
		{"a.tgz", gzipBytes(t, tarb), FormatTarGz},
		{"noext", gzipBytes(t, tarb), FormatTarGz},
		{"plain.gz", gzipBytes(t, []byte("not a tar at all, just text")), FormatGz},
		{"mystery", gzipBytes(t, []byte("not a tar at all, just text")), FormatGz},
		{"x.7z", append([]byte("7z\xBC\xAF\x27\x1C"), make([]byte, 64)...), FormatSevenZip},
		{"x.rar", append([]byte("Rar!\x1A\x07\x01\x00"), make([]byte, 64)...), FormatRar},
	}
	for _, c := range cases {
		got, err := Detect(c.name, bytes.NewReader(c.data), int64(len(c.data)))
		if err != nil || got != c.want {
			t.Errorf("Detect(%s) = %s, %v want %s", c.name, got, err, c.want)
		}
	}
	if _, err := Detect("text.txt", bytes.NewReader([]byte("hello")), 5); err == nil {
		t.Error("plain text detected as archive")
	}
	if _, err := Detect("fake.zip", bytes.NewReader([]byte("hello")), 5); err == nil {
		t.Error("extension must not override the magic")
	}
	for in, want := range map[string]Format{"tgz": FormatTarGz, "TAR.GZ": FormatTarGz, ".zip": FormatZip, "gzip": FormatGz, "zstd": FormatZst} {
		if got, ok := ParseFormat(in); !ok || got != want {
			t.Errorf("ParseFormat(%s) = %s", in, got)
		}
	}
	if Stem("backup.tar.gz") != "backup" || Stem("a.zip") != "a" || Stem("noext") != "noext" {
		t.Error("Stem")
	}
}

func TestCapabilitiesFollowTools(t *testing.T) {
	with := newEnv(t)
	without := newEnv(t, withOptions(func(o *Options) { o.SevenZip = "-" }))
	for _, c := range without.svc.Capabilities().Formats {
		switch c.Format {
		case FormatSevenZip:
			if c.CanWrite || c.PasswordWrite || c.Reason != "dependency_missing" || !c.CanRead {
				t.Fatalf("7z without CLI: %+v", c)
			}
		case FormatIso:
			if c.CanRead {
				t.Fatalf("iso without CLI: %+v", c)
			}
		case FormatRar:
			if c.CanWrite || !c.CanRead || !c.PasswordRead {
				t.Fatalf("rar: %+v", c)
			}
		}
	}
	if with.svc.SevenZipAvailable() {
		c, _ := with.svc.Capability(FormatSevenZip)
		if !c.CanWrite || !c.EncryptNames || !c.SplitVolume {
			t.Fatalf("7z with CLI: %+v", c)
		}
	}
}
