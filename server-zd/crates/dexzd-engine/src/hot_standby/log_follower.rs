// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::Arc;
use std::time::Duration;

use dexzd_log::{LogError, LogReadAheadConfig, StreamIdentity, StreamTailReader, StreamTailStep};
use dexzd_object_store::ObjectStore;
use dexzd_protocol::dexzd::v1::LogPosition;
use tokio::sync::watch;
use tokio::task::JoinHandle;
use tokio::time::Instant;
use tokio_util::sync::CancellationToken;

use crate::commit::{
    AppliedPositionAdvancer, AppliedPositionSource, ApplierPoolClosed, BatchEnqueueError,
    CommitState, EngineLocalStore, FollowedBatch, NodeStopReason, PartitionApplierPool,
};
use crate::recovery::{
    EngineStartError, NodeStreams, find_unreplayable_gap, lowest_applied_position,
};

/// Longest wait when a follower stops for its queued frames and final position advance.
const FOLLOWER_STOP_BUDGET: Duration = Duration::from_secs(3);

/// What a log follower reads: the recorded log segments of the followed node, oldest first.
#[derive(Debug, Clone)]
pub(crate) struct FollowSource {
    pub(crate) streams: NodeStreams,
}

/// How a log follower paces its reads.
#[derive(Debug, Clone, Copy)]
pub(crate) struct FollowPacing {
    /// First wait after a read finds no next batch; it doubles while none arrives.
    pub(crate) initial_poll_backoff: Duration,
    pub(crate) max_poll_backoff: Duration,
    /// Wait at one seq before checking whether log cleanup deleted it.
    pub(crate) stalled_seq_deletion_check_after: Duration,
    pub(crate) read_ahead: LogReadAheadConfig,
}

/// How far a log follower has read.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) struct FollowProgress {
    pub(crate) incarnation: u32,
    pub(crate) next_seq: u64,
    /// Seal time of the last accepted batch, in microseconds since the Unix epoch.
    pub(crate) last_accepted_sealed_at_micros: Option<i64>,
    /// Whether the last read at `next_seq` found no batch.
    pub(crate) is_caught_up: bool,
    /// When a read last found no next batch.
    pub(crate) caught_up_at: Option<Instant>,
}

/// Why a log follower stopped reading.
#[derive(Debug)]
pub(crate) enum FollowEnd {
    /// Log cleanup deleted batches the follower still needs; restart from a newer snapshot.
    LogDeleted,
    /// The follower cannot continue.
    Failed(EngineStartError),
    /// [`LogFollower::stop`] or a shutdown of the write fence stopped it.
    Stopped,
}

/// What a [`LogFollower`] runs with. The caller owns the store, the write fence, and the pool.
pub(crate) struct LogFollowerParts {
    pub(crate) object_store: Arc<dyn ObjectStore>,
    pub(crate) store: Arc<EngineLocalStore>,
    /// Stopping it blocks every later frame and position write of `appliers`.
    pub(crate) write_fence: Arc<CommitState>,
    /// Applies followed frames behind `write_fence`; dedicate it, since the follower advances all
    /// its partitions.
    pub(crate) appliers: Arc<PartitionApplierPool>,
    pub(crate) cluster_id: [u8; 16],
    pub(crate) source: FollowSource,
    pub(crate) pacing: FollowPacing,
}

/// Applies each accepted batch of a node's log in seq order, waiting at the first missing seq.
pub(crate) struct LogFollower {
    store: Arc<EngineLocalStore>,
    advancer: Arc<AppliedPositionAdvancer>,
    sources: watch::Sender<FollowSource>,
    progress: watch::Receiver<FollowProgress>,
    cancellation: CancellationToken,
    follow_task: Option<JoinHandle<FollowEnd>>,
    advance_task: Option<JoinHandle<()>>,
}

/// Where a follower continues: a stream, its next seq, and the epoch in effect before it.
#[derive(Debug, Clone, Copy)]
struct FollowCursor {
    incarnation: u32,
    next_seq: u64,
    effective_epoch: u64,
}

impl LogFollower {
    /// Starts following after the store's lowest applied position.
    ///
    /// Returns the gap error of [`find_unreplayable_gap`] when the source cannot update the
    /// store, and the store error of reading its applied positions.
    pub(crate) fn start(parts: LogFollowerParts) -> Result<Self, EngineStartError> {
        let LogFollowerParts {
            object_store,
            store,
            write_fence,
            appliers,
            cluster_id,
            source,
            pacing,
        } = parts;
        let partition_count = write_fence.partition_count();
        if let Some(gap) = find_unreplayable_gap(store.as_ref(), &source.streams, partition_count)?
        {
            return Err(gap);
        }
        let applied = lowest_applied_position(store.as_ref(), partition_count)?;
        let start =
            source.streams.resume_points(applied).next().expect(
                "find_unreplayable_gap requires a recorded segment at the applied position",
            );
        let cursor = FollowCursor {
            incarnation: start.segment.incarnation,
            next_seq: start.applied_seq.saturating_add(1),
            effective_epoch: start.applied_epoch,
        };
        let (followed_sender, followed) = watch::channel(None);
        let advancer = Arc::new(AppliedPositionAdvancer::new(
            appliers.clone(),
            AppliedPositionSource::FollowedBatches(followed),
            partition_count,
        ));
        let cancellation = CancellationToken::new();
        let advance_task = tokio::spawn(advancer.clone().run(cancellation.child_token()));
        let (progress_sender, progress) = watch::channel(FollowProgress {
            incarnation: cursor.incarnation,
            next_seq: cursor.next_seq,
            last_accepted_sealed_at_micros: None,
            is_caught_up: false,
            caught_up_at: None,
        });
        let node_id = source.streams.node_id.clone();
        let (sources, source_updates) = watch::channel(source);
        let follow_task = tokio::spawn(
            FollowTask {
                object_store,
                appliers,
                write_fence,
                cluster_id,
                node_id,
                pacing,
                sources: source_updates,
                followed: followed_sender,
                progress: progress_sender,
                cancellation: cancellation.child_token(),
            }
            .run(cursor),
        );
        Ok(Self {
            store,
            advancer,
            sources,
            progress,
            cancellation,
            follow_task: Some(follow_task),
            advance_task: Some(advance_task),
        })
    }

    /// Replaces the source with one read from a newer manifest.
    pub(crate) fn update_source(&self, source: FollowSource) {
        self.sources.send_replace(source);
    }

    /// Returns how far the follower has read.
    pub(crate) fn progress(&self) -> watch::Receiver<FollowProgress> {
        self.progress.clone()
    }

    /// Advances every running partition to the last queued batch, once its frames are applied.
    ///
    /// Returns [`ApplierPoolClosed`] when the applier lanes stopped first.
    pub(crate) async fn advance_applied_positions(&self) -> Result<(), ApplierPoolClosed> {
        self.advancer.advance_to_last_routed().await
    }

    /// Waits until the follower stops reading on its own. Cancel safe.
    ///
    /// Returns why it stopped, or [`FollowEnd::Stopped`] when it already ended.
    pub(crate) async fn wait_for_end(&mut self) -> FollowEnd {
        let Some(task) = self.follow_task.as_mut() else {
            return FollowEnd::Stopped;
        };
        let end = match task.await {
            Ok(end) => end,
            Err(error) => FollowEnd::Failed(EngineStartError::Stopped {
                reason: NodeStopReason::LocalStoreFailed {
                    detail: format!("the log follower task failed: {error}"),
                },
            }),
        };
        self.follow_task = None;
        end
    }

    /// Stops reading, waits for queued frames, and advances the applied positions to them.
    ///
    /// Returns the store. The caller still owns the write fence and the applier pool.
    pub(crate) async fn stop(mut self) -> Arc<EngineLocalStore> {
        self.cancellation.cancel();
        let stop_deadline = Instant::now() + FOLLOWER_STOP_BUDGET;
        if let Some(task) = self.follow_task.take() {
            match finish_stopped_task(task, stop_deadline).await {
                Some(FollowEnd::Failed(error)) => {
                    tracing::warn!(%error, "the log follower failed as it stopped");
                }
                Some(FollowEnd::LogDeleted | FollowEnd::Stopped) | None => {}
            }
        }
        if let Some(task) = self.advance_task.take() {
            finish_stopped_task(task, stop_deadline).await;
        }
        match tokio::time::timeout_at(stop_deadline, self.advancer.advance_to_last_routed()).await {
            Ok(Ok(())) => {}
            Ok(Err(error)) => {
                tracing::warn!(%error, "applied positions were not advanced as the follower stopped");
            }
            Err(_) => {
                tracing::warn!(
                    "applied positions were not advanced in time as the follower stopped"
                );
            }
        }
        self.store.clone()
    }
}

impl Drop for LogFollower {
    fn drop(&mut self) {
        self.cancellation.cancel();
        if let Some(task) = self.follow_task.take() {
            task.abort();
        }
        if let Some(task) = self.advance_task.take() {
            task.abort();
        }
    }
}

/// Waits until `deadline` for a cancelled task, then aborts it.
///
/// Returns its output, or `None` when it panicked, was aborted, or did not finish in time.
async fn finish_stopped_task<T>(task: JoinHandle<T>, deadline: Instant) -> Option<T> {
    let abort = task.abort_handle();
    match tokio::time::timeout_at(deadline, task).await {
        Ok(Ok(output)) => Some(output),
        Ok(Err(error)) => {
            if !error.is_cancelled() {
                tracing::error!(%error, "a log follower task failed while stopping");
            }
            None
        }
        Err(_) => {
            abort.abort();
            tracing::warn!("a log follower task did not stop in time and was aborted");
            None
        }
    }
}

/// A seq the follower waits at.
struct StalledSeq {
    seq: u64,
    /// When to check whether log cleanup deleted it.
    check_at: Instant,
    /// Whether a manifest read after the wait began still records it.
    is_recorded_by_newer_manifest: bool,
}

/// How reading one stream ended.
enum StreamEnd {
    /// Every seq through the stream's recorded last seq was read.
    Ended,
    /// The follower stops.
    Finished(FollowEnd),
}

/// The background task of a [`LogFollower`].
struct FollowTask {
    object_store: Arc<dyn ObjectStore>,
    appliers: Arc<PartitionApplierPool>,
    write_fence: Arc<CommitState>,
    cluster_id: [u8; 16],
    node_id: String,
    pacing: FollowPacing,
    sources: watch::Receiver<FollowSource>,
    followed: watch::Sender<Option<FollowedBatch>>,
    progress: watch::Sender<FollowProgress>,
    cancellation: CancellationToken,
}

impl FollowTask {
    /// Follows the recorded streams from `cursor`, moving to the next one as each closes.
    async fn run(mut self, mut cursor: FollowCursor) -> FollowEnd {
        loop {
            let stream = StreamIdentity {
                cluster_id: self.cluster_id,
                node_id: self.node_id.clone(),
                incarnation: cursor.incarnation,
            };
            match self.follow_stream(&stream, &mut cursor).await {
                StreamEnd::Finished(end) => return end,
                StreamEnd::Ended => {}
            }
            let next = self
                .sources
                .borrow()
                .streams
                .segment_after(cursor.incarnation);
            let Some(next) = next else {
                return FollowEnd::Failed(EngineStartError::InvalidMetadata {
                    detail: format!(
                        "the log stream of incarnation {} ended, but the manifest records no newer \
                         stream",
                        cursor.incarnation
                    ),
                });
            };
            if next.start_seq > 1 {
                return FollowEnd::LogDeleted;
            }
            cursor = FollowCursor {
                incarnation: next.incarnation,
                next_seq: 1,
                effective_epoch: 0,
            };
            self.progress.send_modify(|progress| {
                progress.incarnation = cursor.incarnation;
                progress.next_seq = cursor.next_seq;
                progress.is_caught_up = false;
            });
        }
    }

    /// Follows one stream until it ends or the follower stops; `cursor` tracks every read seq.
    async fn follow_stream(
        &mut self,
        stream: &StreamIdentity,
        cursor: &mut FollowCursor,
    ) -> StreamEnd {
        let object_store = self.object_store.clone();
        let mut last_seq = match self
            .sources
            .borrow_and_update()
            .streams
            .segment(cursor.incarnation)
        {
            Some(segment) if cursor.next_seq < segment.start_seq => {
                return StreamEnd::Finished(FollowEnd::LogDeleted);
            }
            Some(segment) => segment.end_seq,
            None => return StreamEnd::Finished(FollowEnd::LogDeleted),
        };
        let mut tail = match StreamTailReader::new(
            object_store.as_ref(),
            stream,
            cursor.next_seq,
            cursor.effective_epoch,
            last_seq,
            self.pacing.read_ahead,
        ) {
            Ok(tail) => tail,
            Err(error) => return StreamEnd::Finished(FollowEnd::Failed(error.into())),
        };
        let mut backoff = self.pacing.initial_poll_backoff;
        let mut stalled: Option<StalledSeq> = None;
        loop {
            if self.sources.has_changed().unwrap_or(false) {
                if let Some(end) =
                    self.apply_source_update(&mut tail, cursor.incarnation, &mut last_seq)
                {
                    return StreamEnd::Finished(end);
                }
                if let Some(stalled) = &mut stalled {
                    stalled.is_recorded_by_newer_manifest = true;
                }
            }
            let step = tokio::select! {
                biased;
                () = self.cancellation.cancelled() => return StreamEnd::Finished(FollowEnd::Stopped),
                reason = self.write_fence.wait_stopped() => {
                    return StreamEnd::Finished(end_after_fence_stop(reason));
                }
                step = tail.next_step() => step,
            };
            match step {
                Ok(StreamTailStep::Accepted(batch)) => {
                    let seq = batch.header.seq;
                    let sealed_at_micros = batch.header.sealed_at_unix_micros;
                    let enqueued = tail
                        .read_ahead_while(self.appliers.enqueue_batch(stream, batch))
                        .await;
                    if let Err(error) = enqueued {
                        return StreamEnd::Finished(self.end_after_enqueue_failure(error));
                    }
                    self.followed.send_replace(Some(FollowedBatch {
                        position: LogPosition {
                            node_id: stream.node_id.clone(),
                            incarnation: stream.incarnation,
                            seq,
                        },
                        effective_epoch: tail.effective_epoch(),
                    }));
                    self.record_read(&tail, cursor, Some(sealed_at_micros), false);
                    backoff = self.pacing.initial_poll_backoff;
                    stalled = None;
                }
                Ok(StreamTailStep::Voided(_)) => {
                    self.record_read(&tail, cursor, None, false);
                    stalled = None;
                }
                Ok(StreamTailStep::WaitingAt(seq)) => {
                    self.record_read(&tail, cursor, None, true);
                    let now = Instant::now();
                    let stalled_seq = match &mut stalled {
                        Some(stalled_seq) if stalled_seq.seq == seq => stalled_seq,
                        _ => stalled.insert(StalledSeq {
                            seq,
                            check_at: now + self.pacing.stalled_seq_deletion_check_after,
                            is_recorded_by_newer_manifest: false,
                        }),
                    };
                    if now >= stalled_seq.check_at {
                        let checked = tail.ensure_waiting_seq_was_not_deleted().await;
                        if let Some(end) = end_after_stalled_seq_check(
                            checked,
                            stream.incarnation,
                            stalled_seq,
                            last_seq,
                        ) {
                            return StreamEnd::Finished(end);
                        }
                        stalled_seq.check_at = now + self.pacing.stalled_seq_deletion_check_after;
                    }
                    if let Some(end) = self.wait_before_next_read(backoff).await {
                        return StreamEnd::Finished(end);
                    }
                    backoff = (backoff * 2).min(self.pacing.max_poll_backoff);
                }
                Ok(StreamTailStep::Ended) => return StreamEnd::Ended,
                Err(LogError::Store(error)) => {
                    tracing::warn!(
                        %error,
                        incarnation = stream.incarnation,
                        seq = tail.next_seq(),
                        "reading the followed log failed; retrying"
                    );
                    self.progress
                        .send_modify(|progress| progress.is_caught_up = false);
                    if let Some(end) = self.wait_before_next_read(backoff).await {
                        return StreamEnd::Finished(end);
                    }
                    backoff = (backoff * 2).min(self.pacing.max_poll_backoff);
                }
                Err(error) => return StreamEnd::Finished(FollowEnd::Failed(error.into())),
                Ok(step) => {
                    return StreamEnd::Finished(FollowEnd::Failed(
                        LogError::Invalid(format!("log following cannot handle step {step:?}"))
                            .into(),
                    ));
                }
            }
        }
    }

    /// Applies a newer source to the stream being read.
    ///
    /// Returns the end of following when the source no longer retains the next seq.
    fn apply_source_update(
        &mut self,
        tail: &mut StreamTailReader<'_>,
        incarnation: u32,
        last_seq: &mut Option<u64>,
    ) -> Option<FollowEnd> {
        let segment = self
            .sources
            .borrow_and_update()
            .streams
            .segment(incarnation);
        let Some(segment) = segment else {
            return Some(FollowEnd::LogDeleted);
        };
        if tail.next_seq() < segment.start_seq {
            return Some(FollowEnd::LogDeleted);
        }
        if let Some(end_seq) = segment.end_seq
            && last_seq.is_none()
        {
            if let Err(error) = tail.end_at(end_seq) {
                return Some(FollowEnd::Failed(error.into()));
            }
            *last_seq = Some(end_seq);
        }
        None
    }

    /// Sleeps `backoff`, waking early for a newer source.
    ///
    /// Returns the end of following when the follower stops meanwhile.
    async fn wait_before_next_read(&self, backoff: Duration) -> Option<FollowEnd> {
        // A clone waits for the update, so the next read still sees it as unseen.
        let mut source_updates = self.sources.clone();
        tokio::select! {
            biased;
            () = self.cancellation.cancelled() => Some(FollowEnd::Stopped),
            reason = self.write_fence.wait_stopped() => Some(end_after_fence_stop(reason)),
            changed = source_updates.changed() => match changed {
                Ok(()) => None,
                // The follower dropped its sender, so it is stopping.
                Err(_) => Some(FollowEnd::Stopped),
            },
            () = tokio::time::sleep(backoff) => None,
        }
    }

    fn record_read(
        &self,
        tail: &StreamTailReader<'_>,
        cursor: &mut FollowCursor,
        sealed_at_micros: Option<i64>,
        is_caught_up: bool,
    ) {
        cursor.next_seq = tail.next_seq();
        cursor.effective_epoch = tail.effective_epoch();
        self.progress.send_modify(|progress| {
            progress.next_seq = cursor.next_seq;
            progress.is_caught_up = is_caught_up;
            if is_caught_up {
                progress.caught_up_at = Some(Instant::now());
            }
            if sealed_at_micros.is_some() {
                progress.last_accepted_sealed_at_micros = sealed_at_micros;
            }
        });
    }

    fn end_after_enqueue_failure(&self, error: BatchEnqueueError) -> FollowEnd {
        match error {
            BatchEnqueueError::UnknownPartition { .. } => {
                FollowEnd::Failed(EngineStartError::InvalidMetadata {
                    detail: error.to_string(),
                })
            }
            BatchEnqueueError::PoolClosed(_) => match self.write_fence.stop_reason() {
                Some(reason) => end_after_fence_stop(reason),
                None => FollowEnd::Stopped,
            },
        }
    }
}

/// Classifies the deletion check of a seq the follower has waited at for a while.
///
/// Returns [`FollowEnd::LogDeleted`] when log cleanup deleted it, and a failure when a closed
/// stream lacks it although a newer manifest still records it. Returns `None` otherwise, since
/// the check can miss an unordered deletion that the next manifest reveals.
fn end_after_stalled_seq_check(
    checked: Result<(), LogError>,
    incarnation: u32,
    stalled: &StalledSeq,
    last_seq: Option<u64>,
) -> Option<FollowEnd> {
    let seq = stalled.seq;
    match checked {
        Err(LogError::MissingBatches { .. }) => Some(FollowEnd::LogDeleted),
        Err(LogError::Store(error)) => {
            tracing::warn!(%error, seq, "checking a stalled log seq failed; retrying later");
            None
        }
        Err(error) => Some(FollowEnd::Failed(error.into())),
        Ok(()) if last_seq.is_some() && stalled.is_recorded_by_newer_manifest => {
            Some(FollowEnd::Failed(
                LogError::Corrupt(format!(
                    "closed log stream of incarnation {incarnation} has no batch at seq {seq}"
                ))
                .into(),
            ))
        }
        Ok(()) => None,
    }
}

/// Returns how following ends once its write fence stopped for `reason`.
fn end_after_fence_stop(reason: NodeStopReason) -> FollowEnd {
    match reason {
        NodeStopReason::Shutdown => FollowEnd::Stopped,
        reason => FollowEnd::Failed(EngineStartError::Stopped { reason }),
    }
}
