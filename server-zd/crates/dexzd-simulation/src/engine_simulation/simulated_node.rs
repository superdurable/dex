// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! One engine node process: the engine, its Flow runtime and effects, and how it crashes.

use std::collections::{BTreeMap, BTreeSet};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use bytes::Bytes;
use dexzd_apply::{ApplyContext, ApplyError, ApplyOutcome, FlowStateReader};
use dexzd_clock::WallClock;
use dexzd_cluster_metadata::{
    ClusterConfig, LeaseConfig, METADATA_FORMAT_VERSION, MetadataError, NodeConfig,
};
use dexzd_engine::{
    AttributeSyncWaker, BlockingWorkPlacement, CommandApplier, CommitConfig,
    DeterministicCommandApplier, EffectDependencies, EffectRunner, EffectTasksHandle,
    EffectsConfig, EngineCommandAndEffectHooks, EngineIdSource, EngineLocalStore, EngineNode,
    EngineNodeConfig, EngineNodeDependencies, EngineServices, EngineStartError,
    FlowCoordinationConfig, FlowCoordinatorDependencies, FlowCoordinatorRegistry,
    HardDeleteObjects, HotStandby, HotStandbyTakeover, LocalStoreConfig, NodeMetaUploadLedger,
    NodeStopReason, RecoveryConfig, SnapshotConfig, default_run_settings,
};
use dexzd_keyspace::{ColumnFamily, KEY_LAYOUT_VERSION};
use dexzd_object_store::{ObjectStore, WriteCondition};
use dexzd_protocol::dexzd::v1::{ClusterObject, FlowCommand, PartitionHashAlgorithm, RunSettings};
use dexzd_request_validation::ValidationConfig;
use dexzd_storage::{InMemoryLocalStore, LocalStore, ScanDirection, WriteBatch};
use dexzd_stream_store::{StreamStore, StreamStoreConfig};
use prost::Message;
use rand::RngExt;
use rand_chacha::ChaCha8Rng;
use tokio::task::JoinHandle;

use crate::engine_simulation::EngineSimulationProfile;
use crate::engine_simulation::committed_log::CommittedLog;
use crate::engine_simulation::event_trace::EventTrace;
use crate::engine_simulation::simulated_clock::{
    START_UNIX_MICROS, SimulatedWallClock, duration_micros,
};
use crate::engine_simulation::simulated_object_store::{NodeObjectStore, SharedObjectStorage};
use crate::engine_simulation::simulated_workers::SimulatedWorkers;

/// The single node of the simulated cluster.
pub(crate) const NODE_ID: &str = "node-a";
/// Partitioning of [`crate::ApplyChecker::new`], so the shadow places Flows like the engine.
const PARTITION_HASH_KEY: [u8; 16] = [7; 16];
pub(crate) const PARTITION_COUNT: u32 = 64;
/// Object key of the cluster object, a persistent format.
const CLUSTER_OBJECT_KEY: &str = "cluster";
pub(crate) const LEASE_DURATION: Duration = Duration::from_secs(10);
pub(crate) const LEASE_TAKEOVER_GRACE: Duration = Duration::from_secs(1);
/// Probability that a process crashes at a random point while it starts.
pub(crate) const START_CRASH_PROBABILITY: f64 = 0.15;

/// What every node process of one seed shares.
pub(crate) struct ClusterEnvironment {
    /// The shared object store, its observations, and writes still on their way to it.
    pub(crate) storage: Arc<SharedObjectStorage>,
    pub(crate) workers: Arc<SimulatedWorkers>,
    pub(crate) committed_log: Arc<CommittedLog>,
    pub(crate) trace: Arc<EventTrace>,
    /// The seeded source of every ID the engine persists, shared by all processes.
    pub(crate) id_source: Arc<dyn EngineIdSource>,
    /// How long closed runs stay before their purge; some seeds keep them past the end.
    pub(crate) retention_after_close: Duration,
    pub(crate) profile: EngineSimulationProfile,
    pub(crate) origin: tokio::time::Instant,
    pub(crate) close_hold_dry_runs: Arc<CloseHoldDryRuns>,
}

/// Commands that a registry dry-ran for an admitted RPC's close hold, by command kind.
#[derive(Default)]
pub(crate) struct CloseHoldDryRuns {
    by_kind: Mutex<BTreeMap<&'static str, u64>>,
}

impl CloseHoldDryRuns {
    pub(crate) fn counts(&self) -> BTreeMap<&'static str, u64> {
        self.by_kind
            .lock()
            .expect("dry run count lock is not poisoned")
            .clone()
    }
}

/// The registry's applier. A command applied a second time had a dry run first.
struct DryRunCountingApplier {
    applier: Arc<dyn CommandApplier>,
    applied_command_ids: Mutex<BTreeSet<Vec<u8>>>,
    dry_runs: Arc<CloseHoldDryRuns>,
}

impl CommandApplier for DryRunCountingApplier {
    fn apply(
        &self,
        context: &ApplyContext,
        state: &dyn FlowStateReader,
        command: &FlowCommand,
    ) -> Result<ApplyOutcome, ApplyError> {
        let is_first_apply = self
            .applied_command_ids
            .lock()
            .expect("command ID lock is not poisoned")
            .insert(command.command_id.clone());
        if !is_first_apply {
            *self
                .dry_runs
                .by_kind
                .lock()
                .expect("dry run count lock is not poisoned")
                .entry(crate::command_kind(command))
                .or_default() += 1;
        }
        self.applier.apply(context, state, command)
    }
}

impl ClusterEnvironment {
    /// Returns a new process's view of the shared store, with its own fault draws.
    pub(crate) fn node_object_store(
        &self,
        process_label: &str,
        start_kind: &str,
        fault_seed: u64,
        fault_probability: f64,
    ) -> Arc<NodeObjectStore> {
        let object_store = Arc::new(NodeObjectStore::new(
            Arc::clone(&self.storage),
            process_label.to_owned(),
            start_kind.to_owned(),
            fault_seed,
        ));
        object_store.set_fault_probability(fault_probability);
        object_store
    }

    /// Writes the cluster object before any node starts, so its hash key is not random.
    pub(crate) async fn bootstrap_cluster_object(&self, random_bytes: [u8; 16]) {
        let mut cluster_id = random_bytes;
        // UUID version 7 and the RFC 4122 variant, which cluster validation requires.
        cluster_id[6] = (cluster_id[6] & 0x0F) | 0x70;
        cluster_id[8] = (cluster_id[8] & 0x3F) | 0x80;
        let cluster = ClusterObject {
            format_version: METADATA_FORMAT_VERSION,
            cluster_id: cluster_id.to_vec(),
            partition_count: PARTITION_COUNT,
            partition_hash_algorithm: PartitionHashAlgorithm::Siphash24 as i32,
            partition_hash_key: PARTITION_HASH_KEY.to_vec(),
            key_layout_version: KEY_LAYOUT_VERSION,
            created_at_unix_micros: START_UNIX_MICROS,
        };
        self.storage
            .store
            .put(
                CLUSTER_OBJECT_KEY,
                Bytes::from(cluster.encode_to_vec()),
                WriteCondition::CreateOnly,
            )
            .await
            .expect("the empty in-memory store accepts the cluster object");
    }
}

/// How a restarted process finds its local store.
pub(crate) enum LocalStoreStart {
    /// Restored from the newest snapshot, or empty: a new machine or a lost disk.
    Lost,
    /// A store the process continues with: the store at the crash, or an earlier copy of it.
    Existing(Arc<EngineLocalStore>),
}

#[derive(Debug, Clone, Copy)]
/// What a restarted process finds on its local disk.
pub(crate) enum RestartKind {
    Kept,
    Lost,
    RolledBack,
}

impl RestartKind {
    pub(crate) fn name(self) -> &'static str {
        match self {
            Self::Kept => "kept",
            Self::Lost => "lost",
            Self::RolledBack => "rolled-back",
        }
    }
}

/// Settings of one process start.
pub(crate) struct ProcessStart {
    pub(crate) process_label: String,
    pub(crate) local_store: LocalStoreStart,
    pub(crate) incarnation: u32,
    pub(crate) clock_offset_micros: i64,
    pub(crate) object_store: Arc<NodeObjectStore>,
}

/// A running node process.
pub(crate) struct NodeProcess {
    pub(crate) process_label: String,
    engine: EngineNode,
    pub(crate) registry: FlowCoordinatorRegistry,
    effect_tasks: EffectTasksHandle,
    pub(crate) object_store: Arc<NodeObjectStore>,
    pub(crate) local_store: Arc<EngineLocalStore>,
    pub(crate) clock: Arc<SimulatedWallClock>,
}

/// Runtime parts the engine's factory builds, captured for the process.
struct SimulatedNodeStartupParts {
    registry: FlowCoordinatorRegistry,
    effect_tasks: EffectTasksHandle,
    local_store: Arc<EngineLocalStore>,
}

/// Why a takeover from a hot standby did not serve, with the standby while it still follows.
pub(crate) struct FailedTakeover {
    pub(crate) error: EngineStartError,
    pub(crate) standby: Option<HotStandby>,
}

impl NodeProcess {
    /// Starts a process: recovery, log replay, and runtime rebuild, as the server does.
    pub(crate) async fn start(
        environment: &ClusterEnvironment,
        start: ProcessStart,
    ) -> Result<Self, EngineStartError> {
        Self::launch(environment, start, None)
            .await
            .map_err(|failed| failed.error)
    }

    /// Starts a process that takes over from `standby`, whose store `start` names.
    pub(crate) async fn take_over(
        environment: &ClusterEnvironment,
        start: ProcessStart,
        standby: HotStandby,
    ) -> Result<Self, FailedTakeover> {
        Self::launch(environment, start, Some(standby)).await
    }

    /// Starts a process, from `standby` when one is given.
    async fn launch(
        environment: &ClusterEnvironment,
        start: ProcessStart,
        standby: Option<HotStandby>,
    ) -> Result<Self, FailedTakeover> {
        let ProcessStart {
            process_label,
            local_store,
            incarnation,
            clock_offset_micros,
            object_store,
        } = start;
        let clock = Arc::new(SimulatedWallClock::new(
            environment.origin,
            clock_offset_micros,
        ));
        let local_store_config = match local_store {
            LocalStoreStart::Lost => LocalStoreConfig::InMemory,
            LocalStoreStart::Existing(store) => LocalStoreConfig::ExistingForTest(store),
        };
        let settings = run_settings(environment.retention_after_close, environment.profile);
        let applier: Arc<dyn CommandApplier> = Arc::new(DeterministicCommandApplier);
        let stream_store = Arc::new(
            StreamStore::new(&StreamStoreConfig::default())
                .expect("the disabled default Stream Store always starts"),
        );
        let worker_port = environment.workers.dispatcher_for(&process_label);
        let committed_log = Arc::clone(&environment.committed_log);
        let startup_parts: Arc<Mutex<Option<SimulatedNodeStartupParts>>> =
            Arc::new(Mutex::new(None));
        let wall_clock: Arc<dyn WallClock> = clock.clone();
        let object_store_port: Arc<dyn ObjectStore> = object_store.clone();
        let factory_label = process_label.clone();
        let factory_parts = Arc::clone(&startup_parts);
        let factory_clock = Arc::clone(&wall_clock);
        let factory_store = Arc::clone(&object_store_port);
        let factory_applier: Arc<dyn CommandApplier> = Arc::new(DryRunCountingApplier {
            applier: Arc::clone(&applier),
            applied_command_ids: Mutex::new(BTreeSet::new()),
            dry_runs: Arc::clone(&environment.close_hold_dry_runs),
        });
        let config = engine_node_config(local_store_config, incarnation, environment.profile);
        let dependencies = EngineNodeDependencies {
            metrics: Arc::new(dexzd_metrics::NodeMetrics::unregistered()),
            object_store: object_store_port,
            clock: wall_clock,
            applier,
            id_source: Arc::clone(&environment.id_source),
            lease_validity_deadline: dexzd_cluster_metadata::LeaseValidityDeadline::disarmed(),
        };
        let factory = move |services: EngineServices| {
            let partition_hasher = services.partition_hasher.clone();
            let hard_delete_objects = HardDeleteObjects::new(
                factory_store,
                Arc::new(NodeMetaUploadLedger::new(
                    services.local_store.clone(),
                    services.node_meta_lock.clone(),
                )),
                factory_clock.clone(),
                partition_hasher.clone(),
                services.id_source.clone(),
            );
            let (effect_runner, effect_tasks) = EffectRunner::new(
                &effects_config(settings),
                EffectDependencies {
                    metrics: Arc::new(dexzd_metrics::NodeMetrics::unregistered()),
                    dispatcher: worker_port.clone(),
                    hard_delete_objects: hard_delete_objects.clone(),
                    partition_hasher: partition_hasher.clone(),
                    clock: factory_clock,
                    validation_config: ValidationConfig::default(),
                    stream_store,
                    id_source: services.id_source.clone(),
                    attribute_sync_waker: AttributeSyncWaker::new(),
                },
            );
            let registry = FlowCoordinatorRegistry::new(
                &flow_coordination_config(settings),
                FlowCoordinatorDependencies {
                    hasher: partition_hasher,
                    committer: Arc::new(services.committer),
                    applier: factory_applier,
                    effects: Arc::new(effect_runner.clone()),
                    rpc_invoker: worker_port,
                    hard_delete_reader: Arc::new(hard_delete_objects.clone()),
                    hard_delete_uploader: Arc::new(hard_delete_objects),
                    id_source: services.id_source,
                },
            );
            let effect_tasks = effect_tasks.start(Arc::new(registry.clone()));
            let hooks = EngineCommandAndEffectHooks {
                committed_commands: committed_log.sink_for(&factory_label, registry.clone()),
                rebuilder: Arc::new(effect_runner),
            };
            *factory_parts
                .lock()
                .expect("runtime parts lock is not poisoned") = Some(SimulatedNodeStartupParts {
                registry,
                effect_tasks,
                local_store: services.local_store,
            });
            hooks
        };
        let started = match standby {
            None => EngineNode::start(&config, dependencies, factory)
                .await
                .map_err(|error| FailedTakeover {
                    error,
                    standby: None,
                }),
            Some(standby) => {
                // The simulator never cancels a takeover; a crash drops it instead.
                let (_never_cancelled, mut cancellation) = tokio::sync::watch::channel(false);
                match EngineNode::take_over_from_hot_standby_until_cancelled(
                    &config,
                    dependencies,
                    factory,
                    standby,
                    &mut cancellation,
                )
                .await
                {
                    Ok(HotStandbyTakeover::Serving(engine)) => Ok(engine),
                    Ok(not_serving) => Err(FailedTakeover {
                        error: EngineStartError::Stopped {
                            reason: NodeStopReason::Shutdown,
                        },
                        standby: match not_serving {
                            HotStandbyTakeover::Cancelled(standby) => Some(standby),
                            _ => None,
                        },
                    }),
                    Err(failed) => Err(FailedTakeover {
                        error: failed.error,
                        standby: failed.standby,
                    }),
                }
            }
        };
        let parts = startup_parts
            .lock()
            .expect("runtime parts lock is not poisoned")
            .take();
        let engine = match started {
            Ok(engine) => engine,
            Err(error) => {
                if let Some(parts) = parts {
                    parts.registry.close_long_polls();
                    parts.registry.cancel_rpc_tasks();
                    tokio::time::timeout(
                        Duration::from_secs(1),
                        parts.registry.wait_for_rpc_tasks(),
                    )
                    .await
                    .expect("cancelled RPC tasks stop within their shutdown budget");
                    parts.effect_tasks.shutdown().await;
                }
                return Err(error);
            }
        };
        let parts = parts.expect("the engine calls the runtime factory before it serves");
        Ok(Self {
            process_label,
            engine,
            registry: parts.registry,
            effect_tasks: parts.effect_tasks,
            object_store,
            local_store: parts.local_store,
            clock,
        })
    }

    /// Returns whether the process serves from the store a hot standby handed over.
    pub(crate) fn is_serving_hot_standby_store(&self) -> bool {
        self.engine.is_serving_hot_standby_store_for_test()
    }

    /// Returns why the engine stopped, or `None` while it serves.
    pub(crate) fn stop_reason(&self) -> Option<NodeStopReason> {
        self.engine.stop_reason()
    }

    /// Partitions that stopped applying commands after an apply failure.
    pub(crate) fn halted_partitions(&self) -> Vec<dexzd_keyspace::PartitionId> {
        self.engine.halted_partitions()
    }

    pub(crate) fn is_registry_halted(&self) -> bool {
        self.registry.halted().is_cancelled()
    }

    /// Kills the process, cutting off its storage first; `tasks` gets its effect shutdown task.
    pub(crate) fn crash(self, tasks: &mut Vec<JoinHandle<()>>) {
        let Self {
            engine,
            registry,
            effect_tasks,
            object_store,
            ..
        } = self;
        object_store.cut_off();
        registry.close_long_polls();
        registry.cancel_rpc_tasks();
        drop(engine);
        let effect_shutdown = tokio::spawn(async move {
            tokio::time::timeout(Duration::from_secs(1), registry.wait_for_rpc_tasks())
                .await
                .expect("cancelled RPC tasks stop within their shutdown budget");
            effect_tasks.shutdown().await;
        });
        tasks.push(effect_shutdown);
    }
}

/// Copies every row of `store` into a new in-memory store: a disk image at this instant.
pub(crate) fn copy_local_store(store: &EngineLocalStore) -> Arc<EngineLocalStore> {
    let view = store
        .consistent_view()
        .expect("an in-memory store always opens a consistent view");
    let mut batch = WriteBatch::new();
    for column_family in ColumnFamily::ALL {
        let rows = view
            .scan(
                column_family,
                None,
                None,
                ScanDirection::Forward,
                usize::MAX,
            )
            .expect("an in-memory view always scans");
        for (key, value) in rows {
            batch.put(column_family, key, value);
        }
    }
    let copy = InMemoryLocalStore::new();
    copy.write(batch)
        .expect("an in-memory store accepts puts of valid rows");
    Arc::new(EngineLocalStore::Memory(copy))
}

pub(crate) fn engine_node_config(
    local_store: LocalStoreConfig,
    incarnation: u32,
    profile: EngineSimulationProfile,
) -> EngineNodeConfig {
    let mut snapshots = SnapshotConfig {
        log_threshold_bytes: 64 * 1024,
        interval: Duration::from_secs(20),
        retry_delay: Duration::from_secs(5),
        deletion_delay: Duration::from_secs(30),
        ..SnapshotConfig::default()
    };
    if profile == EngineSimulationProfile::RedeploysWithShortHistoryWindow {
        // Without snapshots, a start with a lost store replays every closed stream from seq 1.
        snapshots.log_threshold_bytes = u64::MAX;
        snapshots.interval = Duration::MAX;
    }
    EngineNodeConfig {
        node: NodeConfig {
            node_id: NODE_ID.to_owned(),
            incarnation,
            internal_address: String::new(),
        },
        cluster: ClusterConfig {
            partition_count: PARTITION_COUNT,
        },
        lease: LeaseConfig {
            duration: LEASE_DURATION,
            takeover_grace: LEASE_TAKEOVER_GRACE,
            // The wait loop draws jitter from the thread RNG; the simulator schedules retries.
            fail_fast: true,
            should_take_over_held_lease: false,
        },
        local_store,
        commit: CommitConfig {
            applier_lanes: 2,
            debug_replay_checks: true,
            ..CommitConfig::default()
        },
        snapshots,
        recovery: RecoveryConfig::default(),
        blocking_work: BlockingWorkPlacement::CallingTaskForTest,
    }
}

fn effects_config(settings: RunSettings) -> EffectsConfig {
    EffectsConfig {
        retry_max_backoff: Duration::from_secs(5),
        child_run_settings: settings,
        blocking_work: BlockingWorkPlacement::CallingTaskForTest,
        ..EffectsConfig::default()
    }
}

fn flow_coordination_config(settings: RunSettings) -> FlowCoordinationConfig {
    FlowCoordinationConfig {
        // As on the server, hard-delete values have an object store.
        validation: ValidationConfig {
            has_blob_store: true,
            ..ValidationConfig::default()
        },
        long_poll_default_wait: Duration::from_secs(5),
        long_poll_max_wait: Duration::from_secs(10),
        rpc_default_timeout: Duration::from_secs(5),
        run_settings: settings,
        ..FlowCoordinationConfig::default()
    }
}

fn run_settings(retention_after_close: Duration, profile: EngineSimulationProfile) -> RunSettings {
    let mut settings = RunSettings {
        retention_after_close_micros: duration_micros(retention_after_close),
        request_dedup_capacity: 64,
        ..default_run_settings()
    };
    if profile == EngineSimulationProfile::RedeploysWithShortHistoryWindow {
        settings.history_window_semantic_events = 4;
        settings.history_prune_chunk_semantic_events = 1;
    }
    settings
}

/// Names why a process stopped; `None` means only its registry halted.
pub(crate) fn self_stop_kind(reason: Option<&NodeStopReason>) -> &'static str {
    match reason {
        None => "registry-halted",
        Some(NodeStopReason::Shutdown) => "shutdown",
        Some(NodeStopReason::LeaseLost { .. }) => "lease-lost",
        Some(NodeStopReason::Fenced) => "fenced",
        // The engine reports ProposalFailure::Restart only through this detail text.
        Some(NodeStopReason::CommitPipelineFailed { detail })
            if detail.contains("earlier epoch") =>
        {
            "older-epoch-batch"
        }
        Some(NodeStopReason::CommitPipelineFailed { .. }) => "commit-pipeline-failed",
        Some(NodeStopReason::LocalStoreFailed { .. }) => "local-store-failed",
    }
}

pub(crate) fn start_error_kind(error: &EngineStartError) -> &'static str {
    match error {
        EngineStartError::Metadata(MetadataError::LeaseHeld { .. }) => "lease-held",
        EngineStartError::Metadata(_) => "metadata",
        EngineStartError::Storage(_) => "local-store",
        EngineStartError::Log(_) => "log",
        EngineStartError::Stopped { .. } => "stopped",
        EngineStartError::PartitionEffectRebuild(_) => "runtime-rebuild",
        _ => "other",
    }
}

/// Returns how long a start that failed with `error` waits before the next: until a held lease
/// is free, or 0.2 to 3 seconds.
pub(crate) fn wait_before_next_start(
    error: &EngineStartError,
    clock: &SimulatedWallClock,
    rng: &mut ChaCha8Rng,
) -> Duration {
    match error {
        EngineStartError::Metadata(MetadataError::LeaseHeld {
            expires_at_unix_micros,
            ..
        }) => {
            let available_at =
                expires_at_unix_micros + duration_micros(LEASE_TAKEOVER_GRACE) + 1_000;
            let micros = (available_at - clock.now_unix_micros()).max(1_000);
            Duration::from_micros(u64::try_from(micros).unwrap_or(1_000))
        }
        _ => Duration::from_millis(rng.random_range(200..3_000)),
    }
}
