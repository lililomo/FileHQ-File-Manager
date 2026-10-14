package thumbs

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/fsjail"
)

func TestQueueDedupAndPriority(t *testing.T) {
	q := newQueue()
	q.push(Job{UserID: "u", Path: "/low1", Low: true})
	q.push(Job{UserID: "u", Path: "/a"})
	q.push(Job{UserID: "u", Path: "/b"})
	q.push(Job{UserID: "u", Path: "/a"}) // duplicate → promoted to newest
	q.push(Job{UserID: "u", Path: "/low1", Low: true})
	if q.Len() != 3 {
		t.Fatalf("len = %d, want 3 (deduplicated)", q.Len())
	}
	order := []string{}
	for range 3 {
		j, ok := q.pop()
		if !ok {
			t.Fatal("pop failed")
		}
		order = append(order, string(j.Path))
		q.done(j)
	}
	// newest viewport request first, background last
	if order[0] != "/a" || order[1] != "/b" || order[2] != "/low1" {
		t.Fatalf("order = %v", order)
	}
}

func TestQueueRunningNotRequeued(t *testing.T) {
	q := newQueue()
	q.push(Job{UserID: "u", Path: "/x"})
	j, _ := q.pop()
	if q.push(Job{UserID: "u", Path: "/x"}) {
		t.Fatal("running job must not be re-queued")
	}
	queued, running := q.state("u", "/x")
	if queued || !running {
		t.Fatalf("state queued=%v running=%v", queued, running)
	}
	q.done(j)
	if !q.push(Job{UserID: "u", Path: "/x"}) {
		t.Fatal("after done the job can be queued again")
	}
}

func TestQueueLowPromotedByViewportRequest(t *testing.T) {
	q := newQueue()
	q.push(Job{UserID: "u", Path: "/p", Low: true, ProbeOnly: true})
	q.push(Job{UserID: "u", Path: "/other", Low: true})
	q.push(Job{UserID: "u", Path: "/p"}) // viewport request
	j, _ := q.pop()
	if j.Path != "/p" || j.Low || j.ProbeOnly {
		t.Fatalf("promoted job = %+v", j)
	}
}

func TestQueueDropPrefix(t *testing.T) {
	q := newQueue()
	q.push(Job{UserID: "u", Path: "/dir/a.jpg"})
	q.push(Job{UserID: "u", Path: "/dir/sub/b.jpg"})
	q.push(Job{UserID: "u", Path: "/dir2/c.jpg"})
	q.push(Job{UserID: "v", Path: "/dir/d.jpg"})
	q.drop("u", fsjail.VPath("/dir"))
	if q.Len() != 2 {
		t.Fatalf("len after drop = %d", q.Len())
	}
	paths := map[string]bool{}
	for range 2 {
		j, _ := q.pop()
		paths[j.UserID+j.Path.String()] = true
	}
	if !paths["u/dir2/c.jpg"] || !paths["v/dir/d.jpg"] {
		t.Fatalf("wrong survivors: %v", paths)
	}
}

func TestWorkerLimit(t *testing.T) {
	q := newQueue()
	const workers = 2
	var active, maxActive int32
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				j, ok := q.pop()
				if !ok {
					return
				}
				n := atomic.AddInt32(&active, 1)
				for {
					m := atomic.LoadInt32(&maxActive)
					if n <= m || atomic.CompareAndSwapInt32(&maxActive, m, n) {
						break
					}
				}
				time.Sleep(5 * time.Millisecond)
				atomic.AddInt32(&active, -1)
				q.done(j)
			}
		}()
	}
	for i := range 20 {
		q.push(Job{UserID: "u", Path: fsjail.VPath("/f" + string(rune('a'+i)))})
	}
	for q.Len() > 0 {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	q.close()
	wg.Wait()
	if maxActive > workers {
		t.Fatalf("max concurrent jobs %d > %d workers", maxActive, workers)
	}
}
