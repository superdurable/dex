package peekable

import (
	"context"
	"errors"
	"sync"
)

// ErrQueueFull is returned when PushNonBlocking is called on a full queue.
// Callers should wrap this with their own categorized error for proper error-at tracking.
var ErrQueueFull = errors.New("queue is full")

// ErrQueueClosed is returned when trying to push to a closed queue.
var ErrQueueClosed = errors.New("queue is closed")

// PeekableChannel provides a wrapper on top of Golang Channel to be peekable.
// Golang channel is not peekable, sometimes make things hard for cases that need to examine the item before actually
// consuming it (e.g. based on the size).
type PeekableChannel[T any] interface {
	// PushNonBlocking pushes a value into the queue without blocking
	// if the queue is full, returns ErrQueueFull
	// if the queue is closed, returns ErrQueueClosed
	PushNonBlocking(item *T) error
	// PushBlocking pushes a value into the queue, blocking until there is capacity,
	// or until context is done / queue is closed.
	// Returns context error when ctx is canceled/timed out, or ErrQueueClosed when closed.
	PushBlocking(ctx context.Context, item *T) error
	// PopNonBlocking receive the first item in the queue without blocking
	// if no item available, return nil and false
	PopNonBlocking() (*T, bool)
	// PopBlocking receives the first item in the queue if available, or blocking until available or context is done
	// Returns the item and true if successful, or nil and false if race-condition or context was cancelled/timed out.
	//
	// NOTE: This is designed for single-consumer use only. Multiple concurrent consumers may
	// result in race conditions where only one consumer gets the item while others get nil.
	PopBlocking(ctx context.Context) (*T, bool)
	// PeekBlocking reads the first item in the queue if available
	// It will block if the queue is empty, until the timeout from ctx.
	// Multiple concurrent PeekBlocking calls will all return the same peeked item
	// until PopNonBlocking or PopBlocking is called to consume it.
	PeekBlocking(ctx context.Context) (*T, bool)
	// Close stops the background goroutine and cleans up resources
	Close()
}

type peekRequest[T any] struct {
	ctx      context.Context
	resultCh chan<- peekResult[T]
}

type peekResult[T any] struct {
	item *T
	ok   bool
}

type peekableChannelImpl[T any] struct {
	mu        sync.Mutex // Mutex to protect the peeked value
	queue     chan *T
	peeked    *T // Pointer to the peeked value
	peekReqCh chan peekRequest[T]
	closeCh   chan struct{}
	stateMu   sync.Mutex // Protects closed state and push registration
	closed    bool
	pushWg    sync.WaitGroup
	closeOnce sync.Once
}

// NewPeekableChannel creates and returns a new peekableChannelImpl instance.
func NewPeekableChannel[T any](size int) PeekableChannel[T] {
	pc := &peekableChannelImpl[T]{
		queue:     make(chan *T, size),
		peekReqCh: make(chan peekRequest[T]),
		closeCh:   make(chan struct{}),
	}

	go pc.peekWorker()

	return pc
}

func (pc *peekableChannelImpl[T]) PushNonBlocking(item *T) error {
	if !pc.registerPush() {
		return ErrQueueClosed
	}
	defer pc.pushWg.Done()

	select {
	case pc.queue <- item:
		return nil
	default:
		return ErrQueueFull
	}
}

func (pc *peekableChannelImpl[T]) PushBlocking(ctx context.Context, item *T) error {
	if !pc.registerPush() {
		return ErrQueueClosed
	}
	defer pc.pushWg.Done()

	select {
	case pc.queue <- item:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-pc.closeCh:
		return ErrQueueClosed
	}
}

func (pc *peekableChannelImpl[T]) PopNonBlocking() (*T, bool) {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	if pc.peeked != nil {
		val := pc.peeked
		pc.peeked = nil
		return val, true
	}

	select {
	case item := <-pc.queue:
		return item, false
	default:
		return nil, false
	}
}

func (pc *peekableChannelImpl[T]) PopBlocking(ctx context.Context) (*T, bool) {
	_, ok := pc.PeekBlocking(ctx)
	if !ok {
		return nil, false
	}
	item, gotPeeked := pc.PopNonBlocking()
	if !gotPeeked {
		return nil, false
	}
	return item, true
}

func (pc *peekableChannelImpl[T]) PeekBlocking(ctx context.Context) (*T, bool) {
	pc.mu.Lock()
	if pc.peeked != nil {
		result := pc.peeked
		pc.mu.Unlock()
		return result, true
	}
	pc.mu.Unlock()

	resultCh := make(chan peekResult[T], 1)
	req := peekRequest[T]{
		ctx:      ctx,
		resultCh: resultCh,
	}

	select {
	case pc.peekReqCh <- req:
		select {
		case result := <-resultCh:
			return result.item, result.ok
		case <-ctx.Done():
			return nil, false
		case <-pc.closeCh:
			return nil, false
		}
	case <-ctx.Done():
		return nil, false
	case <-pc.closeCh:
		return nil, false
	}
}

func (pc *peekableChannelImpl[T]) peekWorker() {
	for {
		select {
		case req := <-pc.peekReqCh:
			pc.handlePeekRequest(req)
		case <-pc.closeCh:
			return
		}
	}
}

func (pc *peekableChannelImpl[T]) handlePeekRequest(req peekRequest[T]) {
	pc.mu.Lock()
	if pc.peeked != nil {
		item := pc.peeked
		pc.mu.Unlock()
		req.resultCh <- peekResult[T]{item: item, ok: true}
		return
	}
	pc.mu.Unlock()

	select {
	case item := <-pc.queue:
		pc.mu.Lock()
		pc.peeked = item
		pc.mu.Unlock()
		req.resultCh <- peekResult[T]{item: item, ok: true}

	case <-req.ctx.Done():
		req.resultCh <- peekResult[T]{item: nil, ok: false}

	case <-pc.closeCh:
		req.resultCh <- peekResult[T]{item: nil, ok: false}
	}
}

func (pc *peekableChannelImpl[T]) Close() {
	pc.closeOnce.Do(func() {
		pc.stateMu.Lock()
		pc.closed = true
		close(pc.closeCh)
		pc.stateMu.Unlock()
		pc.pushWg.Wait()
	})
}

func (pc *peekableChannelImpl[T]) registerPush() bool {
	pc.stateMu.Lock()
	defer pc.stateMu.Unlock()

	if pc.closed {
		return false
	}
	pc.pushWg.Add(1)
	return true
}
