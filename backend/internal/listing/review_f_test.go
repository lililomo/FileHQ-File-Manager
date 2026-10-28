package listing

import (
	"context"
	"strings"
	"testing"

	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
)

// Review F: staging files of atomic writes, uploads and zip edits must never
// be listed, including on the fast path "hidden files on, no filter" (review
// C m-06; Step 16b rule "even with hidden files on").
func TestReviewFStagingNeverListedEvenWithHiddenOn(t *testing.T) {
	svc, j, _ := newEnv(t, "normal.txt", ".hidden", ".a.txt.fm-tmp-0123", ".fm-upload-9", ".fm-zipedit-task1-arsip.zip", "sub/")
	for _, req := range []ListRequest{
		{Dir: fsjail.Root, Hidden: true},               // fast path (no copy)
		{Dir: fsjail.Root, Hidden: true, Filter: "fm"}, // filtered path
		{Dir: fsjail.Root, Hidden: false},              // dotfiles hidden anyway
		{Dir: fsjail.Root, Hidden: true, Types: []files.Category{"other"}},
	} {
		req.UserID, req.Sort, req.Locale = "u1", DefaultSort, "id"
		res, err := svc.List(context.Background(), j, req)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, it := range res.Items {
			names = append(names, it.Name)
			if isStagingName(it.Name) {
				t.Errorf("hidden=%v filter=%q: staging entry listed: %s", req.Hidden, req.Filter, it.Name)
			}
		}
		if req.Hidden && req.Filter == "" && len(req.Types) == 0 && !strings.Contains(strings.Join(names, ","), ".hidden") {
			t.Errorf("hidden=%v: dotfile .hidden missing from %v", req.Hidden, names)
		}
		if res.Total != len(res.Items) {
			t.Errorf("total %d != items %d", res.Total, len(res.Items))
		}
	}
}
