package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/exec"
	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/tasks"
)

// Step 23d: the transcoder against stub ffmpeg/ffprobe/nice scripts (never
// the real ffmpeg): exact argv, nice through the runner, output layout,
// progress parsing, concurrency cap, refusals, cancel/failure cleanup,
// sweeper, disk budget, playlist rewriting.

// stubFFmpeg emulates ffmpeg's HLS muxer: it records its argv and pid,
// writes N segments (temp file + rename, like -hls_flags temp_file) and an
// EVENT playlist, prints -progress blocks and ends the playlist. Marker
// files in its directory change the behaviour: segments (count), delay
// (seconds between segments), fail (exit 1 with a path on stderr).
const stubFFmpeg = `#!/bin/sh
LOG=@LOG@
seg=""; out=""; prev=""
for a in "$@"; do
  if [ "$prev" = "-hls_segment_filename" ]; then seg="$a"; fi
  prev="$a"; out="$a"
done
dir=$(dirname "$out"); id=$(basename "$dir")
for a in "$@"; do echo "$a"; done > "$LOG/$id.argv"
echo $$ > "$LOG/$id.pid"
mkdir -p "$LOG/running"; touch "$LOG/running/$id"
ls "$LOG/running" | wc -l >> "$LOG/concurrency.log"
n=3; [ -f "$LOG/segments" ] && n=$(cat "$LOG/segments")
delay=0; [ -f "$LOG/delay" ] && delay=$(cat "$LOG/delay")
if [ -f "$LOG/fail" ]; then rm -f "$LOG/running/$id"; echo "Invalid data found when processing input $dir/x" >&2; exit 1; fi
printf '#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:EVENT\n' > "$dir/pl"
i=0
while [ $i -lt $n ]; do
  f=$(printf "$dir/seg_%05d.ts" $i)
  printf 'segment %d\n' $i > "$f.tmp"; mv "$f.tmp" "$f"
  printf '#EXTINF:4.000000,\n%s\n' "$(basename "$f")" >> "$dir/pl"
  cp "$dir/pl" "$out.tmp"; mv "$out.tmp" "$out"
  i=$((i+1))
  printf 'frame=%d\nout_time_us=%d\nprogress=continue\n' $i $((i*4000000))
  [ "$delay" != "0" ] && sleep "$delay"
done
printf '#EXT-X-ENDLIST\n' >> "$dir/pl"; cp "$dir/pl" "$out.tmp"; mv "$out.tmp" "$out"; rm -f "$dir/pl"
printf 'out_time_us=N/A\nprogress=end\n'
rm -f "$LOG/running/$id"
`

// stubFFprobe prints probe.json of its directory.
const stubFFprobe = `#!/bin/sh
cat @LOG@/probe.json
`

// stubNice records the nice level and execs the rest (nice -n L cmd...).
const stubNice = `#!/bin/sh
echo "$1 $2" >> @LOG@/nice.log
shift 2
exec "$@"
`

const probeHEVC = `{"streams":[{"codec_type":"video","codec_name":"hevc","width":1920,"height":1080},{"codec_type":"audio","codec_name":"aac"}],"format":{"duration":"12.0"}}`

type tcEnv struct {
	t       *testing.T
	log     string
	storage string
	data    string
	store   *db.Store
	engine  *tasks.Engine
	tc      *Transcoder
	jails   map[string]*fsjail.Jail
	users   map[string]string // username → id
	set     atomic.Pointer[TranscodeSettings]
	clock   atomic.Pointer[time.Time]
	runner  *exec.Runner
}

func writeStub(t *testing.T, path, body, log string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(body, "@LOG@", log)), 0o755); err != nil { //nolint:gosec // test stub must be executable
		t.Fatal(err)
	}
}

func newTCEnv(t *testing.T, opts ...func(*TranscoderOptions)) *tcEnv {
	t.Helper()
	root := t.TempDir()
	e := &tcEnv{t: t, log: filepath.Join(root, "stub"), storage: filepath.Join(root, "storage"), jails: map[string]*fsjail.Jail{}, users: map[string]string{}}
	e.data = filepath.Join(e.storage, ".system")
	for _, d := range []string{e.log, filepath.Join(e.data, "db"), filepath.Join(e.data, "tmp")} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	writeStub(t, filepath.Join(e.log, "ffmpeg"), stubFFmpeg, e.log)
	writeStub(t, filepath.Join(e.log, "ffprobe"), stubFFprobe, e.log)
	writeStub(t, filepath.Join(e.log, "nice"), stubNice, e.log)
	e.writeMarker("probe.json", probeHEVC)
	database, err := db.Open(filepath.Join(e.data, "db", "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.MigrateUp(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.store = db.NewStore(database)
	now := db.Now()
	for _, name := range []string{"budi", "sari"} {
		u, err := e.store.CreateUser(context.Background(), sqlc.CreateUserParams{ID: db.NewID(), Username: name, DisplayName: name, Role: "user", Status: "active",
			PasswordHash: "x", QuotaBytes: 1 << 30, CreatedAt: now, UpdatedAt: now})
		if err != nil {
			t.Fatal(err)
		}
		e.users[name] = u.ID
		if err := os.MkdirAll(filepath.Join(e.storage, name), 0o750); err != nil {
			t.Fatal(err)
		}
		j, err := fsjail.OpenUser(e.storage, name, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = j.Close() })
		e.jails[name] = j
	}
	e.set.Store(&TranscodeSettings{Enabled: true, MaxConcurrent: 1, MaxHeight: 720, MaxDuration: 3 * time.Hour, Nice: 19})
	start := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	e.clock.Store(&start)
	// the stub nice wrapper proves the level travels through internal/exec
	e.runner = &exec.Runner{TmpDir: filepath.Join(e.data, "tmp"), Path: "/usr/bin:/bin", NicePath: filepath.Join(e.log, "nice")}
	zero := time.Duration(0)
	e.engine = tasks.New(tasks.Options{Repo: e.store, MaxPerUser: 3, MaxGlobal: 4, NotifyMinDuration: &zero, ErrorCode: files.TaskErrorCode,
		OpenJail: func(_ context.Context, userID string) (*fsjail.Jail, func(), error) {
			for name, id := range e.users {
				if id == userID {
					j, err := fsjail.OpenUser(e.storage, name, id)
					return j, func() { _ = j.Close() }, err
				}
			}
			return nil, nil, errors.New("no user")
		}})
	o := TranscoderOptions{Runner: e.runner, Prober: &Prober{Runner: e.runner, FFprobe: filepath.Join(e.log, "ffprobe")}, FFmpeg: filepath.Join(e.log, "ffmpeg"),
		DataDir: e.data, Tasks: e.store, Threads: 2, MinDiskFree: 1,
		Settings: func() TranscodeSettings { return *e.set.Load() },
		Now:      func() time.Time { return *e.clock.Load() }}
	for _, fn := range opts {
		fn(&o)
	}
	e.tc = NewTranscoder(o)
	e.tc.Register(e.engine)
	ctx, cancel := context.WithCancel(context.Background())
	if err := e.engine.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); e.engine.Shutdown(2 * time.Second) })
	return e
}

func (e *tcEnv) writeMarker(name, content string) {
	e.t.Helper()
	if err := os.WriteFile(filepath.Join(e.log, name), []byte(content), 0o640); err != nil {
		e.t.Fatal(err)
	}
}

func (e *tcEnv) settings(fn func(*TranscodeSettings)) {
	s := *e.set.Load()
	fn(&s)
	e.set.Store(&s)
}

func (e *tcEnv) advance(d time.Duration) {
	n := e.clock.Load().Add(d)
	e.clock.Store(&n)
}

// video writes a source file and enqueues it after Check.
func (e *tcEnv) enqueue(user, name string) tasks.Task {
	e.t.Helper()
	j := e.jails[user]
	if err := os.WriteFile(filepath.Join(e.storage, user, name), []byte("not really "+name), 0o640); err != nil {
		e.t.Fatal(err)
	}
	info, err := j.Stat(fsjail.VPath("/" + name))
	if err != nil {
		e.t.Fatal(err)
	}
	p, err := e.tc.Check(context.Background(), j, info)
	if err != nil {
		e.t.Fatalf("check %s: %v", name, err)
	}
	task, err := e.engine.Enqueue(context.Background(), tasks.EnqueueRequest{UserID: e.users[user], Type: TaskTranscodeHLS, Title: "Konversi " + name, Payload: p})
	if err != nil {
		e.t.Fatal(err)
	}
	return task
}

func (e *tcEnv) wait(user, id string, cond func(tasks.Task) bool, what string) tasks.Task {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		x, err := e.engine.Get(context.Background(), e.users[user], id)
		if err == nil && cond(x) {
			return x
		}
		time.Sleep(10 * time.Millisecond)
	}
	x, _ := e.engine.Get(context.Background(), e.users[user], id)
	e.t.Fatalf("timeout waiting for %s: %+v", what, x)
	return tasks.Task{}
}

func final(x tasks.Task) bool { return x.State.Final() }

func (e *tcEnv) hlsDir(id string) string { return filepath.Join(e.data, "tmp", "hls", id) }

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func TestHLSArgsExact(t *testing.T) {
	got := HLSArgs("/srv/u/a b.mkv", "/data/tmp/hls/x", 720, 2)
	want := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-progress", "pipe:1", "-nostats",
		"-i", "/srv/u/a b.mkv", "-map", "0:v:0", "-map", "0:a:0?",
		"-vf", "scale=w=-2:h='trunc(min(ih,720)/2)*2'",
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-profile:v", "main", "-pix_fmt", "yuv420p",
		"-force_key_frames", "expr:gte(t,n_forced*4)",
		"-c:a", "aac", "-b:a", "128k", "-ac", "2", "-sn", "-dn", "-threads", "2",
		"-f", "hls", "-hls_time", "4", "-hls_playlist_type", "event", "-hls_flags", "temp_file",
		"-hls_segment_filename", "/data/tmp/hls/x/seg_%05d.ts", "/data/tmp/hls/x/playlist.m3u8"}
	if !slices.Equal(got, want) {
		t.Fatalf("argv:\n got %q\nwant %q", got, want)
	}
	for res, h := range map[string]int{"480p": 480, "720p": 720, "1080p": 1080, "": 720, "4k": 720} {
		if ResolutionHeight(res) != h {
			t.Errorf("ResolutionHeight(%q)", res)
		}
		if a := HLSArgs("/i", "/o", ResolutionHeight(res), 1); a[15] != fmt.Sprintf("scale=w=-2:h='trunc(min(ih,%d)/2)*2'", h) {
			t.Errorf("%s: %s", res, a[15])
		}
	}
	if w, h := ScaledSize(1920, 1080, 720); w != 1280 || h != 720 {
		t.Errorf("scaled 1080p: %dx%d", w, h)
	}
	if w, h := ScaledSize(161, 91, 720); w != 160 || h != 90 {
		t.Errorf("scaled odd: %dx%d", w, h)
	}
	if TranscodeTimeout(10_000) != 100*time.Second || TranscodeTimeout(int64(2*time.Hour/time.Millisecond)) != 3*time.Hour {
		t.Errorf("timeout: %s %s", TranscodeTimeout(10_000), TranscodeTimeout(int64(2*time.Hour/time.Millisecond)))
	}
}

func TestProgressWriterParsesBlocks(t *testing.T) {
	var got []int64
	w := &progressWriter{onBlock: func(us int64) { got = append(got, us) }}
	stream := "frame=1\nout_time_us=N/A\nprogress=continue\nframe=2\nout_time_us=4000000\nspeed=2.1x\nprogress=continue\nout_time_us=8100000\nprogress=end\n"
	for i := 0; i < len(stream); i += 7 { // split mid-line
		_, _ = w.Write([]byte(stream[i:min(i+7, len(stream))]))
	}
	if !slices.Equal(got, []int64{0, 4_000_000, 8_100_000}) {
		t.Fatalf("blocks: %v", got)
	}
	_, _ = w.Write([]byte(strings.Repeat("x", 5000)))
	if len(w.buf) != 0 {
		t.Fatal("runaway line kept")
	}
}

// TestTranscodeRunWithStub: one conversion end to end — argv exactly as
// designed (720p cap, threads), nice 19 through the runner, folder layout
// DATA_DIR/tmp/hls/<task>/, progress in media seconds, result, owner-only
// serving.
func TestTranscodeRunWithStub(t *testing.T) {
	e := newTCEnv(t)
	task := e.enqueue("budi", "film.mkv")
	if task.Progress.Unit != tasks.UnitItems {
		t.Fatalf("unit %s", task.Progress.Unit)
	}
	x := e.wait("budi", task.ID, final, "done")
	if x.State != tasks.Done {
		t.Fatalf("state %s %+v", x.State, x.Error)
	}
	dir := e.hlsDir(task.ID)
	in, _ := e.jails["budi"].AbsForCLI("/film.mkv")
	raw, err := os.ReadFile(filepath.Join(e.log, task.ID+".argv"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n"), HLSArgs(in, dir, 720, 2); !slices.Equal(got, want) {
		t.Fatalf("ffmpeg argv:\n got %q\nwant %q", got, want)
	}
	// ffprobe (enqueue check + start re-check) keeps the runner default 10,
	// ffmpeg gets transcode.nice = 19
	if nice, _ := os.ReadFile(filepath.Join(e.log, "nice.log")); string(nice) != "-n 10\n-n 10\n-n 19\n" {
		t.Fatalf("nice: %q", nice)
	}
	names, _ := os.ReadDir(dir)
	var listed []string
	for _, n := range names {
		listed = append(listed, n.Name())
	}
	if !slices.Equal(listed, []string{"playlist.m3u8", "seg_00000.ts", "seg_00001.ts", "seg_00002.ts"}) {
		t.Fatalf("layout: %v", listed)
	}
	if x.Progress.Done != 12 || x.Progress.Total != 12 {
		t.Fatalf("progress: %+v", x.Progress)
	}
	var res TranscodeResult
	if err := json.Unmarshal(x.Result, &res); err != nil {
		t.Fatal(err)
	}
	if res.Dir != task.ID || res.Playlist != "/files/hls/"+task.ID+"/playlist.m3u8" || res.Segments != 3 || res.Duration != 12 || res.Width != 1280 || res.Height != 720 {
		t.Fatalf("result: %+v", res)
	}
	if !e.tc.Ready(task.ID) {
		t.Fatal("not ready")
	}
	info, _ := e.jails["budi"].Stat("/film.mkv")
	if id, ok := e.tc.FindDone(e.users["budi"], "/film.mkv", info.Size, info.ModTime.UnixNano()); !ok || id != task.ID {
		t.Fatal("FindDone")
	}
	if _, ok := e.tc.FindDone(e.users["budi"], "/film.mkv", info.Size+1, info.ModTime.UnixNano()); ok {
		t.Fatal("FindDone matched another version")
	}
	f, fi, done, err := e.tc.Open(e.users["budi"], task.ID, PlaylistName)
	if err != nil || !done || fi.Size() == 0 {
		t.Fatalf("open playlist: %v %v", err, done)
	}
	_ = f.Close()
	if f, _, _, err := e.tc.Open(e.users["budi"], task.ID, "seg_00001.ts"); err != nil {
		t.Fatal(err)
	} else {
		_ = f.Close()
	}
	for _, c := range []struct{ user, id, name string }{
		{e.users["sari"], task.ID, PlaylistName},       // another user
		{e.users["budi"], task.ID, "seg_00009.ts"},     // missing segment
		{e.users["budi"], task.ID, "../seg_00000.ts"},  // traversal
		{e.users["budi"], task.ID, "seg_00000.ts.tmp"}, // not a segment name
		{e.users["budi"], task.ID, ""},                 // directory itself
		{e.users["budi"], "0123456789abcdef", PlaylistName},
	} {
		if _, _, _, err := e.tc.Open(c.user, c.id, c.name); !errors.Is(err, ErrHLSNotFound) {
			t.Errorf("open %+v: %v", c, err)
		}
	}
	logs, _ := e.engine.Logs(context.Background(), e.users["budi"], task.ID, 0)
	if len(logs) == 0 || !strings.Contains(logs[0].Message, "nice 19") {
		t.Fatalf("logs: %+v", logs)
	}
}

// TestTranscodeConcurrencyCap: two users, transcode.max_concurrent = 1 →
// the second task waits queued and starts after the first; both finish and
// ffmpeg never ran twice at once.
func TestTranscodeConcurrencyCap(t *testing.T) {
	e := newTCEnv(t)
	e.writeMarker("delay", "0.2")
	a := e.enqueue("budi", "a.mkv")
	b := e.enqueue("sari", "b.mkv")
	e.wait("budi", a.ID, func(x tasks.Task) bool { return x.State == tasks.Running }, "a running")
	time.Sleep(150 * time.Millisecond)
	if x, _ := e.engine.Get(context.Background(), e.users["sari"], b.ID); x.State != tasks.Queued {
		t.Fatalf("b: %s (want queued behind the cap)", x.State)
	}
	if e.engine.Running() != 1 {
		t.Fatalf("running %d", e.engine.Running())
	}
	for _, c := range []struct{ u, id string }{{"budi", a.ID}, {"sari", b.ID}} {
		if x := e.wait(c.u, c.id, final, "done "+c.id); x.State != tasks.Done {
			t.Fatalf("%s: %s %+v", c.id, x.State, x.Error)
		}
	}
	conc, _ := os.ReadFile(filepath.Join(e.log, "concurrency.log"))
	if strings.Fields(string(conc))[0] != "1" || strings.Fields(string(conc))[1] != "1" {
		t.Fatalf("concurrent ffmpeg runs: %q", conc)
	}
	// with the cap at 2 both run together
	e.settings(func(s *TranscodeSettings) { s.MaxConcurrent = 2 })
	c := e.enqueue("budi", "c.mkv")
	d := e.enqueue("sari", "d.mkv")
	e.wait("budi", c.ID, func(x tasks.Task) bool { return x.State == tasks.Running }, "c running")
	e.wait("sari", d.ID, func(x tasks.Task) bool { return x.State == tasks.Running || x.State.Final() }, "d running")
	e.wait("budi", c.ID, final, "c done")
	e.wait("sari", d.ID, final, "d done")
}

// TestTranscodeCheckRefusals: disabled, too long, unknown duration, not a
// video, folder — at enqueue time (the HTTP layer maps the reasons).
func TestTranscodeCheckRefusals(t *testing.T) {
	e := newTCEnv(t)
	j := e.jails["budi"]
	_ = os.WriteFile(filepath.Join(e.storage, "budi", "v.mkv"), []byte("x"), 0o640)
	info, _ := j.Stat("/v.mkv")
	reason := func() string {
		_, err := e.tc.Check(context.Background(), j, info)
		var te *TranscodeError
		if errors.As(err, &te) {
			return te.Reason
		}
		if err != nil {
			return err.Error()
		}
		return ""
	}
	if r := reason(); r != "" {
		t.Fatalf("ok case: %s", r)
	}
	e.settings(func(s *TranscodeSettings) { s.Enabled = false })
	if r := reason(); r != ReasonDisabled {
		t.Fatalf("disabled: %s", r)
	}
	e.settings(func(s *TranscodeSettings) { s.Enabled = true; s.MaxDuration = 10 * time.Second })
	_, err := e.tc.Check(context.Background(), j, info)
	var te *TranscodeError
	if !errors.As(err, &te) || te.Reason != ReasonTooLong || te.DurationMs != 12000 {
		t.Fatalf("too long: %v", err)
	}
	if code, _ := te.TaskCode(); code != "VALIDATION_FAILED" {
		t.Fatalf("code %s", code)
	}
	e.settings(func(s *TranscodeSettings) { s.MaxDuration = 0 }) // clamps to 3 h
	e.writeMarker("probe.json", `{"streams":[{"codec_type":"video","codec_name":"hevc","width":10,"height":10}],"format":{"duration":"10801"}}`)
	if r := reason(); r != ReasonTooLong {
		t.Fatalf("3 h cap: %s", r)
	}
	e.writeMarker("probe.json", `{"streams":[{"codec_type":"video","codec_name":"hevc","width":10,"height":10}],"format":{}}`)
	if r := reason(); r != ReasonUnknownDuration {
		t.Fatalf("unknown duration: %s", r)
	}
	e.writeMarker("probe.json", `{"streams":[{"codec_type":"audio","codec_name":"mp3"}],"format":{"duration":"10"}}`)
	if r := reason(); r != ReasonNotVideo {
		t.Fatalf("audio: %s", r)
	}
	_ = os.Mkdir(filepath.Join(e.storage, "budi", "dir.mkv"), 0o750)
	dinfo, _ := j.Stat("/dir.mkv")
	if _, err := e.tc.Check(context.Background(), j, dinfo); !errors.As(err, &te) || te.Reason != ReasonNotVideo {
		t.Fatalf("folder: %v", err)
	}
}

// TestTranscodeCancelKillsAndRemoves: cancelling a running conversion kills
// ffmpeg (process group) and removes its folder.
func TestTranscodeCancelKillsAndRemoves(t *testing.T) {
	e := newTCEnv(t)
	e.writeMarker("delay", "5")
	e.writeMarker("segments", "50")
	task := e.enqueue("budi", "long.mkv")
	dir := e.hlsDir(task.ID)
	deadline := time.Now().Add(5 * time.Second)
	for !exists(filepath.Join(dir, PlaylistName)) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !exists(filepath.Join(dir, "seg_00000.ts")) {
		t.Fatal("first segment never written")
	}
	pidRaw, _ := os.ReadFile(filepath.Join(e.log, task.ID+".pid"))
	pid, _ := strconv.Atoi(strings.TrimSpace(string(pidRaw)))
	if pid <= 0 || syscall.Kill(pid, 0) != nil {
		t.Fatalf("ffmpeg stub not running: %q", pidRaw)
	}
	if _, err := e.engine.Cancel(context.Background(), e.users["budi"], task.ID); err != nil {
		t.Fatal(err)
	}
	if x := e.wait("budi", task.ID, final, "cancelled"); x.State != tasks.Cancelled {
		t.Fatalf("state %s", x.State)
	}
	if exists(dir) {
		t.Fatal("folder kept after cancel")
	}
	time.Sleep(50 * time.Millisecond)
	if syscall.Kill(pid, 0) == nil {
		t.Fatal("ffmpeg still alive after cancel")
	}
	if _, _, _, err := e.tc.Open(e.users["budi"], task.ID, PlaylistName); !errors.Is(err, ErrHLSNotFound) {
		t.Fatalf("open after cancel: %v", err)
	}
}

// TestTranscodeFailureAndDisable: ffmpeg failing → failed, folder removed,
// stderr redacted in the task log; switching transcoding off stops a
// running conversion.
func TestTranscodeFailureAndDisable(t *testing.T) {
	e := newTCEnv(t)
	e.writeMarker("fail", "1")
	task := e.enqueue("budi", "bad.mkv")
	x := e.wait("budi", task.ID, final, "failed")
	if x.State != tasks.Failed || x.Error == nil || x.Error.Code != "INTERNAL" {
		t.Fatalf("failed: %s %+v", x.State, x.Error)
	}
	if exists(e.hlsDir(task.ID)) {
		t.Fatal("folder kept after failure")
	}
	logs, _ := e.engine.Logs(context.Background(), e.users["budi"], task.ID, 0)
	joined := ""
	for _, l := range logs {
		joined += l.Message + "\n"
	}
	if !strings.Contains(joined, "ffmpeg failed (status 1)") || strings.Contains(joined, e.data) || !strings.Contains(joined, "<hls>") {
		t.Fatalf("logs: %s", joined)
	}
	_ = os.Remove(filepath.Join(e.log, "fail"))
	e.writeMarker("delay", "0.3")
	e.writeMarker("segments", "40")
	task = e.enqueue("budi", "stop.mkv")
	e.wait("budi", task.ID, func(x tasks.Task) bool { return x.State == tasks.Running && x.Progress.Done > 0 }, "progress")
	e.advance(2 * time.Second) // the runner re-reads the setting at most once per second
	e.settings(func(s *TranscodeSettings) { s.Enabled = false })
	x = e.wait("budi", task.ID, final, "stopped")
	if x.State != tasks.Failed || x.Error == nil || x.Error.Code != "FORBIDDEN" || x.Error.Message != ReasonDisabled {
		t.Fatalf("disabled mid-run: %s %+v", x.State, x.Error)
	}
	if exists(e.hlsDir(task.ID)) {
		t.Fatal("folder kept after disable")
	}
	// a queued/retried task re-checks the setting when it starts
	e.settings(func(s *TranscodeSettings) { s.Enabled = true })
	j := e.jails["budi"]
	info, _ := j.Stat("/stop.mkv")
	p, _ := e.tc.Check(context.Background(), j, info)
	e.settings(func(s *TranscodeSettings) { s.Enabled = false })
	q, _ := e.engine.Enqueue(context.Background(), tasks.EnqueueRequest{UserID: e.users["budi"], Type: TaskTranscodeHLS, Payload: p})
	if x := e.wait("budi", q.ID, final, "refused"); x.State != tasks.Failed || x.Error.Code != "FORBIDDEN" {
		t.Fatalf("queued while disabled: %+v", x.Error)
	}
}

// TestTranscodeSourceChanged: the file changed between enqueue and start.
func TestTranscodeSourceChanged(t *testing.T) {
	e := newTCEnv(t)
	j := e.jails["budi"]
	_ = os.WriteFile(filepath.Join(e.storage, "budi", "v.mkv"), []byte("x"), 0o640)
	info, _ := j.Stat("/v.mkv")
	p, err := e.tc.Check(context.Background(), j, info)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(e.storage, "budi", "v.mkv"), []byte("longer now"), 0o640)
	q, _ := e.engine.Enqueue(context.Background(), tasks.EnqueueRequest{UserID: e.users["budi"], Type: TaskTranscodeHLS, Payload: p})
	if x := e.wait("budi", q.ID, final, "refused"); x.State != tasks.Failed || x.Error.Message != ReasonSourceChanged {
		t.Fatalf("source changed: %+v", x.Error)
	}
}

// TestHLSSweep: finished folders are removed 1 h after the task finished
// and after the last access, recent ones stay; folders of unknown, failed
// or deleted tasks and stray names go at once; disabling transcoding
// removes the rest; after a restart a finished folder is adopted with its
// mtime as last access.
func TestHLSSweep(t *testing.T) {
	e := newTCEnv(t)
	ctx := context.Background()
	old := e.enqueue("budi", "old.mkv")
	e.wait("budi", old.ID, final, "old done")
	fresh := e.enqueue("sari", "fresh.mkv")
	e.wait("sari", fresh.ID, final, "fresh done")
	hls := filepath.Join(e.data, "tmp", "hls")
	stray := filepath.Join(hls, "not-a-task")
	unknown := filepath.Join(hls, db.NewID())
	for _, d := range []string{stray, unknown} {
		_ = os.MkdirAll(d, 0o750)
	}
	if n, err := e.tc.Sweep(ctx); err != nil || n != 2 || exists(stray) || exists(unknown) {
		t.Fatalf("first sweep: %d %v", n, err)
	}
	// 50 min later: the old one was played 40 min ago, nothing is idle yet
	e.advance(10 * time.Minute)
	if f, _, _, err := e.tc.Open(e.users["budi"], old.ID, "seg_00000.ts"); err == nil {
		_ = f.Close()
	}
	e.advance(40 * time.Minute)
	if n, _ := e.tc.Sweep(ctx); n != 0 {
		t.Fatalf("nothing idle yet, removed %d", n)
	}
	// 61 min after finishing: fresh (never played) goes, old (played 51 min ago) stays
	e.advance(11 * time.Minute)
	if n, _ := e.tc.Sweep(ctx); n != 1 || exists(e.hlsDir(fresh.ID)) || !exists(e.hlsDir(old.ID)) {
		t.Fatalf("idle sweep: %d", n)
	}
	e.advance(10 * time.Minute)
	if n, _ := e.tc.Sweep(ctx); n != 1 || exists(e.hlsDir(old.ID)) {
		t.Fatalf("old idle sweep: %d", n)
	}
	// restart: a fresh Transcoder adopts a finished folder (mtime = last access)
	again := e.enqueue("budi", "again.mkv")
	e.wait("budi", again.ID, final, "again done")
	restarted := NewTranscoder(e.tc.o)
	if n, _ := restarted.Sweep(ctx); n != 0 || !exists(e.hlsDir(again.ID)) {
		t.Fatalf("adopt sweep removed %d", n)
	}
	info, _ := e.jails["budi"].Stat("/again.mkv")
	if id, ok := restarted.FindDone(e.users["budi"], "/again.mkv", info.Size, info.ModTime.UnixNano()); !ok || id != again.ID || !restarted.Ready(id) {
		t.Fatal("not adopted")
	}
	stale := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(e.hlsDir(again.ID), stale, stale)
	restarted2 := NewTranscoder(TranscoderOptions{DataDir: e.data, Tasks: e.store, Settings: e.tc.o.Settings, Now: func() time.Time { return time.Now().Add(2 * time.Hour) }})
	if n, _ := restarted2.Sweep(ctx); n != 1 {
		t.Fatalf("stale adopted folder kept: %d", n)
	}
	// a deleted task's folder goes; disabled removes finished folders
	t1 := e.enqueue("budi", "t1.mkv")
	e.wait("budi", t1.ID, final, "t1 done")
	t2 := e.enqueue("sari", "t2.mkv")
	e.wait("sari", t2.ID, final, "t2 done")
	if err := e.engine.Delete(ctx, e.users["budi"], t1.ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.tc.Sweep(ctx); n != 1 || exists(e.hlsDir(t1.ID)) || !exists(e.hlsDir(t2.ID)) {
		t.Fatalf("deleted task sweep: %d", n)
	}
	e.settings(func(s *TranscodeSettings) { s.Enabled = false })
	if n, _ := e.tc.Sweep(ctx); n != 1 || exists(e.hlsDir(t2.ID)) {
		t.Fatalf("disabled sweep: %d", n)
	}
}

// TestHLSDiskBudget: above the budget new conversions are refused (at
// enqueue and when a queued task starts).
func TestHLSDiskBudget(t *testing.T) {
	e := newTCEnv(t, func(o *TranscoderOptions) { o.DiskBudget = 40 })
	if err := e.tc.CheckDisk(); err != nil {
		t.Fatalf("empty: %v", err)
	}
	first := e.enqueue("budi", "a.mkv")
	e.wait("budi", first.ID, final, "a done")
	if u := e.tc.DiskUsage(); u < 40 {
		t.Fatalf("usage %d", u)
	}
	var te *TranscodeError
	if err := e.tc.CheckDisk(); !errors.As(err, &te) || te.Reason != ReasonDiskBudget {
		t.Fatalf("over budget: %v", err)
	}
	second := e.enqueue("budi", "b.mkv")
	if x := e.wait("budi", second.ID, final, "b refused"); x.State != tasks.Failed || x.Error.Code != "CONFLICT" {
		t.Fatalf("queued over budget: %+v", x.Error)
	}
}

func TestRewritePlaylist(t *testing.T) {
	in := "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n#EXT-X-PLAYLIST-TYPE:EVENT\n#EXTINF:4.000000,\nseg_00000.ts\n#EXTINF:4.000000,\r\nseg_00001.ts\n"
	out, err := RewritePlaylist([]byte(in), "scope=u%3Ax&exp=9&sig=abc")
	if err != nil {
		t.Fatal(err)
	}
	want := "#EXTM3U\n#EXT-X-START:TIME-OFFSET=0\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n#EXT-X-PLAYLIST-TYPE:EVENT\n#EXTINF:4.000000,\nseg_00000.ts?scope=u%3Ax&exp=9&sig=abc\n#EXTINF:4.000000,\nseg_00001.ts?scope=u%3Ax&exp=9&sig=abc\n"
	if string(out) != want {
		t.Fatalf("rewrite:\n%s", out)
	}
	for _, bad := range []string{"#EXTM3U\n../../etc/passwd\n", "#EXTM3U\nhttps://evil.example/x.ts\n", "hello\n", ""} {
		if _, err := RewritePlaylist([]byte(bad), "q"); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
