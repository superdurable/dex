// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The start of an engine node: from the lease, through recovery, to serving.

use std::sync::Arc;
use std::sync::atomic::AtomicU64;
use std::time::{Duration, Instant};

use dexzd_cluster_metadata::{LeaseObjectStores, LeaseObservers, LeaseReleaseError, LeaseStatus};
use dexzd_keyspace::PartitionId;
use dexzd_log::{CommitPipeline, StreamIdentity};
use dexzd_object_store::ObjectStore;
use dexzd_storage::LocalReadView;
use tokio::sync::{mpsc, watch};
use tokio_util::sync::CancellationToken;

use crate::commit::{
    AppliedPositionAdvancer, AppliedPositionSource, CommandCommitter, CommandCommitterParts,
    CommitProgress, CommitState, CommittedBatchRouter, EngineLocalStore, FrameApplier,
    FrameApplierParts, LEASE_VALIDITY_EXPIRED_DETAIL, NodeStopReason, PartitionApplierPool,
    run_log_proposer,
};
use crate::engine_node::{
    EngineNode, EngineNodeConfig, EngineNodeDependencies, LeasedParts, RunningTasks,
    validate_config,
};
use crate::hot_standby::HotStandby;
use crate::recovery::{
    ClusterMetadata, EngineCommandAndEffectHooks, EngineServices, EngineStartError,
    LocalStoreRequirements, LogReplay, OpenedLocalStore, StartupReadRetryStore,
    delete_older_set_aside_stores, open_local_store, rebuild_search_order_indexes,
    reusable_open_store,
};
use crate::snapshots::{
    LogCleanup, OwnershipFence, SnapshotCatalog, SnapshotRetention, SnapshotService,
};

/// Where a starting node gets its local store.
pub(crate) enum LocalStoreSource {
    /// Opens or restores the store that the node configuration names.
    Configured,
    /// Takes over a hot standby's store when the retained log can still update it.
    HotStandby(HotStandby),
}

/// Longest wait for a hot standby's store to close before the start reopens its directory.
const STORE_CLOSE_BUDGET: Duration = Duration::from_secs(30);

/// How a start ended, apart from errors after the lease.
pub(crate) enum StartOutcome {
    Serving(EngineNode),
    /// Cancellation arrived before the lease; the local store source is returned unused.
    Cancelled(LocalStoreSource),
    /// The start failed before the lease; the local store source is returned unused.
    FailedBeforeLease {
        error: EngineStartError,
        source: LocalStoreSource,
    },
}

impl LocalStoreSource {
    pub(crate) fn into_hot_standby(self) -> Option<HotStandby> {
        match self {
            Self::HotStandby(standby) => Some(standby),
            Self::Configured => None,
        }
    }
}

/// Opens the store that the node serves from, once this process holds the lease.
///
/// Returns the hot standby's store and `true` when the standby followed this node into the
/// configured store and the retained log can update it. Otherwise returns the store that
/// [`open_local_store`] opens or restores, and `false`.
async fn open_serving_store(
    standby: Option<&mut HotStandby>,
    config: &EngineNodeConfig,
    object_store: &dyn ObjectStore,
    requirements: &LocalStoreRequirements<'_>,
) -> Result<(OpenedLocalStore, bool), EngineStartError> {
    if let Some(standby) = standby
        && let Some(store) = take_over_store(standby, config, requirements).await?
    {
        let opened = OpenedLocalStore {
            store,
            set_aside_id: None,
        };
        return Ok((opened, true));
    }
    let opened = open_local_store(&config.local_store, object_store, requirements).await?;
    Ok((opened, false))
}

/// Stops the standby, fences and joins its work, and checks its store under the lease.
///
/// Returns its store when it followed this node into the configured store and the retained log
/// can update it. Returns `None` otherwise, once the store closed so its directory reopens.
async fn take_over_store(
    standby: &mut HotStandby,
    config: &EngineNodeConfig,
    requirements: &LocalStoreRequirements<'_>,
) -> Result<Option<Arc<EngineLocalStore>>, EngineStartError> {
    let is_configured_store = standby.has_local_store(&config.local_store);
    // The gap check also rejects a store that applied a batch past a closed stream's end.
    let reusable = match standby.hand_over_store().await {
        None => {
            tracing::info!("the hot standby had no local store to hand over");
            None
        }
        Some(_) if !is_configured_store => {
            tracing::error!(
                "the hot standby followed into another store than this node's; opening the \
                 node's local store"
            );
            None
        }
        Some(store) => reusable_open_store(&store, requirements)?,
    };
    if reusable.is_some() {
        tracing::info!("serving from the hot standby's local store");
    } else if is_configured_store
        && config.local_store.has_directory()
        && !standby.wait_until_store_closed(STORE_CLOSE_BUDGET).await
    {
        tracing::error!("the hot standby's local store is still open; opening it may fail");
    }
    Ok(reusable)
}

/// Checks the node configuration and that a hot standby follows the node that starts.
fn validate_start_config(
    config: &EngineNodeConfig,
    local_store_source: &LocalStoreSource,
) -> Result<(), EngineStartError> {
    validate_config(config)?;
    match local_store_source {
        LocalStoreSource::HotStandby(standby)
            if !standby.is_following_node(&config.node.node_id) =>
        {
            Err(EngineStartError::InvalidConfig {
                detail: format!(
                    "the hot standby does not follow node {}",
                    config.node.node_id
                ),
            })
        }
        LocalStoreSource::HotStandby(_) | LocalStoreSource::Configured => Ok(()),
    }
}

/// Starts a node like [`EngineNode::start_until_cancelled`], with its local store from `local_store_source`.
///
/// Returns the serving node, or the unused source with [`StartOutcome::Cancelled`] or
/// [`StartOutcome::FailedBeforeLease`] when cancellation or an error arrives before the lease.
/// After the lease, returns the errors that [`EngineNode::start_until_cancelled`] documents.
pub(crate) async fn start_engine_node<F>(
    config: &EngineNodeConfig,
    dependencies: EngineNodeDependencies,
    command_and_effect_hooks_factory: F,
    cancellation: &mut watch::Receiver<bool>,
    local_store_source: LocalStoreSource,
) -> Result<StartOutcome, EngineStartError>
where
    F: FnOnce(EngineServices) -> EngineCommandAndEffectHooks,
{
    if let Err(error) = validate_start_config(config, &local_store_source) {
        return Ok(StartOutcome::FailedBeforeLease {
            error,
            source: local_store_source,
        });
    }
    let EngineNodeDependencies {
        metrics,
        object_store,
        clock,
        applier,
        id_source,
        lease_validity_deadline,
    } = dependencies;
    let acquisition_store = Arc::new(StartupReadRetryStore::new(
        object_store.clone(),
        config.recovery.startup_read_retry_budget,
        config.node.node_id.clone(),
        metrics.clone(),
        Some(lease_validity_deadline.clone()),
    ));
    let startup_store: Arc<dyn ObjectStore> = acquisition_store.clone();
    // Fails fast on a contradicting manifest; the predecessor may still change it until the lease.
    let unleased_metadata = tokio::select! {
        metadata = ClusterMetadata::bootstrap_or_validate(
            startup_store.as_ref(), clock.as_ref(), config.cluster, &config.node,
        ) => match metadata {
            Ok(metadata) => metadata,
            Err(error) => {
                return Ok(StartOutcome::FailedBeforeLease {
                    error,
                    source: local_store_source,
                });
            }
        },
        () = wait_for_cancellation(cancellation) => {
            return Ok(StartOutcome::Cancelled(local_store_source));
        }
    };
    let partition_count = unleased_metadata.partition_count();
    metrics
        .behavior_version
        .set(i64::from(unleased_metadata.behavior_version));
    metrics.set_role(dexzd_metrics::NodeRole::Standby);
    let cluster_id = match unleased_metadata.cluster_id() {
        Ok(cluster_id) => cluster_id,
        Err(error) => {
            return Ok(StartOutcome::FailedBeforeLease {
                error,
                source: local_store_source,
            });
        }
    };
    let acquired = config
        .lease
        .acquire_until_cancelled_with_observers_and_stores(
            LeaseObjectStores::new(startup_store, object_store.clone()),
            clock.clone(),
            &config.node,
            cancellation,
            id_source.lease_holder_process_id(),
            LeaseObservers {
                metrics: Some(metrics.as_ref().clone()),
                validity_deadline: lease_validity_deadline.clone(),
            },
        )
        .await;
    acquisition_store.finish_startup();
    let lease = match acquired {
        Ok(Some(lease)) => lease,
        Ok(None) => return Ok(StartOutcome::Cancelled(local_store_source)),
        Err(error) => {
            return Ok(StartOutcome::FailedBeforeLease {
                error: error.into(),
                source: local_store_source,
            });
        }
    };
    let retrying_store = Arc::new(StartupReadRetryStore::new(
        object_store.clone(),
        config.recovery.startup_read_retry_budget,
        config.node.node_id.clone(),
        metrics.clone(),
        None,
    ));
    let startup_store: Arc<dyn ObjectStore> = retrying_store.clone();
    let epoch = lease.epoch();
    metrics
        .lease_epoch
        .set(i64::try_from(epoch).unwrap_or(i64::MAX));
    metrics.set_role(dexzd_metrics::NodeRole::Recovering);
    let recovery_started = std::time::Instant::now();
    tracing::info!(node_id = %config.node.node_id, epoch, partition_count, "engine node recovering");
    let state = Arc::new(CommitState::new(partition_count, lease_validity_deadline));
    let mut tasks = RunningTasks::default();
    tasks.spawn(watch_lease(
        lease.subscribe(),
        state.clone(),
        metrics.clone(),
        tasks.cancellation.clone(),
    ));
    // Every failure from here on must release the lease, or a successor waits for it to expire.
    let mut starting = StartingNode {
        parts: Some(LeasedParts {
            state: state.clone(),
            tasks,
            appliers: None,
            standby: local_store_source.into_hot_standby(),
            lease,
        }),
    };
    let mut serving_future = Box::pin(async {
        let parts = starting.parts_mut();
        let metadata = unleased_metadata
            .read_under_lease(startup_store.as_ref(), &config.node, epoch)
            .await?;
        metrics
            .behavior_version
            .set(i64::from(metadata.behavior_version));
        let stream = StreamIdentity {
            cluster_id,
            node_id: config.node.node_id.clone(),
            incarnation: metadata.streams.serving_incarnation,
        };
        let (
            OpenedLocalStore {
                store,
                set_aside_id,
            },
            is_serving_hot_standby_store,
        ) = open_serving_store(
            parts.standby.as_mut(),
            config,
            startup_store.as_ref(),
            &LocalStoreRequirements {
                snapshot: metadata.node_snapshot(&config.node.node_id),
                streams: &metadata.streams,
                partition_count,
                new_store_lineage_id: id_source.store_lineage_id(),
            },
        )
        .await?;
        // Its following stopped; only the lease release path still needs an unfinished one.
        parts.standby = None;
        let rebuilt_search_order_partitions = rebuild_search_order_indexes(&store)?;
        if rebuilt_search_order_partitions > 0 {
            tracing::info!(
                partition_count = rebuilt_search_order_partitions,
                "rebuilt search order indexes"
            );
        }
        let node_meta_lock = Arc::new(std::sync::Mutex::new(()));
        let (proposal_sender, proposal_receiver) = mpsc::unbounded_channel();
        let committer = CommandCommitter::new(CommandCommitterParts {
            state: state.clone(),
            store: store.clone(),
            clock: clock.clone(),
            id_source: id_source.clone(),
            epoch,
            behavior_version: metadata.behavior_version,
            max_unapplied_bytes: config.commit.max_unapplied_bytes,
            is_checking_replay: config.commit.debug_replay_checks,
            proposals: proposal_sender,
        });
        let hooks = command_and_effect_hooks_factory(EngineServices {
            committer: committer.clone(),
            partition_hasher: metadata.cluster.partition_hasher.clone(),
            node_meta_lock: node_meta_lock.clone(),
            local_store: store.clone(),
            id_source: id_source.clone(),
        });
        let appliers = Arc::new(PartitionApplierPool::start(
            FrameApplier::new(FrameApplierParts {
                metrics: metrics.clone(),
                store: store.clone(),
                applier,
                hasher: metadata.cluster.partition_hasher.clone(),
                state: state.clone(),
                committed_commands: hooks.committed_commands.clone(),
                is_checking_replay: config.commit.debug_replay_checks,
                blocking_work: config.blocking_work,
            })?,
            config.commit.applier_lanes,
            config.commit.applier_queue_frames,
        ));
        parts.appliers = Some(appliers.clone());
        let replayed = LogReplay {
            config: &config.recovery,
            object_store: startup_store.as_ref(),
            clock: clock.as_ref(),
            id_source: id_source.as_ref(),
            cluster_id,
            streams: &metadata.streams,
            store: &store,
            appliers: &appliers,
            state: &state,
            epoch,
        }
        .run()
        .await?;
        delete_older_set_aside_stores(
            &config.local_store,
            config.recovery.retained_set_aside_store_count,
            set_aside_id,
            config.blocking_work,
        )
        .await;
        // Recovery uses these applier lanes; only later frames are live commits.
        appliers.enable_metrics();
        committer.seed_stamp_watermarks(&applied_command_timestamps(&store)?);
        let (pipeline, committed_batches) = CommitPipeline::start_with_metrics(
            object_store.clone(),
            clock.clone(),
            stream.clone(),
            epoch,
            replayed.next_seq,
            config.commit.pipeline_config(),
            metrics.as_ref().clone(),
        )?;
        let progress = Arc::new(CommitProgress {
            committed_log_bytes: AtomicU64::new(0),
            last_routed_seq: AtomicU64::new(replayed.epoch_start_seq),
        });
        let tasks = &mut parts.tasks;
        tasks.spawn(run_log_proposer(
            pipeline,
            proposal_receiver,
            state.clone(),
            tasks.cancellation.clone(),
        ));
        tasks.spawn(
            CommittedBatchRouter {
                appliers: appliers.clone(),
                state: state.clone(),
                node_id: stream.node_id.clone(),
                incarnation: stream.incarnation,
                progress: progress.clone(),
            }
            .run(committed_batches, tasks.cancellation.clone()),
        );
        let applied_positions = Arc::new(AppliedPositionAdvancer::new(
            appliers.clone(),
            AppliedPositionSource::RoutedBatches {
                progress: progress.clone(),
                node_id: stream.node_id.clone(),
                incarnation: stream.incarnation,
                epoch,
            },
            partition_count,
        ));
        tasks.spawn(applied_positions.clone().run(tasks.cancellation.clone()));
        // Rebuilt work may submit at once, so commands are accepted before rebuilding.
        if !state.start_serving() {
            return Err(EngineStartError::Stopped {
                reason: state.stop_reason().unwrap_or(NodeStopReason::Shutdown),
            });
        }
        let rebuild_started = Instant::now();
        let mut rebuilt_partition_count = 0;
        let mut total_effects = 0;
        for partition in 0..partition_count {
            let partition = PartitionId(
                u16::try_from(partition).expect("the manifest limits partitions to u16"),
            );
            if !state.is_partition_halted(partition) {
                total_effects += hooks.rebuilder.rebuild_partition(partition).await?;
                rebuilt_partition_count += 1;
            }
        }
        tracing::info!(
            rebuilt_partition_count,
            total_effects,
            duration_ms = u64::try_from(rebuild_started.elapsed().as_millis()).unwrap_or(u64::MAX),
            "rebuilt partition effects"
        );
        if let Some(reason) = state.stop_reason() {
            return Err(EngineStartError::Stopped { reason });
        }
        let snapshots = Arc::new(SnapshotService {
            store,
            object_store: object_store.clone(),
            clock: clock.clone(),
            appliers: appliers.clone(),
            progress,
            retention: SnapshotRetention {
                object_store: object_store.clone(),
                node_id: stream.node_id.clone(),
                retained_count: config.snapshots.retained_count,
                deletion_delay: config.snapshots.deletion_delay,
            },
            fence: OwnershipFence {
                object_store: object_store.clone(),
                clock: clock.clone(),
                state: state.clone(),
                node_id: stream.node_id.clone(),
                epoch,
            },
            log_cleanup: LogCleanup {
                object_store,
                clock,
                stream,
                partition_count,
                deletion_delay: config.snapshots.deletion_delay,
            },
            cluster_id,
            epoch,
            id_source,
            node_meta_lock,
            config: config.snapshots.clone(),
            catalog: tokio::sync::Mutex::new(SnapshotCatalog::default()),
            last_snapshot_routed_seq: AtomicU64::new(0),
            blocking_work: config.blocking_work,
            metrics: metrics.clone(),
        });
        tasks.spawn(snapshots.clone().run(tasks.cancellation.clone()));
        metrics
            .recovery_last_duration
            .set(recovery_started.elapsed().as_secs_f64());
        metrics
            .recovery_last_replayed_bytes
            .set(i64::try_from(replayed.replayed_bytes).unwrap_or(i64::MAX));
        tracing::info!(epoch, next_seq = replayed.next_seq, "engine node serving");
        metrics.set_role(dexzd_metrics::NodeRole::Serving);
        Ok((
            committer,
            applied_positions,
            snapshots,
            is_serving_hot_standby_store,
        ))
    });
    let serving_result = tokio::select! {
        biased;
        reason = state.wait_stopped() => Err(EngineStartError::Stopped { reason }),
        () = wait_for_cancellation(cancellation) => Err(EngineStartError::Stopped {
            reason: NodeStopReason::Shutdown,
        }),
        result = &mut serving_future => result,
    };
    drop(serving_future);
    retrying_store.finish_startup();
    match serving_result {
        Ok((committer, applied_positions, snapshots, is_serving_hot_standby_store)) => {
            tracing::info!(
                is_serving_hot_standby_store,
                "engine node started its local store"
            );
            let LeasedParts {
                state,
                tasks,
                appliers,
                lease,
                ..
            } = starting.take_parts();
            Ok(StartOutcome::Serving(EngineNode {
                metrics,
                committer,
                state,
                appliers: appliers.expect("the applier pool starts before the node serves"),
                applied_positions,
                snapshots,
                lease,
                tasks,
                cluster_id,
                #[cfg(feature = "test-util")]
                is_serving_hot_standby_store,
            }))
        }
        Err(error) => {
            let release_result = starting.release_after_failure().await;
            if release_result.is_ok() {
                metrics.lease_epoch.set(0);
            }
            metrics.set_role(dexzd_metrics::NodeRole::Standby);
            match release_result {
                Ok(()) => Err(error),
                Err(detail) => Err(EngineStartError::FailedStartLeaseRelease {
                    start: Box::new(error),
                    detail,
                }),
            }
        }
    }
}

/// Owns the leased parts while [`EngineNode::start`] runs, so a failed or cancelled start releases
/// the lease.
struct StartingNode {
    parts: Option<LeasedParts>,
}

impl StartingNode {
    fn parts_mut(&mut self) -> &mut LeasedParts {
        self.parts
            .as_mut()
            .expect("the parts stay until the start finishes")
    }

    /// Disarms the guard and returns the parts.
    fn take_parts(&mut self) -> LeasedParts {
        self.parts
            .take()
            .expect("the parts stay until the start finishes")
    }

    /// Stops the parts and releases the lease; the release continues if the caller is cancelled.
    async fn release_after_failure(mut self) -> Result<(), String> {
        let release_task = tokio::spawn(release_after_unfinished_start(self.take_parts()));
        match release_task.await {
            Ok(result) => result.map_err(|error| error.to_string()),
            Err(error) => {
                tracing::error!(%error, "the lease release task failed after an unfinished start");
                Err(error.to_string())
            }
        }
    }
}

impl Drop for StartingNode {
    fn drop(&mut self) {
        let Some(parts) = self.parts.take() else {
            return;
        };
        match tokio::runtime::Handle::try_current() {
            Ok(runtime) => {
                // Detached on purpose: the release must outlive the cancelled start.
                let release_task = runtime.spawn(release_after_unfinished_start(parts));
                drop(release_task);
            }
            Err(_) => tracing::error!(
                "no async runtime can release the node lease after a cancelled start; it stays \
                 held until it expires"
            ),
        }
    }
}

/// Stops the parts of a failed or cancelled start, releases the lease, and logs the outcome.
async fn release_after_unfinished_start(parts: LeasedParts) -> Result<(), LeaseReleaseError> {
    let result = parts.stop_and_release_lease(None).await;
    match &result {
        Ok(()) => tracing::info!("released the node lease after an unfinished start"),
        Err(error) => {
            tracing::error!(%error, "the node lease stays held after an unfinished start")
        }
    }
    result
}

async fn wait_for_cancellation(cancellation: &mut watch::Receiver<bool>) {
    loop {
        if *cancellation.borrow_and_update() {
            return;
        }
        if cancellation.changed().await.is_err() {
            std::future::pending::<()>().await;
        }
    }
}

fn applied_command_timestamps(
    store: &EngineLocalStore,
) -> Result<Vec<(PartitionId, i64)>, EngineStartError> {
    store
        .partition_metas()?
        .into_iter()
        .map(|meta| {
            let partition =
                u16::try_from(meta.partition).map_err(|_| EngineStartError::InvalidMetadata {
                    detail: format!("local store names partition {}", meta.partition),
                })?;
            Ok((PartitionId(partition), meta.last_command_timestamp_micros))
        })
        .collect()
}

/// Stops the node when the lease is lost or its local validity deadline passes.
async fn watch_lease(
    mut status: watch::Receiver<LeaseStatus>,
    state: Arc<CommitState>,
    metrics: Arc<dexzd_metrics::NodeMetrics>,
    cancellation: CancellationToken,
) {
    loop {
        let current = status.borrow_and_update().clone();
        let deadline = match current {
            LeaseStatus::Lost(detail) => {
                metrics.lease_epoch.set(0);
                state.stop(NodeStopReason::LeaseLost { detail });
                return;
            }
            LeaseStatus::ValidUntil(deadline) => deadline,
        };
        tokio::select! {
            biased;
            () = cancellation.cancelled() => return,
            changed = status.changed() => {
                if changed.is_err() {
                    metrics.lease_epoch.set(0);
                    state.stop(NodeStopReason::LeaseLost {
                        detail: "lease renewal stopped".to_owned(),
                    });
                    return;
                }
            }
            () = tokio::time::sleep_until(deadline) => {
                metrics.lease_epoch.set(0);
                state.stop(NodeStopReason::LeaseLost {
                    detail: LEASE_VALIDITY_EXPIRED_DETAIL.to_owned(),
                });
                return;
            }
        }
    }
}
