package cliauth

import (
	"container/heap"
	"context"
	"sync"
	"time"
)

type RefreshJobDispatcher struct {
	nextScheduleAt func(string, time.Time) (time.Time, bool)

	heapMu       sync.Mutex
	heapQueue    MinHeap
	heapIndex    map[string]*HeapItem
	pendingQueue map[string]struct{}

	wakeCh chan struct{}
	jobCh  chan<- string
}

func newRefreshJobDispatcher(
	jobCh chan<- string,
	nextScheduleAt func(string, time.Time) (time.Time, bool),
) *RefreshJobDispatcher {
	return &RefreshJobDispatcher{
		nextScheduleAt: nextScheduleAt,
		heapIndex:      make(map[string]*HeapItem),
		pendingQueue:   make(map[string]struct{}),
		wakeCh:         make(chan struct{}, 1),
		jobCh:          jobCh,
	}
}

func (d *RefreshJobDispatcher) Enqueue(id string) {
	if id == "" {
		return
	}
	d.heapMu.Lock()
	d.pendingQueue[id] = struct{}{}
	d.heapMu.Unlock()
	select {
	case d.wakeCh <- struct{}{}:
	default:
	}
}

func (d *RefreshJobDispatcher) DispatchLoop(ctx context.Context) {
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	defer timer.Stop()

	var timerCh <-chan time.Time
	d.resetTimer(timer, &timerCh, time.Now())

	for {
		select {
		case <-ctx.Done():
			return
		case <-d.wakeCh:
			now := time.Now()
			d.applyPendingQueue(now)
			d.resetTimer(timer, &timerCh, now)
		case <-timerCh:
			now := time.Now()
			d.handleDueJobs(ctx, now)
			d.applyPendingQueue(now)
			d.resetTimer(timer, &timerCh, now)
		}
	}
}

func (d *RefreshJobDispatcher) Reset(ids []string, now time.Time) {
	d.heapMu.Lock()
	defer d.heapMu.Unlock()

	d.heapQueue = d.heapQueue[:0]
	d.heapIndex = make(map[string]*HeapItem, len(ids))
	d.pendingQueue = make(map[string]struct{})

	for _, id := range ids {
		next, ok := d.nextScheduleAt(id, now)
		if !ok {
			continue
		}
		item := &HeapItem{id: id, next: next}
		heap.Push(&d.heapQueue, item)
		d.heapIndex[id] = item
	}
}

func (d *RefreshJobDispatcher) resetTimer(timer *time.Timer, timerCh *<-chan time.Time, now time.Time) {
	next, ok := d.heapPeek()
	if !ok {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		*timerCh = nil
		return
	}
	wait := next.Sub(now)
	if wait < 0 {
		wait = 0
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(wait)
	*timerCh = timer.C
}

func (d *RefreshJobDispatcher) handleDueJobs(ctx context.Context, now time.Time) {
	for _, id := range d.pickupDueJobs(now) {
		d.handleDueJob(ctx, now, id)
	}
}

func (d *RefreshJobDispatcher) handleDueJob(ctx context.Context, now time.Time, id string) {
	next, shouldSchedule := d.nextScheduleAt(id, now)
	if !shouldSchedule {
		return
	}

	if next.After(now) {
		d.heapUpsert(id, next)
		return
	}

	select {
	case <-ctx.Done():
		return
	case d.jobCh <- id:
	}
}

func (d *RefreshJobDispatcher) pickupDueJobs(now time.Time) []string {
	d.heapMu.Lock()
	defer d.heapMu.Unlock()
	var due []string
	for len(d.heapQueue) > 0 {
		item := d.heapQueue[0]
		if item == nil || item.next.After(now) {
			break
		}
		popped := heap.Pop(&d.heapQueue).(*HeapItem)
		if popped == nil {
			continue
		}
		delete(d.heapIndex, popped.id)
		due = append(due, popped.id)
	}
	return due
}

func (d *RefreshJobDispatcher) applyPendingQueue(now time.Time) {
	for _, id := range d.drainPendingQueue() {
		next, ok := d.nextScheduleAt(id, now)
		if !ok {
			d.heapRemove(id)
			continue
		}
		d.heapUpsert(id, next)
	}
}

func (d *RefreshJobDispatcher) drainPendingQueue() []string {
	d.heapMu.Lock()
	defer d.heapMu.Unlock()
	if len(d.pendingQueue) == 0 {
		return nil
	}
	out := make([]string, 0, len(d.pendingQueue))
	for id := range d.pendingQueue {
		out = append(out, id)
		delete(d.pendingQueue, id)
	}
	return out
}

func (d *RefreshJobDispatcher) heapPeek() (time.Time, bool) {
	d.heapMu.Lock()
	defer d.heapMu.Unlock()
	if len(d.heapQueue) == 0 {
		return time.Time{}, false
	}
	return d.heapQueue[0].next, true
}

func (d *RefreshJobDispatcher) heapUpsert(id string, next time.Time) {
	if id == "" || next.IsZero() {
		return
	}
	d.heapMu.Lock()
	defer d.heapMu.Unlock()
	if item, ok := d.heapIndex[id]; ok && item != nil {
		item.next = next
		heap.Fix(&d.heapQueue, item.index)
		return
	}
	item := &HeapItem{id: id, next: next}
	heap.Push(&d.heapQueue, item)
	d.heapIndex[id] = item
}

func (d *RefreshJobDispatcher) heapRemove(id string) {
	if id == "" {
		return
	}
	d.heapMu.Lock()
	defer d.heapMu.Unlock()
	item, ok := d.heapIndex[id]
	if !ok || item == nil {
		return
	}
	heap.Remove(&d.heapQueue, item.index)
	delete(d.heapIndex, id)
}

// ---- min-heap for refresh scheduling ----

type HeapItem struct {
	id    string
	next  time.Time
	index int
}

type MinHeap []*HeapItem

func (h MinHeap) Len() int           { return len(h) }
func (h MinHeap) Less(i, j int) bool { return h[i].next.Before(h[j].next) }
func (h MinHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *MinHeap) Push(x any) {
	item, ok := x.(*HeapItem)
	if !ok || item == nil {
		return
	}
	item.index = len(*h)
	*h = append(*h, item)
}

func (h *MinHeap) Pop() any {
	old := *h
	n := len(old)
	if n == 0 {
		return (*HeapItem)(nil)
	}
	item := old[n-1]
	item.index = -1
	*h = old[:n-1]
	return item
}
