package schedgroup

import (
	"container/heap"
	"context"
	"sync"
	"time"
)

// START GROUP OMIT

// A Group is a goroutine worker pool which schedules tasks to be performed
// after a specified time. A Group must be created with the New constructor.
type Group struct {
	// Context/cancelation support.
	ctx    context.Context
	cancel func()

	// Task runner and a heap of tasks to be run.
	wg    sync.WaitGroup
	mu    sync.Mutex
	tasks tasks

	// Signals for when a task is added and how many tasks remain on the heap.
	addC chan struct{}
	lenC chan int
}

// END GROUP OMIT

// New creates a new Group which will use ctx for cancelation. If cancelation
// is not a concern, use context.Background().
func New(ctx context.Context) *Group {
	// Monitor goroutine context and cancelation.
	mctx, cancel := context.WithCancel(ctx)

	g := &Group{
		ctx:    ctx,
		cancel: cancel,

		addC: make(chan struct{}),
		lenC: make(chan int),
	}

	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		g.monitor(mctx)
	}()

	return g
}

// Delay schedules a function to run at or after the specified delay. Delay
// is a convenience wrapper for Schedule which adds delay to the current time.
// Specifying a negative delay will cause the task to be scheduled immediately.
//
// If Delay is called after a call to Wait, Delay will panic.
func (g *Group) Delay(delay time.Duration, fn func()) {
	g.Schedule(time.Now().Add(delay), fn)
}

// START SCHEDULE OMIT

// Schedule schedules a function to run at or after the specified time.
// Specifying a past time will cause the task to be scheduled immediately.
func (g *Group) Schedule(when time.Time, fn func()) {
	g.mu.Lock()
	defer g.mu.Unlock()

	heap.Push(&g.tasks, task{
		Deadline: when,
		Call:     fn,
	})

	// Notify monitor that a new task has been pushed on to the heap.
	select {
	case g.addC <- struct{}{}:
	default:
	}
}

// END SCHEDULE OMIT

// START WAIT1 OMIT

// Wait waits for the completion of all scheduled tasks, or for cancelation of
// the context passed to New.
func (g *Group) Wait() error {
	// Context cancelation takes priority.
	if err := g.ctx.Err(); err != nil {
		return err
	}

	// See if the task heap is already empty. If so, we can exit early.
	g.mu.Lock()
	if g.tasks.Len() == 0 {
		// Release the mutex immediately so that any running jobs are able to
		// complete and send on g.lenC.
		//
		// Tip: Ctrl+\ sends SIGQUIT to dump stacks.
		g.mu.Unlock()
		g.cancel()
		g.wg.Wait()
		return nil
	}
	g.mu.Unlock()

	// END WAIT1 OMIT
	// START WAIT2 OMIT

	// Wait on context cancelation or for the number of items in the heap to reach 0.
	var n int
	for {
		select {
		case <-g.ctx.Done():
			return g.ctx.Err()
		case n = <-g.lenC:
			if err := g.ctx.Err(); err != nil {
				return err
			}
		}

		if n == 0 {
			// No more tasks left, cancel the monitor goroutine and wait for tasks to complete.
			g.cancel()
			g.wg.Wait()
			return nil
		}
	}
}

// END WAIT2 OMIT

// START MONITOR1 OMIT

// monitor triggers tasks until ctx is canceled.
func (g *Group) monitor(ctx context.Context) {
	t := time.NewTimer(0)
	defer t.Stop()
	for {
		if ctx.Err() != nil {
			return
		}

		// Start any tasks that are ready as of now.
		now := time.Now()
		var tickC <-chan time.Time
		next := g.trigger(now)
		if !next.IsZero() {
			// Wait until the next scheduled task is ready.
			t.Reset(next.Sub(now))
			tickC = t.C
		} else {
			t.Stop()
		}

		// END MONITOR1 OMIT
		// START MONITOR2 OMIT
		select {
		case <-ctx.Done():
			// Context canceled.
		case <-g.addC:
			// A new task was added, check task heap again.
		case <-tickC:
			// If not nil, an existing task should be ready as of now.
		}

		// END MONITOR2 OMIT
	}
}

// START TRIGGER1 OMIT
// trigger checks for scheduled tasks and runs them if they are scheduled
// on or after the time specified by now.
func (g *Group) trigger(now time.Time) time.Time {
	g.mu.Lock()
	defer func() {
		// Notify how many tasks are left on the heap so Wait can stop when
		// appropriate.
		select {
		case g.lenC <- g.tasks.Len():
		default:
			// Wait hasn't been called.
		}

		g.mu.Unlock()
	}()

	// END TRIGGER1 OMIT

	// START TRIGGER2 OMIT
	for g.tasks.Len() > 0 {
		next := &g.tasks[0]
		if next.Deadline.After(now) {
			// Earliest scheduled task is not ready. We return its deadline
			// so the monitor timer knows exactly when to wake up.
			return next.Deadline
		}

		// This task is ready, pop it from the heap and run it.
		t := heap.Pop(&g.tasks).(task)
		g.wg.Add(1)
		go func() {
			defer g.wg.Done()
			t.Call()
		}()
	}

	// No more tasks in the heap, stop the timer until one is added.
	return time.Time{}
}

// END TRIGGER2 OMIT

// A task is a function which is called after the specified deadline.
type task struct {
	Deadline time.Time
	Call     func()
}

// tasks implements heap.Interface.
type tasks []task

var _ heap.Interface = &tasks{}

func (pq tasks) Len() int            { return len(pq) }
func (pq tasks) Less(i, j int) bool  { return pq[i].Deadline.Before(pq[j].Deadline) }
func (pq tasks) Swap(i, j int)       { pq[i], pq[j] = pq[j], pq[i] }
func (pq *tasks) Push(x interface{}) { *pq = append(*pq, x.(task)) }
func (pq *tasks) Pop() (item interface{}) {
	n := len(*pq)
	item, *pq = (*pq)[n-1], (*pq)[:n-1]
	return item
}
