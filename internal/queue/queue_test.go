package queue

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// noopProcessor never does anything; used by tests that exercise queue
// bookkeeping without launching workers.
type noopProcessor struct{}

func (noopProcessor) Process(context.Context, *Job) error { return nil }

func TestJobTransitions(t *testing.T) {
	all := []JobStatus{JobQueued, JobRunning, JobSucceeded, JobPartial, JobFailed, JobCanceled}
	legal := map[JobStatus]map[JobStatus]bool{
		JobQueued:    {JobRunning: true, JobCanceled: true},
		JobRunning:   {JobSucceeded: true, JobPartial: true, JobFailed: true, JobCanceled: true},
		JobSucceeded: {JobQueued: true},
		JobPartial:   {JobQueued: true},
		JobFailed:    {JobQueued: true},
		JobCanceled:  {JobQueued: true},
	}
	for _, from := range all {
		for _, to := range all {
			want := legal[from][to]
			if got := validJobTransition(from, to); got != want {
				t.Errorf("validJobTransition(%s, %s) = %v, want %v", from, to, got, want)
			}
		}
	}
}

func TestItemTransitions(t *testing.T) {
	all := []ItemStatus{ItemPending, ItemDownloaded, ItemUploaded, ItemDone, ItemSkipped, ItemFailed}
	legal := map[ItemStatus]map[ItemStatus]bool{
		ItemPending:    {ItemDownloaded: true, ItemSkipped: true, ItemFailed: true},
		ItemDownloaded: {ItemUploaded: true, ItemFailed: true},
		ItemUploaded:   {ItemDone: true, ItemSkipped: true, ItemFailed: true},
	}
	for _, from := range all {
		for _, to := range all {
			want := legal[from][to]
			if got := validItemTransition(from, to); got != want {
				t.Errorf("validItemTransition(%s, %s) = %v, want %v", from, to, got, want)
			}
		}
	}
}

func TestSummarizeAndDeriveStatus(t *testing.T) {
	cases := []struct {
		name    string
		items   []Item
		summary Summary
		status  JobStatus
	}{
		{"empty", nil, Summary{}, JobSucceeded},
		{
			"all created",
			[]Item{{Outcome: OutcomeCreated}, {Outcome: OutcomeCreated}},
			Summary{Created: 2, Total: 2},
			JobSucceeded,
		},
		{
			"mixed is partial",
			[]Item{{Outcome: OutcomeCreated}, {Outcome: OutcomeDuplicate}, {Outcome: OutcomeSkippedArchive}, {Outcome: OutcomeFailed}},
			Summary{Created: 1, Duplicate: 1, Skipped: 1, Failed: 1, Total: 4},
			JobPartial,
		},
		{
			"all failed",
			[]Item{{Outcome: OutcomeFailed}, {Outcome: OutcomeFailed}},
			Summary{Failed: 2, Total: 2},
			JobFailed,
		},
		{
			"duplicates and skips only succeed",
			[]Item{{Outcome: OutcomeDuplicate}, {Outcome: OutcomeSkippedArchive}},
			Summary{Duplicate: 1, Skipped: 1, Total: 2},
			JobSucceeded,
		},
	}
	for _, tc := range cases {
		if got := summarize(tc.items); got != tc.summary {
			t.Errorf("%s: summarize = %+v, want %+v", tc.name, got, tc.summary)
		}
		if got := deriveStatus(tc.items); got != tc.status {
			t.Errorf("%s: deriveStatus = %s, want %s", tc.name, got, tc.status)
		}
	}
}

func TestUpdateItemRejectsIllegalTransition(t *testing.T) {
	j := newJob(1, "u", Options{}, time.Now())
	j.SetItems([]Item{{PostID: "1"}})
	// pending -> done is not a legal item transition.
	if j.UpdateItem(0, func(it *Item) { it.Status = ItemDone }) {
		t.Error("UpdateItem should report an illegal pending->done transition")
	}
	// pending -> downloaded is legal.
	if !j.UpdateItem(0, func(it *Item) { it.Status = ItemDownloaded }) {
		t.Error("UpdateItem should accept pending->downloaded")
	}
	// out-of-range index.
	if j.UpdateItem(9, func(it *Item) {}) {
		t.Error("UpdateItem should reject an out-of-range index")
	}
}

func TestRetryKeepsBackLinkOnArchiveSkip(t *testing.T) {
	// A plain retry of a created job archive-skips the post on re-run; its
	// monbooru back-link must survive rather than being dropped.
	j := newJob(1, "http://danbooru/posts/489", Options{}, time.Now())
	j.SetItems([]Item{{PostID: "489"}})
	j.UpdateItem(0, func(it *Item) { it.Status = ItemDownloaded })
	j.UpdateItem(0, func(it *Item) { it.Status = ItemUploaded })
	j.UpdateItem(0, func(it *Item) {
		it.Status = ItemDone
		it.Outcome = OutcomeCreated
		it.MonbooruID = 77
		it.SHA256 = "abc"
	})
	j.Finalize(time.Now())

	if err := j.reset(false); err != nil {
		t.Fatalf("reset: %v", err)
	}
	j.SetItems([]Item{{PostID: "489"}})
	j.UpdateItem(0, func(it *Item) {
		it.Status = ItemSkipped
		it.Outcome = OutcomeSkippedArchive
	})
	j.Finalize(time.Now())

	got := j.Items[0]
	if got.Outcome != OutcomeSkippedArchive {
		t.Errorf("outcome = %s, want skipped_archive", got.Outcome)
	}
	if got.MonbooruID != 77 || got.SHA256 != "abc" {
		t.Errorf("retry dropped the back-link: monbooru_id=%d sha256=%q, want 77/abc", got.MonbooruID, got.SHA256)
	}
}

func TestEnqueueGetListAndIDs(t *testing.T) {
	q := New(noopProcessor{}, 1, 100) // not started: jobs stay queued
	ids := make([]int64, 5)
	for i := range ids {
		ids[i] = q.Enqueue(fmt.Sprintf("http://x/%d", i), Options{})
	}
	if ids[0] != 1 || ids[4] != 5 {
		t.Errorf("ids should be 1..5, got %v", ids)
	}
	for _, id := range ids {
		if _, err := q.Get(id); err != nil {
			t.Errorf("Get(%d): %v", id, err)
		}
	}
	if _, err := q.Get(999); err != ErrNotFound {
		t.Errorf("Get(unknown) = %v, want ErrNotFound", err)
	}

	all, total := q.List(ListOptions{})
	if total != 5 || len(all) != 5 {
		t.Fatalf("List all: total=%d len=%d, want 5/5", total, len(all))
	}
	// Newest first.
	if all[0].ID != 5 || all[4].ID != 1 {
		t.Errorf("List order = %d..%d, want 5..1", all[0].ID, all[4].ID)
	}
	queued, n := q.List(ListOptions{Status: JobQueued})
	if n != 5 || len(queued) != 5 {
		t.Errorf("List queued: n=%d len=%d, want 5/5", n, len(queued))
	}
	if _, n := q.List(ListOptions{Status: JobSucceeded}); n != 0 {
		t.Errorf("List succeeded n=%d, want 0", n)
	}

	page2, total := q.List(ListOptions{Limit: 2, Page: 2})
	if total != 5 || len(page2) != 2 || page2[0].ID != 3 || page2[1].ID != 2 {
		t.Errorf("page 2 (limit 2) = %v total=%d, want ids [3 2] total 5", idsOf(page2), total)
	}
	if page, _ := q.List(ListOptions{Limit: 2, Page: 9}); len(page) != 0 {
		t.Errorf("page past the end should be empty, got %d", len(page))
	}
}

func idsOf(jobs []*Job) []int64 {
	out := make([]int64, len(jobs))
	for i, j := range jobs {
		out[i] = j.ID
	}
	return out
}

func TestRingEvictsPastBound(t *testing.T) {
	// White-box: push five finished jobs through a ring bounded at three and
	// confirm only the newest three remain indexed.
	q := New(noopProcessor{}, 1, 3)
	for i := int64(1); i <= 5; i++ {
		j := newJob(i, "u", Options{}, time.Now())
		q.index[i] = j
		q.pushFinishedLocked(j)
	}
	if len(q.finished) != 3 {
		t.Fatalf("ring holds %d, want 3", len(q.finished))
	}
	for _, gone := range []int64{1, 2} {
		if _, ok := q.index[gone]; ok {
			t.Errorf("job %d should have been evicted from the index", gone)
		}
	}
	for _, kept := range []int64{3, 4, 5} {
		if _, ok := q.index[kept]; !ok {
			t.Errorf("job %d should still be indexed", kept)
		}
	}
}

func TestQueueClear(t *testing.T) {
	// White-box: Clear empties the finished ring and de-indexes those jobs;
	// a pending job is untouched.
	q := New(noopProcessor{}, 1, 100)
	for i := int64(1); i <= 3; i++ {
		j := newJob(i, "u", Options{}, time.Now())
		q.index[i] = j
		q.pushFinishedLocked(j)
	}
	pending := newJob(99, "u", Options{}, time.Now())
	q.index[99] = pending
	q.pending = append(q.pending, pending)

	q.Clear()
	if len(q.finished) != 0 {
		t.Errorf("finished ring = %d, want 0 after clear", len(q.finished))
	}
	for _, gone := range []int64{1, 2, 3} {
		if _, ok := q.index[gone]; ok {
			t.Errorf("finished job %d should be de-indexed after clear", gone)
		}
	}
	if _, ok := q.index[99]; !ok || len(q.pending) != 1 {
		t.Error("a pending job should survive clear")
	}
}

func TestRetryAndCancelUnknownIDs(t *testing.T) {
	q := New(noopProcessor{}, 1, 100)
	if err := q.Retry(404, false); err != ErrNotFound {
		t.Errorf("Retry(unknown) = %v, want ErrNotFound", err)
	}
	if err := q.Cancel(404); err != ErrNotFound {
		t.Errorf("Cancel(unknown) = %v, want ErrNotFound", err)
	}
}

func TestContinueEnqueuesNextWindow(t *testing.T) {
	q := New(noopProcessor{}, 1, 100) // not started: jobs stay queued
	id := q.Enqueue("http://x/search", Options{Gallery: "art", MaxItems: 50})
	q.index[id].SetCapped(50) // simulate a capped run

	nid, err := q.Continue(id)
	if err != nil {
		t.Fatalf("Continue: %v", err)
	}
	if nid == id {
		t.Fatal("Continue should create a new job, not reuse the source")
	}
	nj, err := q.Get(nid)
	if err != nil {
		t.Fatal(err)
	}
	if nj.URL != "http://x/search" || nj.Gallery != "art" || nj.MaxItems != 50 || nj.Offset != 50 {
		t.Errorf("continued job = {url:%q gallery:%q max:%d offset:%d}, want the source's next window", nj.URL, nj.Gallery, nj.MaxItems, nj.Offset)
	}

	// Continuing the continuation advances the offset by the cap again.
	q.index[nid].SetCapped(50)
	n2, err := q.Continue(nid)
	if err != nil {
		t.Fatalf("Continue chain: %v", err)
	}
	if j, _ := q.Get(n2); j.Offset != 100 {
		t.Errorf("second continue offset = %d, want 100", j.Offset)
	}

	// A job that was not capped has no next window; an unknown id is not found.
	if _, err := q.Continue(q.Enqueue("http://y", Options{})); err != ErrNotCapped {
		t.Errorf("Continue(non-capped) = %v, want ErrNotCapped", err)
	}
	if _, err := q.Continue(404); err != ErrNotFound {
		t.Errorf("Continue(unknown) = %v, want ErrNotFound", err)
	}
}

func TestCancelRemovesPendingJob(t *testing.T) {
	q := New(noopProcessor{}, 1, 100) // not started: the job stays pending
	id := q.Enqueue("http://x", Options{})
	if err := q.Cancel(id); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if _, err := q.Get(id); err != ErrNotFound {
		t.Errorf("a canceled pending job should be removed, Get = %v", err)
	}
	if len(q.pending) != 0 {
		t.Errorf("pending should be empty after cancel, got %d", len(q.pending))
	}
}

func TestWaitTimesOutOnPendingJob(t *testing.T) {
	q := New(noopProcessor{}, 1, 100) // not started: nothing finishes
	id := q.Enqueue("http://x", Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := q.Wait(ctx, id); err == nil {
		t.Error("Wait on a never-finishing job should return ctx error")
	}
	if _, err := q.Wait(context.Background(), 404); err != ErrNotFound {
		t.Errorf("Wait(unknown) = %v, want ErrNotFound", err)
	}
}
