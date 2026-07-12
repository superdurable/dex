package dex

import (
	"context"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
)

// pollForRunLoop is one goroutine in the PollForRun pool. Each
// iteration:
//
//  1. Acquires a run slot (blocks if RunConcurrency runs are already
//     active — back-pressure: while the slot is held nobody is polling
//     for new runs from this poller, so the server queues the task).
//  2. Long-polls MatchingService.PollForRun(ns, taskListName, workerID).
//  3. On receipt of a non-empty PollForRunResponse, spawns runMain (which
//     releases the slot on exit) and immediately loops back to step 1.
//  4. On empty long-poll (timeout, no task) or transient error, RELEASES
//     the slot and retries (slot was held for the duration of the poll
//     but no run is occupying it).
func (w *Worker) pollForRunLoop() {
	backoff := time.Duration(0)
	maxBackoff := w.opts.pollErrorMaxBackoff()
	signaledReady := false

	for {
		select {
		case <-w.rootCtx.Done():
			return
		default:
		}

		if backoff > 0 {
			select {
			case <-w.rootCtx.Done():
				return
			case <-time.After(backoff):
			}
		}

		// Step 1: acquire a run slot. Blocks when RunConcurrency runs
		// are already in flight — that's the upstream back-pressure
		// knob.
		if !w.acquireRunSlot() {
			return
		}

		// Signal readiness once we hold a slot and are about to long-poll —
		// the point at which a sync-match dispatch can rendezvous.
		if !signaledReady {
			signaledReady = true
			w.signalPollerReady()
		}

		// Step 2: long-poll for the next run dispatch.
		pr, err := w.pollForRunOnce()
		if err != nil {
			w.releaseRunSlot()
			// Exit only on actual shutdown. A per-poll DeadlineExceeded (the
			// long-poll WithTimeout child firing) is transient — reconnect,
			// don't kill the poller (there is no respawn).
			if w.rootCtx.Err() != nil {
				return
			}
			w.log.Warn("PollForRun error", "error", err)
			backoff = nextBackoff(backoff, maxBackoff)
			continue
		}
		backoff = 0
		if pr == nil {
			// Empty long-poll response: no run, no slot consumed.
			w.releaseRunSlot()
			continue
		}
		// Step 3: hand the slot to runMain — releaseRunSlot runs when
		// the run finishes (terminal completion / stop / shutdown).
		go func() {
			defer w.releaseRunSlot()
			w.runMain(pr)
		}()
	}
}

// pollForRunOnce issues a single PollForRun long-poll. Returns:
//   - (PollForRunResponse, nil) when the server dispatched a run.
//   - (nil, nil) when the long-poll timed out with no task.
//   - (nil, err) on transport / protocol error.
func (w *Worker) pollForRunOnce() (*pb.PollForRunResponse, error) {
	req := &pb.PollForRunRequest{
		Namespace:    w.namespace,
		TaskListName: w.taskListName,
		WorkerId:     w.workerID,
	}

	ctx, cancel := context.WithTimeout(w.rootCtx, w.opts.longPollRPCTimeout())
	defer cancel()
	resp, err := w.matchClient.PollForRun(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.RunId == "" {
		return nil, nil
	}
	return resp, nil
}

// pollForExternalEventsLoop is the single sticky-events poller goroutine.
// Long-polls MatchingService.PollForExternalEvents(ns, workerID); each
// returned ExternalEvent is routed by run_id to the per-run extChMsgInbox
// channel registered by runMain. Reconnects on disconnect with backoff.
func (w *Worker) pollForExternalEventsLoop() {
	backoff := time.Duration(0)
	maxBackoff := w.opts.pollErrorMaxBackoff()

	for {
		select {
		case <-w.rootCtx.Done():
			return
		default:
		}

		if backoff > 0 {
			select {
			case <-w.rootCtx.Done():
				return
			case <-time.After(backoff):
			}
		}

		err := w.pollForExternalEventsOnce()
		if err != nil {
			// Exit only on actual shutdown; a per-poll DeadlineExceeded is a
			// normal long-poll timeout — reconnect (no respawn otherwise).
			if w.rootCtx.Err() != nil {
				return
			}
			w.log.Debug("PollForExternalEvents disconnected; reconnecting", "error", err)
			backoff = nextBackoff(backoff, maxBackoff)
			continue
		}
		backoff = 0
	}
}

// pollForExternalEventsOnce issues one long-poll. Returns nil on
// long-poll timeout with no events (the loop will simply re-poll), or
// an error if the stream / RPC failed. Each received event is routed
// to the matching run's extChMsgInbox.
func (w *Worker) pollForExternalEventsOnce() error {
	req := &pb.PollForExternalEventsRequest{
		Namespace: w.namespace,
		WorkerId:  w.workerID,
	}

	ctx, cancel := context.WithTimeout(w.rootCtx, w.opts.longPollRPCTimeout())
	defer cancel()
	resp, err := w.matchClient.PollForExternalEvents(ctx, req)
	if err != nil {
		return err
	}
	for _, evt := range resp.GetEvents() {
		w.routeExternalEvent(evt)
	}
	return nil
}

// routeExternalEvent looks up the extChMsgInbox for evt.run_id and pushes the
// event onto it. If no extChMsgInbox is registered (run not started yet, or
// already exited), the event is dropped — the next WorkerCallResponse
// catch-up on heartbeat / step completion will reconcile.
func (w *Worker) routeExternalEvent(evt *pb.ExternalEvent) {
	if evt == nil || evt.RunId == "" {
		return
	}
	inbox, ok := w.runInboxes.Load(evt.RunId)
	if !ok {
		w.log.Debug("External event dropped: no extChMsgInbox", "runID", evt.RunId)
		return
	}
	ch, ok := inbox.(chan *pb.ExternalEvent)
	if !ok {
		return
	}
	select {
	case ch <- evt:
	default:
		// Inbox full; drop and rely on the next WorkerCallResponse
		// catch-up to reconcile. Correctness-safe but indicates
		// RunInboxBufferSize is undersized for this run's external-
		// event volume — raise it via WorkerOptions.RunInboxBufferSize.
		w.log.Error("External event dropped: extChMsgInbox full", "runID", evt.RunId)
	}
}

// nextBackoff implements the same exponential-backoff schedule used by
// every poller goroutine
func nextBackoff(prev, max time.Duration) time.Duration {
	if prev == 0 {
		return 100 * time.Millisecond
	}
	next := prev * 2
	if next > max {
		next = max
	}
	return next
}

// runMain is the per-run goroutine spawned by the PollForRun pool.
//
// Exits when:
//   - The run reaches a terminal stop_decision (COMPLETE / FAIL /
//     DEAD_END with no in-flight work).
//   - StopRequested is observed — drains in-flight step ctxs, returns.
//   - ctx is cancelled (worker shutdown) — best-effort ProcessReleaseRun(yield).
func (w *Worker) runMain(pr *pb.PollForRunResponse) {
	inbox := make(chan *pb.ExternalEvent, w.opts.runInboxBufferSize())
	w.runInboxes.Store(pr.RunId, inbox)
	defer w.runInboxes.Delete(pr.RunId)

	executor := newRunExecutor(w, pr, inbox)
	defer executor.timerMgr.Stop()
	defer executor.cancelInFlightStepTasks()
	err := executor.runMainLoop()

	// on shutdown, release the run so that another worker can pick it early
	if w.rootCtx.Err() != nil {
		w.releaseRunBestEffort(pr)
	}

	if err != nil {
		w.log.Warn("run processing failed",
			"runID", pr.RunId, "error", err)
	} else {
		w.log.Info("run processing completed",
			"runID", pr.RunId)
	}
}
