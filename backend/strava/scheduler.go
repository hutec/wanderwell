package strava

import (
	"context"
	"errors"
	"sync"
)

// ErrSchedulerClosed is returned by activity requests after StravaAPI.Shutdown.
var ErrSchedulerClosed = errors.New("strava request scheduler is closed")

// requestScheduler executes individual requests using a fixed worker pool.
// It must be constructed with newRequestScheduler and must not be copied.
type requestScheduler struct {
	mu      sync.Mutex
	pending chan scheduledRequest // Accessed only under mu; never closed.
	changed chan struct{}         // Replaced under mu whenever the queue changes.
	ctx     context.Context
	cancel  context.CancelFunc
	workers sync.WaitGroup
}

type scheduledRequest struct {
	ctx context.Context
	run func(context.Context)
}

// newRequestScheduler starts a fixed number of workers with room for
// pendingCapacity waiting requests, in addition to executing requests. Both
// settings must be positive. Callers wait for space when the queue is full;
// the scheduler does not create goroutines for pending or blocked requests.
func newRequestScheduler(workers, pendingCapacity int) (*requestScheduler, error) {
	if workers <= 0 || pendingCapacity <= 0 {
		return nil, errors.New("strava scheduler workers and pending capacity must be positive")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &requestScheduler{
		pending: make(chan scheduledRequest, pendingCapacity),
		changed: make(chan struct{}),
		ctx:     ctx,
		cancel:  cancel,
	}
	s.workers.Add(workers)
	for range workers {
		go s.work()
	}
	return s, nil
}

// executeRequest waits for queue space, executes request at most once, and returns its
// typed result and error to this caller. The callback receives a context that
// preserves ctx's values and deadline and is canceled by either ctx or shutdown.
// Cancellation while queued prevents execution; canceled entries occupy space
// until a worker dequeues them. Waiting for space and completion both respect ctx.
// If completion races cancellation or shutdown, either outcome may be returned.
//
// Callbacks must cooperate with cancellation so shutdown can finish. They must
// not call shutdown or synchronously submit more work to the same scheduler:
// doing so can deadlock the worker pool.
func executeRequest[T any](ctx context.Context, s *requestScheduler, request func(context.Context) (T, error)) (T, error) {
	type result struct {
		value T
		err   error
	}
	// A worker can finish even if its caller has already returned on cancellation.
	results := make(chan result, 1)
	job := scheduledRequest{
		ctx: ctx,
		run: func(ctx context.Context) {
			value, err := request(ctx)
			results <- result{value, err}
		},
	}
	var zero T
	if err := s.enqueue(ctx, job); err != nil {
		return zero, err
	}
	select {
	case result := <-results:
		return result.value, result.err
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-s.ctx.Done():
		return zero, ErrSchedulerClosed
	}
}

func (s *requestScheduler) enqueue(ctx context.Context, job scheduledRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		if s.ctx.Err() != nil {
			return ErrSchedulerClosed
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		select {
		case s.pending <- job:
			s.notify()
			return nil
		default:
		}
		changed := s.changed
		s.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
		}
		s.mu.Lock()
	}
}

func (s *requestScheduler) next() (scheduledRequest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		if s.ctx.Err() != nil {
			return scheduledRequest{}, false
		}
		select {
		case job := <-s.pending:
			s.notify()
			return job, true
		default:
		}
		changed := s.changed
		s.mu.Unlock()
		<-changed
		s.mu.Lock()
	}
}

// notify broadcasts under mu, so no queue change or shutdown wakeup can be lost.
func (s *requestScheduler) notify() {
	close(s.changed)
	s.changed = make(chan struct{})
}

func (s *requestScheduler) work() {
	defer s.workers.Done()
	for {
		job, ok := s.next()
		if !ok {
			return
		}
		ctx, cancel := context.WithCancel(job.ctx)
		// Only active workers register shutdown callbacks, not queued submitters.
		stop := context.AfterFunc(s.ctx, cancel)
		if ctx.Err() == nil && s.ctx.Err() == nil {
			job.run(ctx)
		}
		stop()
		cancel()
	}
}

// shutdown rejects further submissions, releases queued and blocked callers,
// cancels active callback contexts, and waits for all workers to exit. It is safe
// to call concurrently and repeatedly. Callbacks must honor cancellation for
// shutdown to return; callers are released without waiting for those callbacks.
func (s *requestScheduler) shutdown() {
	s.mu.Lock()
	if s.ctx.Err() == nil {
		s.cancel()
		s.pending = nil // Release queued callbacks without ever closing a job channel.
		s.notify()
	}
	s.mu.Unlock()
	s.workers.Wait()
}
