package archive

import (
	"path/filepath"
	"testing"
)

// TestLimits covers PROMPT Step 14a AC 2/3: entry count and the byte limit
// measured while streaming (a 1 MB bomb is stopped at the limit + one
// chunk, tmp cleaned, destination untouched).
func TestLimits(t *testing.T) {
	e := newEnv(t, withOptions(func(o *Options) { o.MaxEntries = 100; o.MaxExtractSize = 4 << 20 }))
	e.write(t, "in/many.zip", manyEntriesZip(t, 101))
	e.write(t, "in/few.zip", manyEntriesZip(t, 100))
	e.write(t, "in/bomb.zip", bombZip(t, 64<<20)) // 64 MiB of zeros ≈ 64 KB compressed
	tk := e.extract(t, ExtractPayload{Archive: "/in/many.zip", Dest: "/many"}, "")
	failedWith(t, tk, CodeLimit)
	if tk.Error.Message == "" || e.exists("many") {
		t.Fatalf("many entries: %+v", tk.Error)
	}
	resultOf[ExtractResult](t, e.extract(t, ExtractPayload{Archive: "/in/few.zip", Dest: "/few"}, ""))
	// the header already tells the truth: refused before any byte is written
	tk = e.extract(t, ExtractPayload{Archive: "/in/bomb.zip", Dest: "/bomb"}, "")
	failedWith(t, tk, CodeLimit)
	if e.exists("bomb") || len(e.tmpDirs(t)) != 0 {
		t.Fatal("bomb: destination or tmp left behind")
	}
	// a lying header (tar.gz has none): stopped while streaming at ≤ limit + 1 chunk
	writeTarGzZeros(t, filepath.Join(e.home, "in", "bomb.tar.gz"), 64<<20)
	tk = e.extract(t, ExtractPayload{Archive: "/in/bomb.tar.gz", Dest: "/bomb2"}, "")
	failedWith(t, tk, CodeLimit)
	if e.exists("bomb2") || len(e.tmpDirs(t)) != 0 {
		t.Fatal("stream bomb: destination or tmp left behind")
	}
	if size, ok := tk.Error.Message, true; !ok || size == "" {
		t.Fatal("message")
	}
	// quota is part of the budget (SPEC §8.11 step 3): 1 MB quota refuses a
	// 2 MB entry as ARCHIVE_LIMIT with details.reason=quota
	e2 := newEnv(t)
	e2.setQuota(t, 1<<20)
	e2.write(t, "in/two.zip", buildZip(t, []zipEntry{{name: "two.bin", content: make([]byte, 2<<20)}}))
	tk = e2.extract(t, ExtractPayload{Archive: "/in/two.zip", Dest: "/two"}, "")
	failedWith(t, tk, CodeLimit)
	if e2.exists("two") {
		t.Fatal("quota: destination created")
	}
	// ratio check: 300:1 beyond RatioMinBytes is unsafe even under the size limit
	e3 := newEnv(t, withOptions(func(o *Options) { o.RatioMinBytes = 1 << 20; o.MaxExtractSize = 1 << 30 }))
	e3.write(t, "in/ratio.zip", bombZip(t, 32<<20))
	tk = e3.extract(t, ExtractPayload{Archive: "/in/ratio.zip", Dest: "/ratio"}, "")
	failedWith(t, tk, CodeUnsafe)
}
