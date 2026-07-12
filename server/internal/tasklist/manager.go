package tasklist

import (
	"context"
	"sync"
	"sync/atomic"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/log/tag"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/cluster"
	"github.com/superdurable/dex/server/internal/metrics"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/superdurable/dex/server/internal/routing"
)

// Manager is the per-partition tasklist coordinator. It composes:
//   - tasklistDB: cached rangeID + ackLevel + DB calls
//   - taskWriter: single-goroutine batched INSERTs
//   - taskReader: pushes DB-fetched tasks into matcher's bufferedCh
//   - pendingSet: BTree-backed watermark + GC
//   - matcher: owns syncOfferCh + bufferedCh; single Poll select
//   - forwarder (non-root only): delegate sync match to root partition
type Manager struct {
	id              *Identifier
	cfg             config.MatchingServiceConfig
	logger          log.Logger
	store           p.TasklistStore
	localRunsClient pb.RunsServiceClient
	memberID        string
	matchingAddress string

	db         *tasklistDB
	writer     *taskWriter
	reader     *taskReader
	pendingSet *pendingSet
	matcher    *matcher
	forwarder  *forwarder

	startOnce sync.Once
	stopOnce  sync.Once
	started   atomic.Bool
	stopped   atomic.Bool

	// startDone is closed exactly once when Start finishes (success or
	// failure) or when Stop runs before Start. A DispatchRun that races a
	// concurrent poller's in-flight Start waits on it instead of failing.
	startDone     chan struct{}
	startDoneOnce sync.Once
}

// ManagerDeps groups external dependencies for testability.
type ManagerDeps struct {
	Store           p.TasklistStore
	RunsClient      pb.RunsServiceClient  // local runs loopback; for ProcessAsyncMatch in the async-pickup path
	Membership      *cluster.Membership   // for forwarder direct-routing to the root partition owner
	RemoteClient    *routing.RemoteClient // for forwarder direct-routing to the root partition owner
	MemberID        string                // this matching node's ID
	MatchingAddress string                // this matching node's gRPC address
	Logger          log.Logger
	Config          config.MatchingServiceConfig
}

func NewManager(id *Identifier, deps ManagerDeps) *Manager {
	logger := deps.Logger.WithTags(
		tag.Namespace(id.Namespace()),
		tag.TaskListName(id.FullName()),
	)

	fwd := newForwarder(id, deps.Config, logger, deps.Membership, deps.RemoteClient)

	bufferSize := deps.Config.TaskBufferSize
	if bufferSize < 1 {
		bufferSize = 100
	}

	mch := newMatcher(id, fwd, logger, bufferSize)

	return &Manager{
		id:              id,
		cfg:             deps.Config,
		logger:          logger,
		store:           deps.Store,
		localRunsClient: deps.RunsClient,
		memberID:        deps.MemberID,
		matchingAddress: deps.MatchingAddress,
		matcher:         mch,
		forwarder:       fwd,
		startDone:       make(chan struct{}),
	}
}

// signalStartDone unblocks any DispatchRun waiting for Start to finish.
func (m *Manager) signalStartDone() {
	m.startDoneOnce.Do(func() { close(m.startDone) })
}

func (m *Manager) Start(ctx context.Context) errors.CategorizedError {
	var startErr errors.CategorizedError
	m.startOnce.Do(func() {
		defer m.signalStartDone()
		// Claim is lifecycle work — not bound to the caller's poll budget.
		claimCtx, cancel := context.WithTimeout(context.Background(), m.cfg.OperationTimeout)
		defer cancel()
		md, claimErr := m.store.ClaimTasklist(claimCtx, m.id.Namespace(), m.id.BaseName(), m.id.Partition(), m.memberID, m.matchingAddress)
		if claimErr != nil {
			m.logger.Error("Tasklist claim failed", tag.Error(claimErr))
			startErr = claimErr
			return
		}
		m.logger.Info("Tasklist claimed",
			tag.RangeID(md.RangeID),
			tag.AckLevel(md.AckLevel))

		m.db = newTasklistDB(m.store, m.id, md.RangeID, md.AckLevel)
		m.pendingSet = newPendingSet(m.id, m.db, m.cfg, m.logger)
		m.writer = newTaskWriter(m.id, m.db, m.cfg, m.logger, m.onFatalErr)
		m.reader = newTaskReader(m.id, m.db, m.writer, m.pendingSet, m.cfg, m.logger, m.matcher.BufferCh(), m.onFatalErr)

		if writerErr := m.writer.Start(ctx); writerErr != nil {
			startErr = writerErr
			return
		}
		m.reader.Start(ctx)
		m.started.Store(true)
	})
	return startErr
}

func (m *Manager) Stop() {
	m.stopOnce.Do(func() {
		m.stopped.Store(true)

		if !m.started.Load() {
			m.signalStartDone()
			m.forwarder.Stop()
			m.logger.Warn("Tasklist manager stopped (never started)")
			return
		}

		// Stop writer first so no new tasks are written.
		m.writer.Stop()
		// Stop reader so no new tasks are buffered.
		m.reader.Stop()
		// Stop forwarder.
		m.forwarder.Stop()
		// Final ackLevel persist (best-effort — fence may have failed).
		if m.pendingSet != nil && m.db != nil {
			wm := m.pendingSet.Watermark()
			if wm > m.db.AckLevel() {
				ctx, cancel := context.WithTimeout(context.Background(), m.cfg.OperationTimeout)
				if err := m.db.UpdateAckLevel(ctx, wm); err != nil {
					m.logger.Error("Final ack persist failed", tag.Error(err))
				}
				cancel()
			}
		}
		// Shutdown drain: delete completedAbove taskIDs.
		if m.pendingSet != nil {
			ctx, cancel := context.WithTimeout(context.Background(), m.cfg.OperationTimeout*3)
			m.pendingSet.ShutdownDrain(ctx)
			cancel()
		}
		m.logger.Info("Tasklist manager stopped")
	})
}

// Stopped reports whether Stop has been called.
func (m *Manager) Stopped() bool { return m.stopped.Load() }

// onFatalErr is invoked by the writer or reader on a fence-violation
// Conflict error.
func (m *Manager) onFatalErr(err errors.CategorizedError) {
	m.logger.Warn("Fatal store error, self-evicting",
		tag.Error(err), tag.RangeID(m.db.RangeID()))
	go m.Stop()
}

// DispatchRun handles a DispatchRun for a partition this node owns.
// Manager owns the full 3-message sync-match handshake
//
//	Four cases:
//	1. local sync match (TryLocalSyncMatch, no DB write) → completeSyncMatchDispatch;
//	2. miss + forwarded (this Manager is root serving a fan-in) → reply
//	   sync_matched=false WITHOUT writing; the originating non-root
//	   partition writes the async fallback;
//	3. miss + non-root → forwarder.ForwardTask relays to root; if root
//	   matched we're done, else write locally;
//	4. miss + root → write locally.
//
// Exactly one DispatchRunResponse reaches upstream.
func (m *Manager) DispatchRun(ctx context.Context, req *pb.DispatchRunRequest, upstream pb.MatchingService_DispatchRunServer) errors.CategorizedError {
	if m.stopped.Load() {
		return errors.NewUnavailableError("tasklist manager stopped", nil)
	}
	// started flips true only after Start() builds m.writer; a cache-hit
	// caller can reach here first — guard the nil writer.
	if !m.started.Load() {
		// A concurrent poller may be mid-Start (GetOrCreateManager hands out
		// the manager before Start finishes). Wait for it rather than failing
		// the dispatch into a slow retry.
		select {
		case <-m.startDone:
		case <-ctx.Done():
		}
		if !m.started.Load() {
			return errors.NewUnavailableError("tasklist manager not started yet", nil)
		}
	}
	m.logger.Debug("Manager.DispatchRun", tag.RunID(req.RunId), tag.Shard(req.ShardId), tag.TaskListName(m.id.FullName()))

	matched, syncTask := m.TryLocalSyncMatch(req.RunId, req.ShardId)
	if matched {
		return m.completeSyncMatchDispatch(ctx, syncTask, upstream)
	}

	// Forwarded request served at root: sync-match-only, never write.
	if req.ForwardedFromPartition != "" {
		return sendDispatchResponse(upstream, false, "")
	}

	// Non-root local miss: relay to root before local DB fallback.
	if !m.id.IsRoot() {
		forwarded, fwdErr := m.forwarder.ForwardTask(ctx, req, upstream)
		if forwarded {
			if fwdErr != nil {
				// returning fwdErr here instead of fallback, because at this point, the task is acked by
				// taskProcessor as started
				return fwdErr
			}
			return nil
		}
		if fwdErr != nil {
			m.logger.Warn("Forward to root failed, falling back to local",
				tag.RunID(req.RunId), tag.TaskListName(m.id.FullName()), tag.Error(fwdErr))
		}
	}

	// fall back to write locally
	if err := m.WriteTask(ctx, req.RunId, req.ShardId); err != nil {
		return err
	}
	m.logger.Debug("Manager.DispatchRun sync-match miss, wrote to DB",
		tag.RunID(req.RunId), tag.TaskListName(m.id.FullName()))
	metrics.CounterDispatchLocalWrite.Inc(metrics.TagPartitionRole(m.id.IsRoot()))
	return sendDispatchResponse(upstream, false, "")
}

// TryLocalSyncMatch attempts a non-blocking LOCAL sync match (no DB write).
// Returns (true, task) if matched
// (false, nil) otherwise.
func (m *Manager) TryLocalSyncMatch(runID string, shardID int32) (bool, *Task) {
	syncTask := newSyncMatchTask(runID, m.id.Namespace(), shardID)
	if m.matcher.TryLocalOffer(syncTask) {
		m.logger.Debug("Manager local sync-match", tag.RunID(runID), tag.Shard(shardID))
		return true, syncTask
	}
	return false, nil
}

// WriteTask persists the dispatch task to this partition's DB (async
// pickup path) and signals the reader to fetch it promptly.
func (m *Manager) WriteTask(ctx context.Context, runID string, shardID int32) errors.CategorizedError {
	taskID, writeErr := m.writer.AppendTask(ctx, runID, shardID)
	if writeErr != nil {
		return writeErr
	}
	m.logger.Debugf("Manager.WriteTask: async-wrote task task_id=%d run_id=%s", taskID, runID)
	m.reader.Signal()
	return nil
}

// completeSyncMatchDispatch finishes a local sync match: read the poller's
// workerID, send Response{matched, workerID} to upstream, receive the
// caller's msg3 PollForRunResponse, and hand it to the poller via
// pollRespDeliveryCh.
func (m *Manager) completeSyncMatchDispatch(ctx context.Context, syncTask *Task, upstream pb.MatchingService_DispatchRunServer) errors.CategorizedError {
	var workerID string
	select {
	case workerID = <-syncTask.WorkerIDCh():
	case <-ctx.Done():
		return errors.NewTimeoutError("timeout at completeSyncMatchDispatch", ctx.Err())
	}
	if err := sendDispatchResponse(upstream, true, workerID); err != nil {
		close(syncTask.PollDeliveryCh())
		return err
	}
	msg3, err := upstream.Recv()
	if err != nil {
		close(syncTask.PollDeliveryCh())
		return errors.NewInternalError("failed to recv PollForRunResponse", err)
	}
	pollResp := msg3.GetPollForRunResponse()
	if pollResp == nil || pollResp.RunId == "" {
		close(syncTask.PollDeliveryCh())
		return errors.NewInternalError("expected PollForRunResponse on msg3", nil)
	}
	syncTask.PollDeliveryCh() <- pollResp
	return nil
}

// PollForRun serves a worker poll for a partition this node owns.
func (m *Manager) PollForRun(ctx context.Context, workerID string, nonBlockingPolling bool) (*pb.PollForRunResponse, errors.CategorizedError) {
	if m.stopped.Load() {
		return nil, errors.NewUnavailableError("tasklist manager stopped", nil)
	}

	var task *Task
	if nonBlockingPolling {
		var ok bool
		task, ok = m.matcher.TryLocalPoll()
		if !ok {
			return &pb.PollForRunResponse{}, nil
		}
	} else {
		var pollErr errors.CategorizedError
		task, pollErr = m.matcher.FullPoll(ctx, workerID)
		if pollErr != nil {
			return nil, pollErr
		}
	}
	// FullPoll returns (nil, nil) on long-poll timeout; reply empty.
	if task == nil {
		return &pb.PollForRunResponse{}, nil
	}
	m.logger.Debug("Manager.PollForRun picked", tag.RunID(task.RunID()), tag.Namespace(task.Namespace()), tag.TaskListName(m.id.FullName()))
	return m.deliverTaskToPoller(ctx, task, workerID)
}

// deliverTaskToPoller turns a matched Task into the worker-facing PollForRunResponse.
//   - sync match (taskID == 0): publish workerID to the offerer (no-op
//     for a forwarded task whose pollRespDeliveryCh is pre-populated and whose
//     workerIDCh is nil), then wait on pollRespDeliveryCh;
//   - async pickup (taskID > 0): ProcessAsyncMatch to transition the run
//     to Running, ack pendingSet on success / push back on transient
//     failure.
func (m *Manager) deliverTaskToPoller(ctx context.Context, task *Task, workerID string) (*pb.PollForRunResponse, errors.CategorizedError) {
	if task.IsSyncMatch() {
		if ch := task.WorkerIDCh(); ch != nil {
			select {
			case ch <- workerID:
			default:
			}
		}
		select {
		case resp := <-task.PollDeliveryCh():
			return resp, nil
		case <-ctx.Done():
			return &pb.PollForRunResponse{}, nil
		}
	}

	// Async pickup: build PollForRunResponse via the local runs service.
	resp, callErr := m.localRunsClient.ProcessAsyncMatch(ctx, &pb.ProcessAsyncMatchRequest{
		Namespace: task.Namespace(),
		RunId:     task.RunID(),
		WorkerId:  workerID,
	})
	if callErr != nil {
		// Transient → push the task back; pendingSet stays unacked so the
		// watermark won't advance. Worker retries.
		m.logger.Warn("ProcessAsyncMatch failed, pushing task back",
			tag.Namespace(task.Namespace()), tag.RunID(task.RunID()), tag.Error(callErr))
		m.reader.PushBack(task)
		return nil, errors.NewInternalError("process async match task failed", callErr)
	}
	m.pendingSet.Ack(task.TaskID())
	if resp.Outcome == pb.AsyncMatchOutcome_ASYNC_MATCH_OUTCOME_SUCCESS {
		return resp.GetPollForRunResponse(), nil
	}
	// STALE_SUCCESS / unspecified — run already terminal or taken; the
	// task is done. Return empty so the worker polls again.
	return &pb.PollForRunResponse{}, nil
}

// sendDispatchResponse sends the single DispatchRunResponse to the
// upstream caller.
func sendDispatchResponse(upstream pb.MatchingService_DispatchRunServer, matched bool, workerID string) errors.CategorizedError {
	err := upstream.Send(&pb.MatchingToEngineDispatchMessage{
		Message: &pb.MatchingToEngineDispatchMessage_Response{
			Response: &pb.DispatchRunResponse{SyncMatched: matched, WorkerId: workerID},
		},
	})
	if err != nil {
		return errors.NewInternalError("failed to send dispatch Response to runService", err)
	}
	return nil
}
