package queue

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// defaultMaxFinished bounds the recent-history ring.
const defaultMaxFinished = 100

// ErrNotFound is returned by Get/Retry/Cancel for an unknown job id.
var ErrNotFound = errors.New("job not found")

// ErrNotCapped is returned by Continue for a job that did not hit the cap, so
// there is no next window to fetch.
var ErrNotCapped = errors.New("job was not capped")

// Options carries per-job settings supplied at enqueue time.
// Empty fields fall back to the configured defaults inside the processor.
type Options struct {
	Gallery  string
	Folder   string
	MaxItems int
	// Offset skips this many leading posts, so a continued capped search fetches
	// the next window rather than re-resolving from the start.
	Offset int
	// Priority marks a single-post / wait request so it jumps ahead of bulk
	// jobs in the FIFO.
	Priority bool
}

// Processor runs a job's full pipeline (resolve, download, map, push). It is
// injected so the queue is unit-testable with a fake. Process should return
// nil on normal completion even when some
// items failed (per-item failures live on the items); it returns an error
// only for a job-level abort such as a failed resolve.
type Processor interface {
	Process(ctx context.Context, job *Job) error
}

// Queue is the in-memory download queue: a FIFO of pending jobs, the set of
// running jobs, and a bounded ring of recently finished jobs, all guarded
// by a single mutex. Worker goroutines pull pending jobs and
// drive them through the injected Processor.
type Queue struct {
	mu      sync.Mutex
	cond    *sync.Cond
	pending []*Job
	running map[int64]*Job
	cancels map[int64]context.CancelFunc
	// finished is the recent-history ring, oldest first; index maps every
	// tracked job id to its live *Job.
	finished    []*Job
	index       map[int64]*Job
	maxFinished int
	nextID      int64
	closed      bool

	proc    Processor
	workers int
	wg      sync.WaitGroup

	// now is the clock, overridable in tests.
	now func() time.Time
}

// New builds a queue with the given worker count and recent-history bound.
// A workers value below 1 is snapped to 1; a maxFinished below 1 uses the
// default. Call Start to launch the workers.
func New(proc Processor, workers, maxFinished int) *Queue {
	if workers < 1 {
		workers = 1
	}
	if maxFinished < 1 {
		maxFinished = defaultMaxFinished
	}
	q := &Queue{
		running:     map[int64]*Job{},
		cancels:     map[int64]context.CancelFunc{},
		index:       map[int64]*Job{},
		maxFinished: maxFinished,
		proc:        proc,
		workers:     workers,
		now:         time.Now,
	}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// Workers returns the number of running worker goroutines. It is fixed when
// the queue is built, so changing downloader.concurrency takes effect only on
// restart.
func (q *Queue) Workers() int { return q.workers }

// Enqueue creates a queued job for url and returns its id. The job jumps
// ahead of bulk jobs when opts.Priority is set.
func (q *Queue) Enqueue(url string, opts Options) int64 {
	q.mu.Lock()
	q.nextID++
	id := q.nextID
	j := newJob(id, url, opts, q.now())
	q.index[id] = j
	q.pushPendingLocked(j)
	q.cond.Broadcast()
	q.mu.Unlock()
	return id
}

// Get returns a snapshot of the job with the given id.
func (q *Queue) Get(id int64) (*Job, error) {
	q.mu.Lock()
	j := q.index[id]
	q.mu.Unlock()
	if j == nil {
		return nil, ErrNotFound
	}
	return j.Snapshot(), nil
}

// ListOptions filters and paginates List.
type ListOptions struct {
	Status JobStatus // "" = every status
	Limit  int       // 0 = no limit
	Page   int       // 1-based; <=1 means the first page
}

// List returns job snapshots newest-first, filtered by status and
// paginated, plus the total number of matching jobs (pre-pagination).
func (q *Queue) List(opts ListOptions) ([]*Job, int) {
	q.mu.Lock()
	all := make([]*Job, 0, len(q.index))
	for _, j := range q.index {
		all = append(all, j)
	}
	q.mu.Unlock()

	// Snapshot outside the queue lock, then filter on the stable copy.
	snaps := make([]*Job, 0, len(all))
	for _, j := range all {
		s := j.Snapshot()
		if opts.Status == "" || s.Status == opts.Status {
			snaps = append(snaps, s)
		}
	}
	sort.Slice(snaps, func(i, k int) bool { return snaps[i].ID > snaps[k].ID })

	total := len(snaps)
	if opts.Limit > 0 {
		page := opts.Page
		if page < 1 {
			page = 1
		}
		start := (page - 1) * opts.Limit
		if start >= len(snaps) {
			return []*Job{}, total
		}
		end := start + opts.Limit
		if end > len(snaps) {
			end = len(snaps)
		}
		snaps = snaps[start:end]
	}
	return snaps, total
}

// Retry re-queues a finished job by id, clearing its prior run. force re-runs
// it with the download-archive bypassed so already-fetched posts are fetched
// again.
func (q *Queue) Retry(id int64, force bool) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	j := q.index[id]
	if j == nil {
		return ErrNotFound
	}
	if err := j.reset(force); err != nil {
		return err
	}
	q.removeFromFinishedLocked(id)
	q.pushPendingLocked(j)
	q.cond.Broadcast()
	return nil
}

// Continue enqueues a follow-up job for the window after a capped job's, so the
// next batch of a truncated search comes down without re-resolving the part
// already fetched. It returns the new job id; the source must have been capped.
func (q *Queue) Continue(id int64) (int64, error) {
	src, err := q.Get(id)
	if err != nil {
		return 0, err
	}
	if !src.Capped {
		return 0, ErrNotCapped
	}
	return q.Enqueue(src.URL, Options{
		Gallery:  src.Gallery,
		Folder:   src.Folder,
		MaxItems: src.Cap,
		Offset:   src.Offset + src.Cap,
	}), nil
}

// Cancel implements the DELETE contract: a running job is
// signalled to stop (it finalizes as canceled and stays in history); a
// pending or finished job is removed from tracking entirely.
func (q *Queue) Cancel(id int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	j := q.index[id]
	if j == nil {
		return ErrNotFound
	}
	if cancel, ok := q.cancels[id]; ok {
		cancel()
		return nil
	}
	// Not running: drop it from the pending FIFO or the finished ring.
	q.removeFromPendingLocked(id)
	q.removeFromFinishedLocked(id)
	delete(q.index, id)
	return nil
}

// Wait blocks until the job reaches a terminal state or ctx is done,
// returning the final snapshot. Used by the API's POST /queue?wait=N path.
// A job that is already finished returns immediately.
func (q *Queue) Wait(ctx context.Context, id int64) (*Job, error) {
	q.mu.Lock()
	j := q.index[id]
	q.mu.Unlock()
	if j == nil {
		return nil, ErrNotFound
	}
	select {
	case <-j.doneChan():
		return j.Snapshot(), nil
	case <-ctx.Done():
		return j.Snapshot(), ctx.Err()
	}
}

// Clear drops every finished job from the recent-history ring, freeing the
// items they held. Running and pending jobs are left untouched.
func (q *Queue) Clear() {
	q.mu.Lock()
	for _, j := range q.finished {
		delete(q.index, j.ID)
	}
	q.finished = nil
	q.mu.Unlock()
}

// pushPendingLocked appends bulk jobs to the FIFO tail and inserts priority
// jobs after the last existing priority job, so priority jobs jump ahead of
// bulk work while preserving FIFO order within each class. Caller holds mu.
func (q *Queue) pushPendingLocked(j *Job) {
	if !j.Priority {
		q.pending = append(q.pending, j)
		return
	}
	i := 0
	for i < len(q.pending) && q.pending[i].Priority {
		i++
	}
	q.pending = append(q.pending, nil)
	copy(q.pending[i+1:], q.pending[i:])
	q.pending[i] = j
}

func (q *Queue) removeFromPendingLocked(id int64) {
	for i, j := range q.pending {
		if j.ID == id {
			q.pending = append(q.pending[:i], q.pending[i+1:]...)
			return
		}
	}
}

func (q *Queue) removeFromFinishedLocked(id int64) {
	for i, j := range q.finished {
		if j.ID == id {
			q.finished = append(q.finished[:i], q.finished[i+1:]...)
			return
		}
	}
}

// pushFinishedLocked appends a finished job to the ring and evicts the
// oldest entries past the bound, dropping them from the index too. Caller
// holds mu.
func (q *Queue) pushFinishedLocked(j *Job) {
	q.finished = append(q.finished, j)
	for len(q.finished) > q.maxFinished {
		evicted := q.finished[0]
		// Null the slot before reslicing: the evicted job (which can pin up to
		// max_items_per_job items) stays reachable in the backing array
		// otherwise, so the ring's footprint would sawtooth to ~2x the bound.
		q.finished[0] = nil
		q.finished = q.finished[1:]
		delete(q.index, evicted.ID)
	}
}
