package dex

import (
	"context"
	"sync"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"google.golang.org/grpc"
)

// Worker polls for runs, dispatches to registered steps, and reports
// results back to the server using the new tasklist + long-poll protocol
type Worker struct {
	registry     *Registry
	matchClient  pb.MatchingServiceClient
	runsClient   pb.RunsServiceClient
	namespace    string
	taskListName string
	workerID     string
	opts         WorkerOptions
	log          Logger

	rootCtx       context.Context
	rootCtxCancel context.CancelFunc

	// ready is closed once the first run-poller has entered its long-poll,
	// i.e. a sync-match dispatch can now rendezvous. Callers wait on Ready()
	// before StartRun to avoid the slow async-backlog dispatch path.
	ready     chan struct{}
	readyOnce sync.Once

	// runSlots is a counting semaphore implemented as a buffered
	// channel of size RunConcurrency.
	runSlots chan struct{}

	// runInboxes maps run_id -> per-run extChMsgInbox channel. Populated by
	// runMain on entry, removed on exit. The sticky external-events
	// poller routes events to the right extChMsgInbox
	// map[string]chan *pb.ExternalEvent, key is run_id
	runInboxes sync.Map
}

// NewWorker creates a Worker
func NewWorker(
	registry *Registry,
	matchConn, runsConn grpc.ClientConnInterface,
	namespace string,
	opts WorkerOptions,
) *Worker {
	hostID := opts.hostID()

	rootCtx, rootCtxCancel := context.WithCancel(context.Background())
	return &Worker{
		registry:      registry,
		matchClient:   pb.NewMatchingServiceClient(matchConn),
		runsClient:    pb.NewRunsServiceClient(runsConn),
		namespace:     namespace,
		taskListName:  opts.taskListName(),
		workerID:      generateWorkerID(hostID),
		opts:          opts,
		log:           opts.logger(),
		rootCtx:       rootCtx,
		rootCtxCancel: rootCtxCancel,
		ready:         make(chan struct{}),

		runSlots: make(chan struct{}, opts.runConcurrency()),
	}
}

// WorkerID returns the stable workerID generated for this Worker.
func (w *Worker) WorkerID() string { return w.workerID }

// Ready returns a channel closed once the first run-poller has entered its
// long-poll. After it closes, a StartRun dispatch can sync-match instead of
// falling into the slower async-backlog path.
func (w *Worker) Ready() <-chan struct{} { return w.ready }

// Start launches worker goroutines (PollForRun pool + PollForExternalEvents
// loop) and blocks until Stop() is called.
func (w *Worker) Start() error {
	w.log.Info("worker starting",
		"namespace", w.namespace,
		"taskListName", w.taskListName,
		"workerID", w.workerID,
		"runConcurrency", w.opts.runConcurrency(),
		"concurrentRunPollers", w.opts.concurrentRunPollers(),
		"concurrentExternalEventPollers", w.opts.concurrentExternalEventPollers())

	var wg sync.WaitGroup

	// PollForRun pool: ConcurrentRunPollers goroutines, each gated on
	// the runSlots semaphore (size RunConcurrency) before polling.
	for i := 0; i < w.opts.concurrentRunPollers(); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.pollForRunLoop()
		}()
	}

	// Sticky PollForExternalEvents pool.
	for i := 0; i < w.opts.concurrentExternalEventPollers(); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.pollForExternalEventsLoop()
		}()
	}

	wg.Wait()
	return w.rootCtx.Err()
}

// acquireRunSlot blocks until a RunConcurrency slot is available or
// ctx is cancelled. Returns true on acquisition, false on cancel.
// Caller must releaseRunSlot exactly once after a successful acquire.
func (w *Worker) acquireRunSlot() bool {
	select {
	case <-w.rootCtx.Done():
		return false
	case w.runSlots <- struct{}{}:
		return true
	}
}

// releaseRunSlot frees one slot acquired via acquireRunSlot. Safe to
// call from any goroutine but conventionally called by runMain on exit.
func (w *Worker) releaseRunSlot() {
	<-w.runSlots
}

// signalPollerReady closes the ready channel the first time any run-poller is
// about to long-poll. Idempotent — later pollers are no-ops.
func (w *Worker) signalPollerReady() {
	w.readyOnce.Do(func() { close(w.ready) })
}

// Stop signals the worker to shut down gracefully. In-flight runs receive
// a context cancel and runMain calls best-effort ProcessReleaseRun before
func (w *Worker) Stop() {
	w.rootCtxCancel()
}
