package files

import (
	"context"
	"strings"
	"testing"

	"kppbwi.app/filemanager/internal/fsjail"
)

func nodeByName(n *AnalysisNode, name string) *AnalysisNode {
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func nodeByKind(n *AnalysisNode, kind string) *AnalysisNode {
	for _, c := range n.Children {
		if c.Kind == kind {
			return c
		}
	}
	return nil
}

// TestAnalyze checks the treemap aggregation, depth limit, merge of small
// folders, largest files and the type summary against a known tree, and that
// the total equals the quota walk (SPEC §8.2 accounting).
func TestAnalyze(t *testing.T) {
	e := newEnv(t, envOpts{})
	ctx := context.Background()
	big := strings.Repeat("x", 10_000)
	e.write("foto/a.jpg", big)        // 10000
	e.write("foto/b.png", big[:5000]) // 5000
	e.write("video/v.mp4", big+big)   // 20000
	e.write("dok/laporan.pdf", big[:3000])
	e.write("dok/sub/deep/deeper/x.txt", big[:2000]) // depth 4 rolls into dok/sub/deep
	e.write("arsip/z.zip", big[:1000])
	e.write("tiny/t.txt", "a") // < 0.5 % of total → merged into "other"
	e.write("root.txt", big[:4000])
	e.mkdir("kosong")
	for i := range 60 {
		e.write("many/f"+strings.Repeat("0", 2-len(itoa(i)))+itoa(i)+".txt", big[:100+i])
	}

	res, err := e.svc.Analyze(ctx, e.j, e.uid, false)
	if err != nil {
		t.Fatal(err)
	}
	st, err := e.j.DirSize(ctx, fsjail.Root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != st.Bytes || res.Files != st.Files || res.Dirs != st.Dirs {
		t.Fatalf("totals %d/%d/%d != quota walk %d/%d/%d", res.Total, res.Files, res.Dirs, st.Bytes, st.Files, st.Dirs)
	}
	if res.Partial || res.Cached {
		t.Fatalf("flags: %+v", res)
	}
	root := res.Tree
	if root.Path != "/" || root.Size != res.Total || root.Kind != "dir" {
		t.Fatalf("root node: %+v", root)
	}
	// children sizes sum to the parent size (files leaf carries direct files)
	var sum int64
	for _, c := range root.Children {
		sum += c.Size
	}
	if sum != root.Size {
		t.Fatalf("children sum %d != root %d", sum, root.Size)
	}
	if f := nodeByKind(root, "files"); f == nil || f.Size != 4000 || f.Files != 1 {
		t.Fatalf("root files leaf: %+v", f)
	}
	if o := nodeByKind(root, "other"); o == nil || o.Size != 1 || o.Dirs != 1 {
		t.Fatalf("merged other: %+v", o)
	}
	if nodeByName(root, "tiny") != nil || nodeByName(root, "kosong") != nil {
		t.Fatal("tiny/empty folders must be merged or dropped")
	}
	// sorted by size desc
	if root.Children[0].Name != "video" || root.Children[1].Name != "foto" {
		t.Fatalf("order: %s, %s", root.Children[0].Name, root.Children[1].Name)
	}
	// depth limit: /dok/sub/deep is depth 3 → no children, but size includes deeper files
	dok := nodeByName(root, "dok")
	sub := nodeByName(dok, "sub")
	deep := nodeByName(sub, "deep")
	if deep == nil || deep.Size != 2000 || deep.Children != nil || deep.Files != 1 || deep.Dirs != 1 {
		t.Fatalf("depth-3 node: %+v", deep)
	}
	if dok.Size != 5000 || dok.Files != 2 || dok.Dirs != 3 {
		t.Fatalf("dok: %+v", dok)
	}
	// top files: 50 largest, sorted desc
	if len(res.TopFiles) != AnalysisTopFiles {
		t.Fatalf("top files: %d", len(res.TopFiles))
	}
	if res.TopFiles[0].Path != "/video/v.mp4" || res.TopFiles[1].Path != "/foto/a.jpg" {
		t.Fatalf("top order: %+v", res.TopFiles[:2])
	}
	for i := 1; i < len(res.TopFiles); i++ {
		if res.TopFiles[i].Size > res.TopFiles[i-1].Size {
			t.Fatalf("top not sorted at %d", i)
		}
	}
	if res.TopFiles[len(res.TopFiles)-1].Size < 100+13 {
		t.Fatalf("smallest kept file too small: %+v", res.TopFiles[len(res.TopFiles)-1])
	}
	// by type
	if res.ByType["image"].Bytes != 15000 || res.ByType["image"].Files != 2 || res.ByType["video"].Bytes != 20000 ||
		res.ByType["archive"].Bytes != 1000 || res.ByType["document"].Files != 64 {
		t.Fatalf("byType: image=%+v video=%+v archive=%+v document=%+v", res.ByType["image"], res.ByType["video"], res.ByType["archive"], res.ByType["document"])
	}
	// cache hit until a mutation
	again, _ := e.svc.Analyze(ctx, e.j, e.uid, false)
	if !again.Cached {
		t.Fatal("second call not cached")
	}
	e.write("new.txt", "zz")
	e.svc.changed(ctx, e.j, e.uid, "/new.txt", false)
	third, _ := e.svc.Analyze(ctx, e.j, e.uid, false)
	if third.Cached || third.Total != res.Total+2 {
		t.Fatalf("after change: cached=%v total=%d", third.Cached, third.Total)
	}
	forced, _ := e.svc.Analyze(ctx, e.j, e.uid, true)
	if forced.Cached {
		t.Fatal("force must recompute")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestTypeClass(t *testing.T) {
	cases := map[Category]string{CatImage: "image", CatVideo: "video", CatAudio: "audio", CatArchive: "archive",
		CatPDF: "document", CatCode: "document", CatSpreadsheet: "document", CatText: "document", CatFont: "other", CatOther: "other"}
	for c, want := range cases {
		if got := TypeClass(c); got != want {
			t.Errorf("%s → %s, want %s", c, got, want)
		}
	}
}
