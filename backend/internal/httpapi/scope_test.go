package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kppbwi.app/filemanager/internal/db/sqlc"
)

// Step 18b: share scope (?share=<id>) — the access matrix of SPEC §8.15,
// subtree confinement, owner bookkeeping, signed URLs, uploads, suggestions.

type scopeActor struct {
	name string
	ck   *http.Cookie
	hdr  map[string]string
}

// shareURL appends ?share=<id> to a target.
func shareURL(target, shareID string) string {
	if shareID == "" {
		return target
	}
	sep := "?"
	if strings.Contains(target, "?") {
		sep = "&"
	}
	return target + sep + "share=" + shareID
}

func createUsersShare(t *testing.T, env *authEnv, ck *http.Cookie, hdr map[string]string, path string, recipients ...map[string]any) (string, map[string]any) {
	t.Helper()
	r := env.call(http.MethodPost, "/api/v1/shares", "203.0.113.5:1", map[string]any{"path": path, "mode": "users", "recipients": recipients}, hdr, ck)
	if r.rec.Code != 201 {
		t.Fatalf("create users share: %d %s", r.rec.Code, r.rec.Body.String())
	}
	sh := shareOf(t, r)
	id, _ := sh["id"].(string)
	return id, sh
}

func TestShareScopeMatrixAndConfinement(t *testing.T) {
	env := newAuthEnv(t)
	ack, ahdr, home := seedShareFixture(t, env)
	ctx := context.Background()
	// a file symlink and a dir symlink that leave the shared folder (inside the owner's root)
	if err := os.Symlink("../rahasia.txt", filepath.Join(home, "laporan", "link-out.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("..", filepath.Join(home, "laporan", "dir-out")); err != nil {
		t.Fatal(err)
	}
	bck, bhdr := env.newUser(t, "budi", "Rahasia sekali 2026!")
	cck, chdr := env.newUser(t, "citra", "Rahasia sekali 2026!")
	dck, dhdr := env.newUser(t, "dedi", "Rahasia sekali 2026!")
	owner := scopeActor{"owner", ack, ahdr}
	viewer := scopeActor{"viewer", bck, bhdr}
	editor := scopeActor{"editor", cck, chdr}
	outsider := scopeActor{"outsider", dck, dhdr}
	anon := scopeActor{"anonymous", nil, map[string]string{}}

	// recipients: budi viewer, citra editor; the view lists them; budi is notified
	id, sh := createUsersShare(t, env, ack, ahdr, "/laporan", map[string]any{"username": "budi"}, map[string]any{"username": "citra", "permission": "editor"})
	if recs, _ := sh["recipients"].([]any); len(recs) != 2 || sh["url"] != "" || sh["mode"] != "users" {
		t.Fatalf("share view: %s", sh)
	}
	r := env.call(http.MethodGet, "/api/v1/notifications", "203.0.113.9:1", nil, nil, bck)
	if r.rec.Code != 200 || !strings.Contains(r.rec.Body.String(), "share_received") || !strings.Contains(r.rec.Body.String(), "/shared-with-me") {
		t.Fatalf("budi notification: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// shared with me
	r = env.call(http.MethodGet, "/api/v1/shared-with-me", "203.0.113.9:1", nil, nil, bck)
	if r.rec.Code != 200 || !strings.Contains(r.rec.Body.String(), `"name":"laporan"`) || !strings.Contains(r.rec.Body.String(), `"permission":"viewer"`) || strings.Contains(r.rec.Body.String(), "/laporan") {
		t.Fatalf("shared-with-me budi: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/shared-with-me", "203.0.113.9:1", nil, nil, dck)
	if r.rec.Code != 200 || r.rec.Body.String() != "{\"groups\":[]}\n" {
		t.Fatalf("shared-with-me dedi: %d %s", r.rec.Code, r.rec.Body.String())
	}

	// --- access matrix ------------------------------------------------------
	type op struct {
		name   string
		method string
		target string
		body   any
	}
	ops := []op{
		{"list", http.MethodGet, "/api/v1/files/list?path=/", nil},
		{"stat", http.MethodGet, "/api/v1/files/stat?path=/ringkasan.txt", nil},
		{"content-get", http.MethodGet, "/api/v1/files/content?path=/ringkasan.txt", nil},
		{"sign-download", http.MethodPost, "/api/v1/files/sign", map[string]any{"path": "/ringkasan.txt", "purpose": "download"}},
		{"versions", http.MethodGet, "/api/v1/versions?path=/ringkasan.txt", nil},
		{"media-info", http.MethodGet, "/api/v1/files/media-info?path=/ringkasan.txt", nil},
		{"zip", http.MethodPost, "/api/v1/files/zip", map[string]any{"paths": []string{"/2026"}}},
		{"mkdir", http.MethodPost, "/api/v1/files/mkdir", map[string]any{"path": "/", "name": "dir-%s"}},
		{"create", http.MethodPost, "/api/v1/files/create", map[string]any{"path": "/", "name": "new-%s.txt"}},
		{"content-put", http.MethodPut, "/api/v1/files/content", map[string]any{"path": "/put-%s.txt", "content": "x", "create": true}},
		{"rename", http.MethodPost, "/api/v1/files/rename", map[string]any{"path": "/2026/q1.txt", "newName": "q1-%s.txt"}},
		{"copy", http.MethodPost, "/api/v1/files/copy", map[string]any{"paths": []string{"/ringkasan.txt"}, "to": "/2026", "onConflict": "rename"}},
		{"precheck", http.MethodPost, "/api/v1/upload/precheck", map[string]any{"path": "/", "files": []map[string]any{{"name": "u.txt", "size": 1}}}},
		{"delete", http.MethodPost, "/api/v1/files/delete", map[string]any{"paths": []string{"/new-%s.txt"}}},
		{"chmod", http.MethodPost, "/api/v1/files/chmod", map[string]any{"paths": []string{"/ringkasan.txt"}, "mode": "0644"}},
		{"delete-permanent", http.MethodPost, "/api/v1/files/delete-permanent", map[string]any{"paths": []string{"/ringkasan.txt"}}},
		{"share-create", http.MethodPost, "/api/v1/shares", map[string]any{"path": "/2026"}},
	}
	readOps := map[string]bool{"list": true, "stat": true, "content-get": true, "sign-download": true, "versions": true, "media-info": true, "zip": true}
	writeOps := map[string]bool{"mkdir": true, "create": true, "content-put": true, "rename": true, "copy": true, "precheck": true, "delete": true}
	ok := func(code int) bool { return code == 200 || code == 201 }
	for _, actor := range []scopeActor{owner, viewer, editor, outsider, anon} {
		for _, o := range ops {
			body := o.body
			if m, isMap := body.(map[string]any); isMap {
				cp := map[string]any{}
				for k, v := range m {
					if s, isStr := v.(string); isStr && strings.Contains(s, "%s") {
						v = fmt.Sprintf(s, actor.name)
					}
					if arr, isArr := v.([]string); isArr {
						out := make([]string, len(arr))
						for i, s := range arr {
							out[i] = strings.ReplaceAll(s, "%s", actor.name)
						}
						v = out
					}
					cp[k] = v
				}
				body = cp
			}
			target := shareURL(o.target, id)
			r := env.call(o.method, target, "203.0.113.9:1", body, actor.hdr, actor.ck)
			code := r.rec.Code
			var want string
			switch {
			case actor.name == "anonymous":
				want = "401"
			case !readOps[o.name] && !writeOps[o.name]:
				// chmod, delete-permanent, share-create: owner-only endpoints refuse the scope outright
				want = "400"
			case actor.name == "outsider":
				want = "404"
			case actor.name == "owner":
				// the owner is not a recipient of their own share: the share scope is 404 for them too
				want = "404"
			case readOps[o.name]:
				want = "ok"
			case writeOps[o.name] && actor.name == "editor":
				want = "ok"
			case writeOps[o.name]:
				want = "403"
			default:
				want = "400"
			}
			got := fmt.Sprint(code)
			if ok(code) {
				got = "ok"
			}
			if want == "ok" && !ok(code) || want != "ok" && got != want {
				t.Errorf("%s %s: got %d want %s: %s", actor.name, o.name, code, want, r.rec.Body.String())
			}
			if actor.name != "owner" && strings.Contains(r.rec.Body.String(), "/laporan") {
				t.Errorf("%s %s leaks the owner path: %s", actor.name, o.name, r.rec.Body.String())
			}
			if strings.Contains(r.rec.Body.String(), home) {
				t.Errorf("%s %s leaks the host path", actor.name, o.name)
			}
		}
	}
	// the owner's own scope sees the editor's work in the owner's folder
	if _, err := os.Stat(filepath.Join(home, "laporan", "dir-editor")); err != nil {
		t.Fatalf("editor mkdir did not land in the owner's folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "laporan", "2026", "q1-editor.txt")); err != nil {
		t.Fatalf("editor rename did not happen: %v", err)
	}
	// write attempts by the viewer changed nothing
	if _, err := os.Stat(filepath.Join(home, "laporan", "dir-viewer")); err == nil {
		t.Fatal("viewer created a folder")
	}

	// --- confinement: nothing above the share, symlinks out are dead ends ---
	for _, target := range []string{
		"/api/v1/files/list?path=../",
		"/api/v1/files/list?path=/dir-out",
		"/api/v1/files/list?path=/dir-out/laporan",
		"/api/v1/files/content?path=/link-out.txt",
		"/api/v1/files/content?path=/dir-out/rahasia.txt",
		"/api/v1/files/stat?path=/../rahasia.txt",
		"/api/v1/files/media-info?path=/link-out.txt",
	} {
		r := env.call(http.MethodGet, shareURL(target, id), "203.0.113.9:1", nil, bhdr, bck)
		if r.rec.Code == 200 && !strings.Contains(target, "stat") {
			t.Errorf("%s: 200 %s", target, r.rec.Body.String())
		}
		if strings.Contains(r.rec.Body.String(), "di luar share") || strings.Contains(r.rec.Body.String(), "rahasia.txt\"") && strings.Contains(target, "list") {
			t.Errorf("%s leaks content outside the share: %s", target, r.rec.Body.String())
		}
	}
	// a signed URL for the escaping symlink is refused when used
	r = env.call(http.MethodPost, shareURL("/api/v1/files/sign", id), "203.0.113.9:1", map[string]any{"path": "/link-out.txt", "purpose": "media"}, bhdr, bck)
	if r.rec.Code == 200 {
		u, _ := r.body["url"].(string)
		if rr := env.call(http.MethodGet, u, "203.0.113.9:1", nil, nil, nil); rr.rec.Code == 200 {
			t.Fatalf("escaping symlink served through a signed url: %s", rr.rec.Body.String())
		}
	}
	// copy/move cannot target outside: the destination is cleaned inside the sub-root
	r = env.call(http.MethodPost, shareURL("/api/v1/files/copy", id), "203.0.113.9:1", map[string]any{"paths": []string{"/ringkasan.txt"}, "to": "/dir-out", "onConflict": "rename"}, chdr, cck)
	if r.rec.Code == 200 {
		if _, err := os.Stat(filepath.Join(home, "ringkasan (1).txt")); err == nil {
			t.Fatal("copy escaped through a dir symlink")
		}
	}
	if _, err := os.Stat(filepath.Join(home, "ringkasan.txt")); err == nil {
		t.Fatal("copy escaped to the owner's root")
	}

	// --- signed URLs are bound to the recipient and re-checked --------------
	r = env.call(http.MethodPost, shareURL("/api/v1/files/sign", id), "203.0.113.9:1", map[string]any{"path": "/2026/q1-editor.txt", "purpose": "download"}, bhdr, bck)
	if r.rec.Code != 200 {
		t.Fatalf("sign: %d %s", r.rec.Code, r.rec.Body.String())
	}
	dlURL, _ := r.body["url"].(string)
	if !strings.Contains(dlURL, "scope=r%3A"+id+"%3A") {
		t.Fatalf("recipient scope missing: %s", dlURL)
	}
	if rr := env.call(http.MethodGet, dlURL, "203.0.113.9:1", nil, nil, nil); rr.rec.Code != 200 || rr.rec.Body.String() != "kuartal satu" {
		t.Fatalf("download via recipient url: %d %s", rr.rec.Code, rr.rec.Body.String())
	}
	// zip streams the sub-root
	r = env.call(http.MethodPost, shareURL("/api/v1/files/zip", id), "203.0.113.9:1", map[string]any{"paths": []string{"/2026"}}, bhdr, bck)
	if r.rec.Code != 200 {
		t.Fatalf("zip: %d %s", r.rec.Code, r.rec.Body.String())
	}
	zipURL, _ := r.body["url"].(string)
	if rr := env.call(http.MethodGet, zipURL, "203.0.113.9:1", nil, nil, nil); rr.rec.Code != 200 || rr.rec.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("zip get: %d %s", rr.rec.Code, rr.rec.Header().Get("Content-Type"))
	}
	// the access journal names the recipient
	r = env.call(http.MethodGet, "/api/v1/shares/"+id+"/access-logs", "203.0.113.5:1", nil, nil, ack)
	if r.rec.Code != 200 || !strings.Contains(r.rec.Body.String(), `"action":"download"`) || !strings.Contains(r.rec.Body.String(), `"action":"list"`) {
		t.Fatalf("access logs: %d %s", r.rec.Code, r.rec.Body.String())
	}

	// --- editor: save creates a version keyed by the owner's path; delete goes to the owner's bin
	r = env.call(http.MethodPut, shareURL("/api/v1/files/content", id), "203.0.113.9:1", map[string]any{"path": "/ringkasan.txt", "content": "isi baru"}, mergeHdr(chdr, map[string]string{"If-Match": "*"}), cck)
	if r.rec.Code != 200 {
		t.Fatalf("editor save: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, shareURL("/api/v1/versions?path=/ringkasan.txt", id), "203.0.113.9:1", nil, bhdr, bck)
	items, _ := r.body["items"].([]any)
	if r.rec.Code != 200 || len(items) != 1 {
		t.Fatalf("versions in scope: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/versions?path=/laporan/ringkasan.txt", "203.0.113.5:1", nil, nil, ack)
	if items, _ := r.body["items"].([]any); r.rec.Code != 200 || len(items) != 1 {
		t.Fatalf("owner sees the version by absolute path: %d %s", r.rec.Code, r.rec.Body.String())
	}
	vid, _ := items[0].(map[string]any)["id"].(string)
	// a version of a file outside the share is 404 for the recipient; the owner's own version id inside it is readable
	if rr := env.call(http.MethodGet, shareURL("/api/v1/versions/"+vid+"/content", id), "203.0.113.9:1", nil, bhdr, bck); rr.rec.Code != 200 {
		t.Fatalf("version content in scope: %d %s", rr.rec.Code, rr.rec.Body.String())
	}
	r = env.call(http.MethodPut, "/api/v1/files/content", "203.0.113.5:1", map[string]any{"path": "/rahasia.txt", "content": "v2"}, mergeHdr(ahdr, map[string]string{"If-Match": "*"}), ack)
	if r.rec.Code != 200 {
		t.Fatalf("owner save outside: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/versions?path=/rahasia.txt", "203.0.113.5:1", nil, nil, ack)
	outsideItems, _ := r.body["items"].([]any)
	outsideID, _ := outsideItems[0].(map[string]any)["id"].(string)
	if rr := env.call(http.MethodGet, shareURL("/api/v1/versions/"+outsideID+"/content", id), "203.0.113.9:1", nil, bhdr, bck); rr.rec.Code != 404 {
		t.Fatalf("version outside the share readable: %d %s", rr.rec.Code, rr.rec.Body.String())
	}
	// delete by the editor → owner's trash with deleted_by
	r = env.call(http.MethodPost, shareURL("/api/v1/files/delete", id), "203.0.113.9:1", map[string]any{"paths": []string{"/ringkasan.txt"}}, chdr, cck)
	if r.rec.Code != 200 || r.body["undoToken"] != nil {
		t.Fatalf("editor delete: %d %s", r.rec.Code, r.rec.Body.String())
	}
	citra, _ := env.store.GetUserByUsername(ctx, "citra")
	admin, _ := env.store.GetUserByUsername(ctx, "admin")
	rows, err := env.store.ListTrashItems(ctx, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	var trashed *sqlcTrashItem
	for i := range rows {
		if rows[i].OriginalPath == "/laporan/ringkasan.txt" {
			trashed = &rows[i]
		}
	}
	if trashed == nil || trashed.DeletedBy == nil || *trashed.DeletedBy != citra.ID {
		t.Fatalf("trash row: %+v", rows)
	}
	if n, _ := env.store.ListTrashItems(ctx, citra.ID); len(n) != 0 {
		t.Fatal("the recipient's own bin received the item")
	}
	// the owner can restore it from their bin
	r = env.call(http.MethodPost, "/api/v1/trash/restore", "203.0.113.5:1", map[string]any{"ids": []string{trashed.ID}}, ahdr, ack)
	if r.rec.Code != 200 {
		t.Fatalf("owner restore: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if b, err := os.ReadFile(filepath.Join(home, "laporan", "ringkasan.txt")); err != nil || string(b) != "isi baru" {
		t.Fatalf("restored content: %q %v", b, err)
	}
	// recipient activity recorded with share-relative paths
	r = env.call(http.MethodGet, "/api/v1/me/activity", "203.0.113.9:1", nil, nil, cck)
	if r.rec.Code != 200 || !strings.Contains(r.rec.Body.String(), `"path":"/ringkasan.txt"`) || strings.Contains(r.rec.Body.String(), "/laporan") {
		t.Fatalf("recipient activity: %d %s", r.rec.Code, r.rec.Body.String())
	}

	// --- trash endpoints refuse the scope (review B m-05) ---------------------
	if rr := env.call(http.MethodGet, shareURL("/api/v1/trash", id), "203.0.113.9:1", nil, bhdr, bck); rr.rec.Code != 400 {
		t.Fatalf("trash with scope: %d", rr.rec.Code)
	}

	// --- owner renames the shared folder: recipients keep access -------------
	r = env.call(http.MethodPost, "/api/v1/files/rename", "203.0.113.5:1", map[string]any{"path": "/laporan", "newName": "laporan-2026"}, ahdr, ack)
	if r.rec.Code != 200 {
		t.Fatalf("owner rename: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, shareURL("/api/v1/files/list?path=/", id), "203.0.113.9:1", nil, bhdr, bck)
	if r.rec.Code != 200 || !strings.Contains(r.rec.Body.String(), "ringkasan.txt") || strings.Contains(r.rec.Body.String(), "laporan-2026") {
		t.Fatalf("list after rename: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// the earlier signed URL still works (path inside the share is unchanged)
	if rr := env.call(http.MethodGet, dlURL, "203.0.113.9:1", nil, nil, nil); rr.rec.Code != 200 {
		t.Fatalf("download after rename: %d", rr.rec.Code)
	}

	// --- owner removes budi: 404 everywhere, the signed URL dies, budi is told
	r = env.call(http.MethodPatch, "/api/v1/shares/"+id, "203.0.113.5:1", map[string]any{"recipients": []map[string]any{{"username": "citra", "permission": "editor"}}}, ahdr, ack)
	if r.rec.Code != 200 {
		t.Fatalf("patch recipients: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if rr := env.call(http.MethodGet, shareURL("/api/v1/files/list?path=/", id), "203.0.113.9:1", nil, bhdr, bck); rr.rec.Code != 404 {
		t.Fatalf("revoked list: %d", rr.rec.Code)
	}
	if rr := env.call(http.MethodGet, dlURL, "203.0.113.9:1", nil, nil, nil); rr.rec.Code != 404 {
		t.Fatalf("revoked signed url: %d", rr.rec.Code)
	}
	r = env.call(http.MethodGet, "/api/v1/notifications", "203.0.113.9:1", nil, nil, bck)
	if !strings.Contains(r.rec.Body.String(), "share_revoked") {
		t.Fatalf("budi revoked notification: %s", r.rec.Body.String())
	}
	// citra leaves; the owner's view drops her
	if rr := env.call(http.MethodDelete, "/api/v1/shared-with-me/"+id, "203.0.113.9:1", nil, chdr, cck); rr.rec.Code != 204 {
		t.Fatalf("leave: %d %s", rr.rec.Code, rr.rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/shares/"+id, "203.0.113.5:1", nil, nil, ack)
	if recs, _ := shareOf(t, r)["recipients"].([]any); len(recs) != 0 {
		t.Fatalf("recipients after leave: %s", r.rec.Body.String())
	}
	_ = dhdr
}

type sqlcTrashItem = sqlc.TrashItem

func mergeHdr(a, b map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func TestShareScopeFileShareUploadAndSuggest(t *testing.T) {
	env := newAuthEnv(t)
	ack, ahdr, home := seedShareFixture(t, env)
	ctx := context.Background()
	bck, bhdr := env.newUser(t, "budi", "Rahasia sekali 2026!")
	cck, chdr := env.newUser(t, "citra", "Rahasia sekali 2026!")

	// single-file share: path "/" (or the name) is the file; siblings and listing are unreachable
	fid, _ := createUsersShare(t, env, ack, ahdr, "/laporan/ringkasan.txt", map[string]any{"username": "budi"})
	r := env.call(http.MethodGet, shareURL("/api/v1/files/stat?path=/", fid), "203.0.113.9:1", nil, bhdr, bck)
	if r.rec.Code != 200 || r.body["name"] != "ringkasan.txt" || strings.Contains(r.rec.Body.String(), "/laporan") {
		t.Fatalf("file share stat: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, shareURL("/api/v1/files/content?path=/", fid), "203.0.113.9:1", nil, bhdr, bck)
	if r.rec.Code != 200 || r.body["content"] != "isi ringkasan" {
		t.Fatalf("file share content: %d %s", r.rec.Code, r.rec.Body.String())
	}
	for _, target := range []string{"/api/v1/files/stat?path=/2026/q1.txt", "/api/v1/files/content?path=/2026/q1.txt", "/api/v1/files/stat?path=/2026"} {
		if rr := env.call(http.MethodGet, shareURL(target, fid), "203.0.113.9:1", nil, bhdr, bck); rr.rec.Code != 404 {
			t.Fatalf("%s: %d %s", target, rr.rec.Code, rr.rec.Body.String())
		}
	}
	if rr := env.call(http.MethodGet, shareURL("/api/v1/files/list?path=/", fid), "203.0.113.9:1", nil, bhdr, bck); rr.rec.Code != 400 {
		t.Fatalf("file share list: %d", rr.rec.Code)
	}
	r = env.call(http.MethodPost, shareURL("/api/v1/files/sign", fid), "203.0.113.9:1", map[string]any{"paths": []string{"/", "/2026/q1.txt"}, "purpose": "media"}, bhdr, bck)
	urls, _ := r.body["urls"].([]any)
	invalid, _ := r.body["invalid"].([]any)
	if r.rec.Code != 200 || len(urls) != 1 || len(invalid) != 1 {
		t.Fatalf("file share sign: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if u, _ := urls[0].(map[string]any)["url"].(string); u != "" {
		if rr := env.call(http.MethodGet, u, "203.0.113.9:1", nil, nil, nil); rr.rec.Code != 200 || rr.rec.Body.String() != "isi ringkasan" {
			t.Fatalf("file share raw: %d %s", rr.rec.Code, rr.rec.Body.String())
		}
	}

	// folder share with an editor: upload through /upload/share/{id}/tus
	id, _ := createUsersShare(t, env, ack, ahdr, "/laporan", map[string]any{"username": "citra", "permission": "editor"}, map[string]any{"username": "budi"})
	admin, _ := env.store.GetUserByUsername(ctx, "admin")
	before, _ := env.quota.Usage(ctx, admin.ID)
	tusPost := func(actor scopeActor, filename, content string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/upload/share/"+id+"/tus/", nil)
		req.RemoteAddr = "203.0.113.9:1"
		req.Header.Set("Tus-Resumable", "1.0.0")
		req.Header.Set("Upload-Length", itoa(len(content)))
		req.Header.Set("Upload-Metadata", tusMeta(map[string]string{"filename": filename, "path": "/2026", "onConflict": "overwrite"}))
		for k, v := range actor.hdr {
			req.Header.Set(k, v)
		}
		if actor.ck != nil {
			req.AddCookie(actor.ck)
		}
		rec := httptest.NewRecorder()
		env.handler.ServeHTTP(rec, req)
		return rec
	}
	if rec := tusPost(scopeActor{"viewer", bck, bhdr}, "x.txt", "no"); rec.Code != 403 {
		t.Fatalf("viewer upload: %d %s", rec.Code, rec.Body.String())
	}
	if rec := tusPost(scopeActor{"owner", ack, ahdr}, "x.txt", "no"); rec.Code != 404 {
		t.Fatalf("owner through the share route: %d %s", rec.Code, rec.Body.String())
	}
	rec := tusPost(scopeActor{"editor", cck, chdr}, "baru.txt", "hello")
	if rec.Code != 201 {
		t.Fatalf("editor tus create: %d %s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "/api/v1/upload/share/"+id+"/tus/") {
		t.Fatalf("Location must stay on the share base: %q", loc)
	}
	path := loc[strings.Index(loc, "/api/v1/"):]
	// budi (viewer) cannot touch citra's upload; dedi's id lookups are 404
	hreq := httptest.NewRequest(http.MethodHead, path, nil)
	hreq.RemoteAddr = "203.0.113.9:1"
	hreq.Header.Set("Tus-Resumable", "1.0.0")
	hreq.AddCookie(bck)
	hrec := httptest.NewRecorder()
	env.handler.ServeHTTP(hrec, hreq)
	if hrec.Code != 403 {
		t.Fatalf("viewer HEAD on the editor's upload: %d", hrec.Code)
	}
	preq := httptest.NewRequest(http.MethodPatch, path, bytes.NewReader([]byte("hello")))
	preq.RemoteAddr = "203.0.113.9:1"
	preq.Header.Set("Tus-Resumable", "1.0.0")
	preq.Header.Set("Upload-Offset", "0")
	preq.Header.Set("Content-Type", "application/offset+octet-stream")
	for k, v := range chdr {
		preq.Header.Set(k, v)
	}
	preq.AddCookie(cck)
	prec := httptest.NewRecorder()
	env.handler.ServeHTTP(prec, preq)
	if prec.Code != 204 || prec.Header().Get("Upload-Result") != "created" {
		t.Fatalf("editor patch: %d %s %v", prec.Code, prec.Body.String(), prec.Header())
	}
	if b, err := os.ReadFile(filepath.Join(home, "laporan", "2026", "baru.txt")); err != nil || string(b) != "hello" {
		t.Fatalf("upload did not land in the owner's folder: %v", err)
	}
	after, _ := env.quota.Usage(ctx, admin.ID)
	if after.Used-before.Used != 5 {
		t.Fatalf("owner quota delta: %d", after.Used-before.Used)
	}
	citra, _ := env.store.GetUserByUsername(ctx, "citra")
	if cu, _ := env.quota.Usage(ctx, citra.ID); cu.Used != 0 {
		t.Fatalf("recipient quota charged: %d", cu.Used)
	}
	r = env.call(http.MethodGet, "/api/v1/notifications", "203.0.113.5:1", nil, nil, ack)
	if !strings.Contains(r.rec.Body.String(), "share_upload") || !strings.Contains(r.rec.Body.String(), "citra mengunggah") {
		t.Fatalf("owner upload notification: %s", r.rec.Body.String())
	}
	// the owner sees the recipient's precheck path relative to the share
	r = env.call(http.MethodPost, "/api/v1/upload/share/"+id+"/precheck", "203.0.113.9:1", map[string]any{"path": "/2026", "files": []map[string]any{{"name": "baru.txt", "size": 5}, {"name": "lain.txt", "size": 1}}}, chdr, cck)
	if r.rec.Code != 200 || !strings.Contains(r.rec.Body.String(), `"conflict"`) {
		t.Fatalf("share precheck: %d %s", r.rec.Code, r.rec.Body.String())
	}

	// user-suggest: ≥ 2 chars, never self, username + displayName only
	if rr := env.call(http.MethodGet, "/api/v1/shares/user-suggest?q=c", "203.0.113.9:1", nil, nil, bck); rr.rec.Code != 400 {
		t.Fatalf("suggest short: %d", rr.rec.Code)
	}
	r = env.call(http.MethodGet, "/api/v1/users/suggest?q=ci", "203.0.113.9:1", nil, nil, bck)
	var sug struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(r.rec.Body.Bytes(), &sug)
	if r.rec.Code != 200 || len(sug.Items) != 1 || sug.Items[0]["username"] != "citra" || len(sug.Items[0]) != 2 {
		t.Fatalf("suggest: %d %s", r.rec.Code, r.rec.Body.String())
	}
	for i := 0; i < 30; i++ {
		env.call(http.MethodGet, "/api/v1/users/suggest?q=ci", "203.0.113.9:1", nil, nil, bck)
	}
	if rr := env.call(http.MethodGet, "/api/v1/users/suggest?q=ci", "203.0.113.9:1", nil, nil, bck); rr.rec.Code != 429 || rr.rec.Header().Get("Retry-After") == "" {
		t.Fatalf("suggest rate limit: %d %v", rr.rec.Code, rr.rec.Header())
	}
}
