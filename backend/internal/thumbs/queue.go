package thumbs

import (
	"container/heap"
	"sync"

	"kppbwi.app/filemanager/internal/fsjail"
)

// Job is one unit of work: render every size of one file (or only probe
// its metadata when ProbeOnly).
type Job struct {
	UserID    string
	Username  string
	Path      fsjail.VPath
	ProbeOnly bool
	// Low marks background work (listing-triggered probes, folder batches)
	// that must never delay a viewport request.
	Low bool
	// seq orders jobs of equal class: newest request first (SPEC §8.4:
	// what the user is looking at now wins).
	seq   uint64
	index int
}

func (j *Job) key() string { return j.UserID + "|" + string(j.Path) }

// queue is a priority queue with de-duplication by (user, path). A job that
// is requested again while queued is promoted (newer seq, Low cleared when
// the new request is not low); a job that is already running is not
// re-queued, the caller gets "pending" and retries.
type queue struct {
	mu      sync.Mutex
	cond    *sync.Cond
	items   jobHeap
	queued  map[string]*Job
	running map[string]struct{}
	seq     uint64
	closed  bool
}

func newQueue() *queue {
	q := &queue{queued: map[string]*Job{}, running: map[string]struct{}{}}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// push enqueues or promotes j. It returns false when the job is already
// running (nothing to do) and true when it is (now) queued.
func (q *queue) push(j Job) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	k := j.key()
	if _, ok := q.running[k]; ok {
		return false
	}
	q.seq++
	if cur, ok := q.queued[k]; ok {
		cur.seq = q.seq
		if !j.Low {
			cur.Low = false
		}
		if !j.ProbeOnly {
			cur.ProbeOnly = false
		}
		heap.Fix(&q.items, cur.index)
		return true
	}
	j.seq = q.seq
	nj := j
	q.queued[k] = &nj
	heap.Push(&q.items, &nj)
	q.cond.Signal()
	return true
}

// pop blocks until a job is available (or the queue is closed → ok=false)
// and marks it running.
func (q *queue) pop() (Job, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 && !q.closed {
		q.cond.Wait()
	}
	if len(q.items) == 0 {
		return Job{}, false
	}
	j := heap.Pop(&q.items).(*Job)
	delete(q.queued, j.key())
	q.running[j.key()] = struct{}{}
	return *j, true
}

// done releases the running mark.
func (q *queue) done(j Job) {
	q.mu.Lock()
	delete(q.running, j.key())
	q.mu.Unlock()
}

// state reports whether a (user, path) is queued or running.
func (q *queue) state(userID string, path fsjail.VPath) (queued, running bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	k := userID + "|" + string(path)
	_, queued = q.queued[k]
	_, running = q.running[k]
	return
}

// drop removes queued jobs under dir (invalidation). Running jobs re-check
// the file themselves.
func (q *queue) drop(userID string, dir fsjail.VPath) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for k, j := range q.queued {
		if j.UserID == userID && (j.Path == dir || j.Path.HasPrefix(dir)) {
			heap.Remove(&q.items, j.index)
			delete(q.queued, k)
		}
	}
}

func (q *queue) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.cond.Broadcast()
}

// Len returns the number of queued jobs.
func (q *queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

type jobHeap []*Job

func (h jobHeap) Len() int { return len(h) }
func (h jobHeap) Less(a, b int) bool {
	if h[a].Low != h[b].Low {
		return !h[a].Low // viewport requests before background work
	}
	return h[a].seq > h[b].seq // newest first
}
func (h jobHeap) Swap(a, b int) {
	h[a], h[b] = h[b], h[a]
	h[a].index, h[b].index = a, b
}
func (h *jobHeap) Push(x any) {
	j := x.(*Job)
	j.index = len(*h)
	*h = append(*h, j)
}
func (h *jobHeap) Pop() any {
	old := *h
	n := len(old)
	j := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return j
}
