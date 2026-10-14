package httpapi

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
)

// TestTrashQuotaAC walks the HTTP side of PROMPT Step 10a: trash list /
// restore / purge / empty, delete-permanent, dir-size, checksum, /me/quota
// and the IDOR check between two users.
func TestTrashQuotaAC(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := readyAdmin(t, env)
	home := filepath.Join(env.cfg.StorageRoot, "admin")
	ip := "203.0.113.5:1"
	seedFiles(t, home, "a.txt", "dir/", "dir/x.txt", "dir/sub/", "dir/sub/y.txt", "other/")
	ctx := context.Background()
	admin, err := env.store.GetUserByUsername(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	post := func(path string, body any) resp { return env.call(http.MethodPost, "/api/v1"+path, ip, body, hdr, ck) }
	get := func(path string) resp { return env.call(http.MethodGet, "/api/v1"+path, ip, nil, hdr, ck) }

	// quota before anything: dirty, zero
	r := get("/me/quota")
	if r.rec.Code != 200 || r.body["quota"] != float64(admin.QuotaBytes) || r.body["dirty"] != true {
		t.Fatalf("quota: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// delete two items → trash
	r = post("/files/delete", map[string]any{"paths": []string{"/a.txt", "/dir"}})
	if r.rec.Code != 200 {
		t.Fatalf("delete: %d %s", r.rec.Code, r.rec.Body.String())
	}
	results := r.body["results"].([]any)
	idA := results[0].(map[string]any)["trashId"].(string)
	idDir := results[1].(map[string]any)["trashId"].(string)
	r = get("/trash")
	if r.rec.Code != 200 || r.body["total"] != float64(2) || r.body["retentionDays"] != float64(env.cfg.TrashRetentionDays) {
		t.Fatalf("trash list: %d %s", r.rec.Code, r.rec.Body.String())
	}
	items := r.body["items"].([]any)
	if first := items[0].(map[string]any); first["id"] != idDir || first["isDir"] != true || first["originalPath"] != "/dir" || first["kind"] != "folder" {
		t.Fatalf("trash item: %v", first)
	}
	if r := get("/trash?kind=bogus"); r.rec.Code != 400 {
		t.Fatalf("bad kind: %d", r.rec.Code)
	}
	if r := get("/trash?kind=folder&sort=name&dir=asc&limit=1"); r.rec.Code != 200 || r.body["total"] != float64(1) {
		t.Fatalf("filtered list: %d %s", r.rec.Code, r.rec.Body.String())
	}
	usedAfterDelete := get("/me/quota").body
	if usedAfterDelete["trash"] != float64(len("a.txt")+len("dir/x.txt")+len("dir/sub/y.txt")) {
		t.Fatalf("trash bytes: %v", usedAfterDelete)
	}

	// restore a.txt to the original folder after it has been re-created → conflict → rename
	seedFiles(t, home, "a.txt")
	r = post("/trash/restore", map[string]any{"ids": []string{idA}})
	if r.rec.Code != 409 || r.code(t) != "ALREADY_EXISTS" {
		t.Fatalf("restore conflict: %d %s", r.rec.Code, r.rec.Body.String())
	}
	conflicts := details(r)["conflicts"].([]any)
	if c := conflicts[0].(map[string]any); c["id"] != idA || c["path"] != "/a.txt" || c["existing"] == nil {
		t.Fatalf("conflict details: %v", c)
	}
	r = post("/trash/restore", map[string]any{"ids": []string{idA}, "perItem": map[string]string{idA: "rename"}})
	if r.rec.Code != 200 {
		t.Fatalf("restore rename: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if rr := r.body["results"].([]any)[0].(map[string]any); rr["status"] != "renamed" || rr["to"] != "/a (1).txt" {
		t.Fatalf("restore result: %v", rr)
	}
	if listNames(t, env, ck, hdr, "/") != "other,a (1).txt,a.txt" {
		t.Fatalf("listing after restore: %s", listNames(t, env, ck, hdr, "/"))
	}
	// restore the folder into another folder (parent exists); original parent gone case: remove /other first
	r = post("/trash/restore", map[string]any{"ids": []string{idDir}, "to": "/other/deep"})
	if r.rec.Code != 404 {
		t.Fatalf("restore into missing folder: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = post("/trash/restore", map[string]any{"ids": []string{idDir}, "to": "/other"})
	if r.rec.Code != 200 || r.body["results"].([]any)[0].(map[string]any)["to"] != "/other/dir" {
		t.Fatalf("restore to: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(home, "other", "dir", "sub", "y.txt")); err != nil {
		t.Fatal("folder content not restored")
	}
	if r := get("/trash"); r.body["total"] != float64(0) {
		t.Fatalf("trash not empty after restores: %s", r.rec.Body.String())
	}

	// second user: IDOR on restore/purge → 404 / gone
	ckB, hdrB := env.newUserWith(t, "budi", "Password budi yang panjang 2026", false)
	postB := func(path string, body any) resp {
		return env.call(http.MethodPost, "/api/v1"+path, "203.0.113.9:1", body, hdrB, ckB)
	}
	r = post("/files/delete", map[string]any{"paths": []string{"/a (1).txt"}})
	idA1 := r.body["results"].([]any)[0].(map[string]any)["trashId"].(string)
	if r := postB("/trash/restore", map[string]any{"ids": []string{idA1}}); r.rec.Code != 404 || r.code(t) != "NOT_FOUND" {
		t.Fatalf("IDOR restore: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = postB("/trash/purge", map[string]any{"ids": []string{idA1}})
	if r.rec.Code != 200 || r.body["deleted"] != float64(0) || r.body["results"].([]any)[0].(map[string]any)["status"] != "gone" {
		t.Fatalf("IDOR purge: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(env.cfg.DataDir, "trash", admin.ID, idA1)); err != nil {
		t.Fatal("user B purged user A's item")
	}
	if r := env.call(http.MethodGet, "/api/v1/trash", "203.0.113.9:1", nil, hdrB, ckB); r.body["total"] != float64(0) {
		t.Fatalf("user B sees A's trash: %s", r.rec.Body.String())
	}
	// purge by owner
	r = post("/trash/purge", map[string]any{"ids": []string{idA1}})
	if r.rec.Code != 200 || r.body["deleted"] != float64(1) || r.body["bytesFreed"] != float64(len("a.txt")) {
		t.Fatalf("purge: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(env.cfg.DataDir, "trash", admin.ID, idA1)); err == nil {
		t.Fatal("purged payload still exists")
	}
	if n, _ := env.store.CountAuditSince(ctx, "file.delete_permanent", "2000-01-01T00:00:00.000Z"); n != 1 {
		t.Fatalf("audit rows: %d", n)
	}
	// empty requires HAPUS
	post("/files/delete", map[string]any{"paths": []string{"/a.txt"}})
	if r := post("/trash/empty", map[string]any{}); r.rec.Code != 400 {
		t.Fatalf("empty without word: %d", r.rec.Code)
	}
	if r := post("/trash/empty", map[string]any{"confirm": "HAPUS"}); r.rec.Code != 200 || r.body["deleted"] != float64(1) {
		t.Fatalf("empty: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if q := get("/me/quota").body; q["trash"] != float64(0) {
		t.Fatalf("trash bytes after empty: %v", q)
	}

	// delete-permanent: > 50 needs HAPUS; audit
	many := make([]string, 0, 51)
	for i := 0; i < 51; i++ {
		name := "m" + two(i) + ".txt"
		seedFiles(t, home, name)
		many = append(many, "/"+name)
	}
	if r := post("/files/delete-permanent", map[string]any{"paths": many}); r.rec.Code != 400 || details(r)["word"] != "HAPUS" {
		t.Fatalf("permanent without word: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = post("/files/delete-permanent", map[string]any{"paths": many, "confirm": "HAPUS"})
	if r.rec.Code != 200 || r.body["deleted"] != float64(51) || r.body["undo"] != nil {
		t.Fatalf("permanent: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(home, "m00.txt")); err == nil {
		t.Fatal("permanent delete left files")
	}
	if n, _ := env.store.CountAuditSince(ctx, "file.delete_permanent", "2000-01-01T00:00:00.000Z"); n != 2 {
		t.Fatalf("audit rows after permanent: %d", n)
	}

	// dir-size and checksum
	r = post("/files/dir-size", map[string]any{"path": "/other"})
	if r.rec.Code != 200 || r.body["files"] != float64(2) || r.body["dirs"] != float64(2) || r.body["partial"] != false {
		t.Fatalf("dir-size: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r := post("/files/dir-size", map[string]any{"path": "/other/dir/x.txt"}); r.rec.Code != 400 {
		t.Fatalf("dir-size on file: %d", r.rec.Code)
	}
	r = post("/files/checksum", map[string]any{"path": "/other/dir/x.txt", "algos": []string{"sha256", "md5"}, "verify": map[string]string{"algo": "md5", "expected": "x"}})
	if r.rec.Code != 200 || r.body["sha256"] == nil || r.body["md5"] == nil || r.body["sha1"] != nil || r.body["verified"] != false {
		t.Fatalf("checksum: %d %s", r.rec.Code, r.rec.Body.String())
	}
	md5 := r.body["md5"].(string)
	r = post("/files/checksum/verify", map[string]any{"path": "/other/dir/x.txt", "algo": "md5", "expected": strings.ToUpper(md5)})
	if r.rec.Code != 200 || r.body["match"] != true {
		t.Fatalf("verify: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r := get("/files/stat?path=/other/dir/x.txt"); r.body["checksums"] == nil {
		t.Fatalf("stat without cached checksums: %s", r.rec.Body.String())
	}
	big := filepath.Join(home, "big.bin")
	f, _ := os.Create(big)
	_ = f.Truncate(200 << 20)
	_ = f.Close()
	if r := post("/files/checksum", map[string]any{"path": "/big.bin"}); r.rec.Code != 413 || r.code(t) != "FILE_TOO_LARGE" {
		t.Fatalf("big checksum: %d %s", r.rec.Code, r.rec.Body.String())
	}

	// quota enforcement over HTTP: 1 MB limit, copy 2 MB → 413 QUOTA_EXCEEDED
	f, _ = os.Create(filepath.Join(home, "two.bin"))
	_ = f.Truncate(2 << 20)
	_ = f.Close()
	if err := env.store.UpdateUserQuota(ctx, updateQuotaParams(admin.ID, 1<<20)); err != nil {
		t.Fatal(err)
	}
	r = post("/files/copy", map[string]any{"paths": []string{"/two.bin"}, "to": "/other"})
	if r.rec.Code != 413 || r.code(t) != "QUOTA_EXCEEDED" || details(r)["reason"] != "quota" || details(r)["needed"] != float64(2<<20) {
		t.Fatalf("copy over quota: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(home, "other", "two.bin")); err == nil {
		t.Fatal("partial file after quota refusal")
	}
	// /auth/me carries the same quota object
	if r := get("/auth/me"); r.rec.Code != 200 || r.body["user"].(map[string]any)["quota"].(map[string]any)["limit"] != float64(1<<20) {
		t.Fatalf("auth/me quota: %s", r.rec.Body.String())
	}
}

func updateQuotaParams(id string, n int64) sqlc.UpdateUserQuotaParams {
	return sqlc.UpdateUserQuotaParams{QuotaBytes: n, UpdatedAt: db.Now(), ID: id}
}

func two(i int) string { return string(rune('0'+i/10)) + string(rune('0'+i%10)) }
