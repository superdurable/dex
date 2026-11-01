// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

mod attribute_sync;
mod bounded_teardown;
mod client_deadline;
mod grpc_server;
mod lease_validity_watchdog;
mod node_lifecycle;
mod public_api_authentication;
mod search_projection;
#[cfg(test)]
mod shutdown_budget_tests;
#[cfg(test)]
mod startup_cancellation_tests;

use std::net::SocketAddr;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use dexzd_api::{
    AttributeIndexes, DexFlowService, FlowServiceDependencies, HealthStatus, StreamOperations,
};
use dexzd_apply::FlowStateReader;
use dexzd_cluster_metadata::LeaseValidityDeadline;
use dexzd_engine::{
    AttributeSyncWaker, CommandCommitter, EffectDependencies, EffectRunner, EffectTasksHandle,
    EngineCommandAndEffectHooks, EngineNode, EngineNodeConfig, EngineNodeDependencies,
    EngineServices, EngineStartError, FlowCoordinatorDependencies, FlowCoordinatorRegistry,
    HardDeleteObjects, HotStandby, HotStandbyTakeover, LocalStoreCloseWaiter, NodeMetaUploadLedger,
    NodeStopReason,
};
use dexzd_keyspace::PartitionHasher;
use dexzd_metrics::NodeMetrics;
use dexzd_protocol::dex::worker_task_service_server::WorkerTaskServiceServer;
use dexzd_search::builtin::{BuiltinSearchBackend, CommittedSearchViewProvider};
use dexzd_search::{SearchBackend, SearchError};
use dexzd_stream_store::StreamStore;
use dexzd_worker_dispatch::WorkerDispatch;
use tokio::net::TcpListener;
use tokio::sync::{mpsc, watch};
use tokio::task::JoinHandle;
use tokio_util::sync::CancellationToken;

use crate::ServerError;
use crate::component_configs::ComponentConfigs;
use crate::config::{Config, SearchBackend as SearchBackendKind};
use crate::embedded_s3::EmbeddedS3;
use crate::metrics_server::MetricsServer;
use crate::registry_committed_command_sink::RegistryCommittedCommandSink;
use crate::worker_rpc_invoker::PullWorkerRpcInvoker;

pub(crate) use search_projection::SearchOutboxWatermarks;

use attribute_sync::{AttributeStores, AttributeSyncService, AttributeSyncStart};
use bounded_teardown::BoundedTeardown;
use grpc_server::{GrpcInstancePhase, GrpcServer, GrpcSettings};
pub(crate) use lease_validity_watchdog::LeaseValidityWatchdog;
use node_lifecycle::{
    AdministrationRequest, HeldNodeLease, LifecycleActiveNodeAdministration, NodeLifecycle,
    ProcessParts, StepDownRequest,
};
use search_projection::{
    SearchProjection, SearchProjectionModeRebuilder, SearchProjectionStart, SqlVisibilitySearch,
};

/// Part of the budget for open calls, so the engine still stops cleanly after a stuck call.
const GRPC_DRAIN_BUDGET: Duration = Duration::from_secs(4);
/// Shutdown budgets of the later steps; with the gRPC drain they total 15 seconds.
const RPC_TASKS_STOP_BUDGET: Duration = Duration::from_millis(500);
const EFFECT_TASKS_STOP_BUDGET: Duration = Duration::from_secs(2);
/// Also bounds the Attribute Sync projector, which stops at the same time.
const SEARCH_PROJECTION_STOP_BUDGET: Duration = Duration::from_secs(1);
/// Covers the engine's three-second task stop and its lease release.
/// It also covers a cancelled takeover's release.
const ENGINE_STOP_BUDGET: Duration = longer_duration(
    EngineNode::SHUTDOWN_BUDGET,
    EngineNode::TAKEOVER_RELEASE_BUDGET,
)
.saturating_add(Duration::from_millis(500));
const STREAM_STORE_CLOSE_BUDGET: Duration = Duration::from_secs(1);
/// Closing the relaxed embedded store fsyncs under host load; abandoning it can lose acknowledged writes.
const EMBEDDED_S3_MINIMUM_STOP_BUDGET: Duration = Duration::from_secs(1);
/// Embedded S3 stops last and may use whatever the earlier steps left of this total.
const SHUTDOWN_BUDGET: Duration = GRPC_DRAIN_BUDGET
    .saturating_add(RPC_TASKS_STOP_BUDGET)
    .saturating_add(EFFECT_TASKS_STOP_BUDGET)
    .saturating_add(SEARCH_PROJECTION_STOP_BUDGET)
    .saturating_add(ENGINE_STOP_BUDGET)
    .saturating_add(STREAM_STORE_CLOSE_BUDGET)
    .saturating_add(EMBEDDED_S3_MINIMUM_STOP_BUDGET);
/// Tenant that serves pull Workers until authentication assigns tenants.
const DEFAULT_TENANT_ID: u64 = 0;
/// The answer to a step down that reached a standby instance of a process that now serves.
const STANDBY_INSTANCE_STEP_DOWN_REFUSAL: &str =
    "the call reached a standby connection that closes soon; retry it";

/// How the process reacts when the lease validity deadline passes without a renewal.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum LeaseExpiryResponse {
    /// A watchdog thread also exits the process with status 1, even when the async runtime stalls.
    ExitProcess,
    /// Only the node stops, so an embedding process such as a test keeps running.
    StopNodeOnly,
}

/// A started server. Dropping it without [`RunningServer::shutdown`] aborts it.
pub struct RunningServer {
    local_address: SocketAddr,
    metrics_address: Option<SocketAddr>,
    shutdown_request: CancellationToken,
    serving: JoinHandle<Result<(), ServerError>>,
    lease_watchdog: Option<LeaseValidityWatchdog>,
    #[cfg(feature = "test-util")]
    dev_object_store_for_test: Option<Arc<s3mock::Store>>,
}

impl RunningServer {
    /// Returns the address that serves FlowService, WorkerTaskService, and gRPC health.
    pub fn local_address(&self) -> SocketAddr {
        self.local_address
    }

    /// Returns the embedded dev object store for assertions while the server is running.
    #[cfg(feature = "test-util")]
    pub fn dev_object_store_for_test(&self) -> Option<Arc<s3mock::Store>> {
        self.dev_object_store_for_test.clone()
    }

    /// Returns the separate metrics address when metrics are enabled.
    pub fn metrics_address(&self) -> Option<SocketAddr> {
        self.metrics_address
    }

    /// Stops gracefully within 15 seconds and returns how serving ended.
    ///
    /// Returns [`ServerError::NodeStopped`] when the node had already stopped for another reason,
    /// such as a lost lease, and [`ServerError::ShutdownIncomplete`] when a shutdown step overran
    /// its budget or failed, such as a lease release that object storage rejected.
    pub async fn shutdown(self) -> Result<(), ServerError> {
        self.shutdown_request.cancel();
        self.wait().await
    }

    /// Waits until the server stops by itself, for example after losing its lease.
    pub async fn wait(mut self) -> Result<(), ServerError> {
        match (&mut self.serving).await {
            Ok(result) => result,
            Err(error) => Err(ServerError::ServingTaskFailed {
                detail: error.to_string(),
            }),
        }
    }

    /// Returns a token that stops the server when cancelled, for signal handlers.
    pub fn shutdown_token(&self) -> CancellationToken {
        self.shutdown_request.clone()
    }

    /// Moves the lease watchdog out, so it keeps running while the async runtime shuts down.
    pub(crate) fn take_lease_watchdog(&mut self) -> Option<LeaseValidityWatchdog> {
        self.lease_watchdog.take()
    }
}

impl EngineStart {
    fn into_hot_standby(self) -> Option<HotStandby> {
        match self {
            Self::Cold => None,
            Self::HotStandby(standby) => Some(standby),
        }
    }
}

impl Drop for RunningServer {
    fn drop(&mut self) {
        self.serving.abort();
    }
}

/// Starts a server from a validated configuration.
///
/// The server stops `embedded_s3`, the dev mode emulator, last.
pub(crate) async fn start_server(
    config: &Config,
    embedded_s3: Option<EmbeddedS3>,
    cancellation: &mut watch::Receiver<bool>,
    lease_expiry_response: LeaseExpiryResponse,
) -> Result<Option<RunningServer>, ServerError> {
    // Only a held dev data directory lock proves that the previous lease holder has stopped.
    let should_take_over_held_lease = embedded_s3
        .as_ref()
        .is_some_and(EmbeddedS3::has_exclusive_data_directory);
    let components = ComponentConfigs::from_config(config, should_take_over_held_lease)?;
    let authenticator =
        public_api_authentication::public_api_authenticator(&components.authenticators)?;
    let listener = TcpListener::bind(config.api.listen)
        .await
        .map_err(|source| ServerError::Listen {
            address: config.api.listen,
            source,
        })?;
    let local_address = listener
        .local_addr()
        .map_err(|source| ServerError::Listen {
            address: config.api.listen,
            source,
        })?;
    let (registry, metrics) = NodeMetrics::registered(
        config.local_storage.directory.clone(),
        config.cluster.partition_count as usize,
    );
    let metrics = Arc::new(metrics);
    metrics.set_role(dexzd_metrics::NodeRole::Starting);
    let metrics_server =
        match MetricsServer::start(&config.metrics.listen, Arc::new(registry)).await {
            Ok(server) => server,
            Err(error) => {
                if let Some(embedded_s3) = embedded_s3 {
                    embedded_s3.shutdown().await;
                }
                return Err(error);
            }
        };
    let metrics_address = metrics_server.as_ref().map(MetricsServer::address);
    let lease_validity_deadline = LeaseValidityDeadline::disarmed();
    let (health, _first_health_server) = HealthStatus::new(lease_validity_deadline.clone()).await;
    let (serving_instance_administration, administration_requests) =
        LifecycleActiveNodeAdministration::new();
    let standby_instance_administration = serving_instance_administration.for_standby_instances();
    let grpc_settings = GrpcSettings {
        listener: Arc::new(listener),
        max_message_bytes: components.grpc_max_message_bytes,
        api_requests: metrics.api_requests.clone(),
        api_latency: metrics.api_latency.clone(),
        authenticator,
        serving_instance_administration: Arc::new(serving_instance_administration),
        standby_instance_administration: Arc::new(standby_instance_administration),
        health,
        lease_validity_deadline: lease_validity_deadline.clone(),
    };
    let grpc = GrpcServer::start(&grpc_settings, GrpcInstancePhase::Standby);
    let lease_watchdog = match lease_expiry_response {
        LeaseExpiryResponse::ExitProcess => {
            match LeaseValidityWatchdog::start(
                lease_validity_deadline.clone(),
                grpc_settings.health.clone(),
            ) {
                Ok(lease_watchdog) => Some(lease_watchdog),
                Err(source) => {
                    grpc.abort().await;
                    if let Some(metrics_server) = metrics_server {
                        metrics_server.shutdown().await;
                    }
                    if let Some(embedded_s3) = embedded_s3 {
                        embedded_s3.shutdown().await;
                    }
                    return Err(ServerError::LeaseWatchdog { source });
                }
            }
        }
        LeaseExpiryResponse::StopNodeOnly => None,
    };
    let started = Box::pin(NodeLifecycle::start(
        config,
        components,
        metrics,
        lease_validity_deadline,
        grpc_settings,
        grpc,
        administration_requests,
        cancellation,
    ))
    .await;
    let lifecycle = match started {
        Ok(Some(lifecycle)) => lifecycle,
        Ok(None) => {
            if let Some(metrics_server) = metrics_server {
                metrics_server.shutdown().await;
            }
            if let Some(embedded_s3) = embedded_s3 {
                embedded_s3.shutdown().await;
            }
            return Ok(None);
        }
        Err(error) => {
            if let Some(metrics_server) = metrics_server {
                metrics_server.shutdown().await;
            }
            if let Some(embedded_s3) = embedded_s3 {
                embedded_s3.shutdown().await;
            }
            return Err(error);
        }
    };
    #[cfg(feature = "test-util")]
    let dev_object_store_for_test = embedded_s3.as_ref().map(EmbeddedS3::store_for_test);
    let shutdown_request = CancellationToken::new();
    let serving =
        tokio::spawn(lifecycle.run(metrics_server, shutdown_request.clone(), embedded_s3));
    tracing::info!(address = %local_address, "dex-server-zd serving");
    Ok(Some(RunningServer {
        local_address,
        metrics_address,
        shutdown_request,
        serving,
        lease_watchdog,
        #[cfg(feature = "test-util")]
        dev_object_store_for_test,
    }))
}

/// How the engine of a serving period gets its local store.
enum EngineStart {
    /// Opens or restores the configured store once this process holds the lease.
    Cold,
    /// Takes over the store that this hot standby keeps current.
    HotStandby(HotStandby),
}

/// How the start of a serving period ended without a server error.
enum ServerNodeStart {
    Serving(Box<ServerNode>),
    /// Cancellation arrived before the lease; a hot standby still follows the log.
    Cancelled(Option<HotStandby>),
    /// An error arrived before the lease; the hot standby still follows the log.
    FailedBeforeLease {
        error: EngineStartError,
        standby: HotStandby,
    },
}

/// What the engine start returned, before the server maps it.
enum EngineStartOutcome {
    Serving(EngineNode),
    Cancelled(Option<HotStandby>),
    FailedBeforeLease {
        error: EngineStartError,
        standby: HotStandby,
    },
    /// A takeover outcome that a later engine added and this server does not handle.
    Unrecognized,
}

/// Why a serving period ended.
enum PeriodEnd {
    Shutdown,
    Failed(ServerError),
    StepDown(StepDownRequest),
}

/// How a serving period stops.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum PeriodStopKind {
    /// Stops the gRPC instance with its open calls, before the process exits.
    Shutdown,
    /// Keeps the gRPC instance, so the step down call gets its answer; public services refuse calls.
    StepDown,
}

/// The teardown of one stopped serving period.
struct PeriodStop {
    teardown: BoundedTeardown,
    /// Whether this process confirmed that it no longer holds the lease.
    has_released_lease: bool,
    /// Reports when the last holder of the period's local store lets it go and the store closes.
    local_store_close_waiter: LocalStoreCloseWaiter,
    /// Ends when the stopped instance's public calls end; they may still read the local store.
    public_calls_ended: std::pin::Pin<Box<dyn std::future::Future<Output = ()> + Send>>,
}

/// Every component of one serving period, in start order.
struct ServerNode {
    metrics: Arc<NodeMetrics>,
    engine: EngineNode,
    registry: FlowCoordinatorRegistry,
    effect_tasks: EffectTasksHandle,
    dispatch: WorkerDispatch,
    stream_store: Arc<StreamStore>,
    /// Moves into the gRPC gate when the period starts serving.
    flow_service: Option<DexFlowService>,
    search_projection: Option<SearchProjection>,
    attribute_sync: AttributeSyncService,
    held_lease: HeldNodeLease,
}

/// What the engine's command and effect hooks factory builds; captured so the node can own it after start.
struct ServerNodeStartupParts {
    registry: FlowCoordinatorRegistry,
    effect_tasks: EffectTasksHandle,
    partition_hasher: PartitionHasher,
}

impl ServerNode {
    /// Connects the period's stores, waits for the lease, and starts every component.
    async fn start(
        process: &ProcessParts,
        engine_start: EngineStart,
        engine_config: &EngineNodeConfig,
        cancellation: &mut watch::Receiver<bool>,
    ) -> Result<ServerNodeStart, ServerError> {
        let config = &process.config;
        let components = &process.components;
        let metrics = process.metrics.clone();
        let object_stores = &process.object_stores;
        let clock = process.clock.clone();
        let applier = process.applier.clone();
        let dispatch = WorkerDispatch::new_with_lease_validity_deadline(
            components.matching.clone(),
            clock.clone(),
            metrics.as_ref().clone(),
            process.lease_validity_deadline.clone(),
        );
        let stream_store = Arc::new(
            StreamStore::new(&components.stream_store)
                .map_err(|source| ServerError::StreamStore { source })?,
        );
        let partition_count = config.cluster.partition_count;
        let attribute_indexes = Arc::new(AttributeIndexes::new_with_registry_refresh(
            object_stores.primary.clone(),
            clock.clone(),
            (config.search.backend == SearchBackendKind::Postgres)
                .then(dexzd_cluster_metadata::AttributeIndexCapacity::postgres),
            if config.search.backend == SearchBackendKind::Postgres {
                dexzd_api::AttributeIndexRegistryRefresh::ExternalRegistrations
            } else {
                dexzd_api::AttributeIndexRegistryRefresh::LocalRegistrations
            },
        ));
        let sql_visibility_search = match config.search.backend {
            SearchBackendKind::Builtin => None,
            SearchBackendKind::Paradedb | SearchBackendKind::Postgres => match until_cancelled(
                SqlVisibilitySearch::connect(
                    &config.search,
                    partition_count,
                    &metrics,
                    attribute_indexes.clone(),
                ),
                cancellation,
            )
            .await
            {
                Some(connected) => Some(connected?),
                None => {
                    close_stream_store(&stream_store).await;
                    return Ok(ServerNodeStart::Cancelled(engine_start.into_hot_standby()));
                }
            },
        };
        let search_outbox_watermarks = sql_visibility_search
            .as_ref()
            .map(SqlVisibilitySearch::watermarks);
        let is_search_projection_enabled = sql_visibility_search.is_some();
        let attribute_stores = match until_cancelled(
            AttributeStores::connect(&config.attribute_store, &components.attribute_sync),
            cancellation,
        )
        .await
        {
            Some(connected) => connected?,
            None => {
                close_stream_store(&stream_store).await;
                return Ok(ServerNodeStart::Cancelled(engine_start.into_hot_standby()));
            }
        };
        let attribute_sync_waker = AttributeSyncWaker::new();
        let startup_parts: Arc<Mutex<Option<ServerNodeStartupParts>>> = Arc::new(Mutex::new(None));
        let dependencies = EngineNodeDependencies {
            metrics: metrics.clone(),
            object_store: object_stores.primary.clone(),
            clock: clock.clone(),
            applier: applier.clone(),
            id_source: process.id_source.clone(),
            lease_validity_deadline: process.lease_validity_deadline.clone(),
        };
        let command_and_effect_hooks_factory = |services: EngineServices| {
            let partition_hasher = services.partition_hasher.clone();
            let hard_delete_objects = HardDeleteObjects::new(
                object_stores.hard_delete.clone(),
                Arc::new(NodeMetaUploadLedger::new(
                    services.local_store.clone(),
                    services.node_meta_lock.clone(),
                )),
                clock.clone(),
                partition_hasher.clone(),
                services.id_source.clone(),
            );
            let (effect_runner, effect_tasks) = EffectRunner::new(
                &components.effects,
                EffectDependencies {
                    metrics: metrics.clone(),
                    dispatcher: Arc::new(dispatch.clone()),
                    hard_delete_objects: hard_delete_objects.clone(),
                    partition_hasher: partition_hasher.clone(),
                    clock: clock.clone(),
                    validation_config: components.validation().clone(),
                    stream_store: stream_store.clone(),
                    id_source: services.id_source.clone(),
                    attribute_sync_waker: attribute_sync_waker.clone(),
                },
            );
            let mode_rebuilder = SearchProjectionModeRebuilder::new(
                Arc::new(effect_runner.clone()),
                services.committer.clone(),
                metrics.clone(),
                partition_hasher.clone(),
                is_search_projection_enabled,
            );
            let registry = FlowCoordinatorRegistry::new(
                &components.flow_coordination,
                FlowCoordinatorDependencies {
                    hasher: partition_hasher.clone(),
                    committer: Arc::new(services.committer),
                    applier: applier.clone(),
                    effects: Arc::new(effect_runner.clone()),
                    rpc_invoker: Arc::new(PullWorkerRpcInvoker::new(dispatch.clone())),
                    hard_delete_reader: Arc::new(hard_delete_objects.clone()),
                    hard_delete_uploader: Arc::new(hard_delete_objects),
                    id_source: services.id_source,
                },
            );
            let effect_tasks = effect_tasks.start(Arc::new(registry.clone()));
            let hooks = EngineCommandAndEffectHooks {
                committed_commands: Arc::new(RegistryCommittedCommandSink::new(
                    registry.clone(),
                    search_outbox_watermarks.clone(),
                )),
                rebuilder: Arc::new(mode_rebuilder),
            };
            *startup_parts
                .lock()
                .expect("runtime parts lock is poisoned only if a holder panicked") =
                Some(ServerNodeStartupParts {
                    registry,
                    effect_tasks,
                    partition_hasher,
                });
            hooks
        };
        let started = match engine_start {
            EngineStart::Cold => EngineNode::start_until_cancelled(
                engine_config,
                dependencies,
                command_and_effect_hooks_factory,
                cancellation,
            )
            .await
            .map(|engine| match engine {
                Some(engine) => EngineStartOutcome::Serving(engine),
                None => EngineStartOutcome::Cancelled(None),
            }),
            EngineStart::HotStandby(standby) => {
                match EngineNode::take_over_from_hot_standby_until_cancelled(
                    engine_config,
                    dependencies,
                    command_and_effect_hooks_factory,
                    standby,
                    cancellation,
                )
                .await
                {
                    Ok(HotStandbyTakeover::Serving(engine)) => {
                        Ok(EngineStartOutcome::Serving(engine))
                    }
                    Ok(HotStandbyTakeover::Cancelled(standby)) => {
                        Ok(EngineStartOutcome::Cancelled(Some(standby)))
                    }
                    Ok(_) => Ok(EngineStartOutcome::Unrecognized),
                    // Before the lease the standby comes back and keeps following.
                    Err(failure) => match failure.standby {
                        Some(standby) => Ok(EngineStartOutcome::FailedBeforeLease {
                            error: failure.error,
                            standby,
                        }),
                        None => Err(failure.error),
                    },
                }
            }
        };
        let parts = startup_parts
            .lock()
            .expect("runtime parts lock is poisoned only if a holder panicked")
            .take();
        let engine = match started {
            Ok(EngineStartOutcome::Serving(engine)) => engine,
            Ok(EngineStartOutcome::Cancelled(standby)) => {
                stop_startup_parts(parts).await;
                close_stream_store(&stream_store).await;
                return Ok(ServerNodeStart::Cancelled(standby));
            }
            Ok(EngineStartOutcome::FailedBeforeLease { error, standby }) => {
                stop_startup_parts(parts).await;
                close_stream_store(&stream_store).await;
                return Ok(ServerNodeStart::FailedBeforeLease { error, standby });
            }
            Ok(EngineStartOutcome::Unrecognized) => {
                stop_startup_parts(parts).await;
                close_stream_store(&stream_store).await;
                return Err(ServerError::ServingTaskFailed {
                    detail: "the engine ended a takeover in a way this server does not handle"
                        .to_owned(),
                });
            }
            Err(source) => {
                stop_startup_parts(parts).await;
                close_stream_store(&stream_store).await;
                if matches!(
                    &source,
                    EngineStartError::Stopped {
                        reason: NodeStopReason::Shutdown
                    }
                ) && *cancellation.borrow()
                {
                    return Ok(ServerNodeStart::Cancelled(None));
                }
                return Err(ServerError::EngineStart { source });
            }
        };
        let ServerNodeStartupParts {
            registry,
            effect_tasks,
            partition_hasher,
        } = parts.expect("the engine calls the runtime factory before it starts serving");
        let (search_backend, search_projection) = match sql_visibility_search {
            Some(sql_visibility_search) => (
                Some(sql_visibility_search.search_backend()),
                Some(sql_visibility_search.start_projection(
                    SearchProjectionStart {
                        committer: engine.committer().clone(),
                        partition_hasher: partition_hasher.clone(),
                        node_epoch: engine.node_epoch(),
                        metrics: metrics.clone(),
                    },
                    engine.wait_for_stop(),
                )),
            ),
            None => match builtin_search_backend(config, &engine, partition_count) {
                Ok(search_backend) => (Some(search_backend), None),
                Err(error) => {
                    registry.close_long_polls();
                    stop_rpc_tasks(&registry).await;
                    effect_tasks.shutdown().await;
                    if let Err(release_error) = engine.shutdown().await {
                        tracing::error!(
                            error = %release_error,
                            "the node lease stays held after a failed start"
                        );
                    }
                    close_stream_store(&stream_store).await;
                    return Err(error);
                }
            },
        };
        let attribute_sync = attribute_stores.start(AttributeSyncStart {
            engine: &engine,
            node_id: &config.cluster.node_id,
            flow_effect_operations: Arc::new(registry.clone()),
            partition_hasher,
            clock: clock.clone(),
            metrics: metrics.clone(),
            waker: attribute_sync_waker,
        });
        let flow_service = DexFlowService::new(
            &components.flow_service,
            FlowServiceDependencies {
                flow_operations: Arc::new(registry.clone()),
                stream_operations: StreamOperations::new(
                    stream_store.clone(),
                    components.flow_service.max_wait,
                ),
                attribute_indexes,
                search_backend,
            },
        );
        let held_lease = HeldNodeLease {
            node_id: config.cluster.node_id.clone(),
            node_epoch: engine.node_epoch(),
            holder_process_id: process.id_source.last_lease_holder_process_id(),
        };
        Ok(ServerNodeStart::Serving(Box::new(Self {
            metrics,
            engine,
            registry,
            effect_tasks,
            dispatch,
            stream_store,
            flow_service: Some(flow_service),
            search_projection,
            attribute_sync,
            held_lease,
        })))
    }

    /// Returns the node lease that this period holds.
    fn held_lease(&self) -> &HeldNodeLease {
        &self.held_lease
    }

    /// Returns which local store this period serves from.
    fn store_source(&self) -> dexzd_metrics::TakeoverStoreSource {
        if self.engine.is_serving_hot_standby_store() {
            dexzd_metrics::TakeoverStoreSource::HotStandby
        } else {
            dexzd_metrics::TakeoverStoreSource::Restored
        }
    }

    /// Serves until shutdown, a node stop, or a step down; answers describe and epoch-mismatched step downs meanwhile.
    async fn serve_until_stopped(
        &mut self,
        grpc: &mut GrpcServer,
        shutdown_request: &CancellationToken,
        administration_requests: &mut mpsc::Receiver<AdministrationRequest>,
    ) -> PeriodEnd {
        if let Some(flow_service) = self.flow_service.take() {
            let worker_task_service =
                WorkerTaskServiceServer::new(self.dispatch.service_for_tenant(DEFAULT_TENANT_ID))
                    .max_decoding_message_size(grpc.max_message_bytes)
                    .max_encoding_message_size(grpc.max_message_bytes);
            grpc.flow_service_gate
                .activate(flow_service.into_server(grpc.max_message_bytes));
            grpc.worker_task_service_gate.activate(worker_task_service);
            grpc.health.set_serving().await;
        }
        let mut node_stopped = std::pin::pin!(self.engine.wait_for_stop());
        let registry_halted = self.registry.halted();
        loop {
            let request = tokio::select! {
                () = shutdown_request.cancelled() => return PeriodEnd::Shutdown,
                reason = &mut node_stopped => {
                    return PeriodEnd::Failed(ServerError::NodeStopped { reason });
                }
                () = registry_halted.cancelled() => {
                    return PeriodEnd::Failed(ServerError::NodeStopped {
                        reason: NodeStopReason::CommitPipelineFailed {
                            detail: "the Flow runtime registry halted".to_owned(),
                        },
                    });
                }
                served = &mut grpc.task => return PeriodEnd::Failed(match served {
                    Ok(Ok(())) => ServerError::ServingTaskFailed {
                        detail: "the gRPC server stopped unexpectedly".to_owned(),
                    },
                    Ok(Err(source)) => ServerError::Serve { source },
                    Err(error) => ServerError::ServingTaskFailed { detail: error.to_string() },
                }),
                Some(request) = administration_requests.recv() => request,
            };
            if let Some(step_down) = request.answer_description(&self.held_lease)
                && let Some(step_down) = step_down.check_precondition(&self.held_lease)
            {
                // That instance retires within 250 ms of a takeover and would cut a longer answer.
                if step_down.is_on_standby_instance() {
                    step_down.refuse_not_active(STANDBY_INSTANCE_STEP_DOWN_REFUSAL);
                    continue;
                }
                // The select picks a ready branch at random, so a stop may race the step down.
                let stop_reason = self.engine.stop_reason().or_else(|| {
                    registry_halted
                        .is_cancelled()
                        .then(|| NodeStopReason::CommitPipelineFailed {
                            detail: "the Flow runtime registry halted".to_owned(),
                        })
                });
                if let Some(reason) = stop_reason {
                    step_down.report_internal_failure(format!("the node stopped: {reason}"));
                    return PeriodEnd::Failed(ServerError::NodeStopped { reason });
                }
                return PeriodEnd::StepDown(step_down);
            }
        }
    }

    /// Stops serving and every component, then releases the lease; only a shutdown sends `grpc` GOAWAY.
    ///
    /// Open RPC tasks may finish before the release; the lease gate refuses other open calls after it.
    async fn stop(self, grpc: &mut GrpcServer, kind: PeriodStopKind) -> PeriodStop {
        let Self {
            metrics,
            engine,
            registry,
            effect_tasks,
            dispatch,
            stream_store,
            flow_service: _,
            search_projection,
            attribute_sync,
            held_lease: _,
        } = self;
        let local_store_close_waiter = engine.local_store_close_waiter();
        metrics.set_role(dexzd_metrics::NodeRole::Draining);
        let mut teardown = BoundedTeardown::start();
        let drain_deadline = tokio::time::Instant::now() + GRPC_DRAIN_BUDGET;
        grpc.health.set_not_serving().await;
        dispatch.shutdown();
        registry.close_long_polls();
        registry.close_rpc_admission();
        match kind {
            PeriodStopKind::Shutdown => grpc.stop.cancel(),
            PeriodStopKind::StepDown => grpc.deactivate_public_services(),
        }
        // Closing it now ends waiting ReadStream calls, which would otherwise wait out the drain.
        teardown
            .run(
                "Stream Store",
                STREAM_STORE_CLOSE_BUDGET,
                close_stream_store(&stream_store),
            )
            .await;
        let public_calls_ended = Box::pin(grpc.public_calls_ended());
        if tokio::time::timeout_at(drain_deadline, registry.wait_for_rpc_tasks())
            .await
            .is_err()
        {
            tracing::warn!("open calls did not finish in time; closing them");
        }
        registry.cancel_rpc_tasks();
        teardown
            .run(
                "RPC tasks",
                RPC_TASKS_STOP_BUDGET,
                registry.wait_for_rpc_tasks(),
            )
            .await;
        teardown
            .run(
                "effect tasks",
                EFFECT_TASKS_STOP_BUDGET,
                effect_tasks.shutdown(),
            )
            .await;
        let (has_search_projection_stopped, has_attribute_sync_stopped) = tokio::join!(
            async {
                match search_projection {
                    Some(projection) => projection.shutdown(SEARCH_PROJECTION_STOP_BUDGET).await,
                    None => true,
                }
            },
            attribute_sync.shutdown(SEARCH_PROJECTION_STOP_BUDGET),
        );
        if !has_search_projection_stopped {
            teardown.record_overrun("search projection", SEARCH_PROJECTION_STOP_BUDGET);
        }
        if !has_attribute_sync_stopped {
            teardown.record_overrun("Attribute Sync projector", SEARCH_PROJECTION_STOP_BUDGET);
        }
        let has_released_lease = teardown
            .run_fallible("engine", ENGINE_STOP_BUDGET, engine.shutdown())
            .await;
        // The lease gate refuses calls now, so waiting for open ones keeps no client from a successor.
        if kind == PeriodStopKind::Shutdown && !grpc.task.is_finished() {
            match tokio::time::timeout_at(drain_deadline, &mut grpc.task).await {
                Ok(Ok(Ok(()))) => {}
                Ok(Ok(Err(error))) => tracing::warn!(%error, "gRPC server stopped with an error"),
                Ok(Err(error)) => tracing::warn!(%error, "gRPC server task failed"),
                Err(_) => {
                    tracing::warn!("open calls did not finish in time; closing them");
                    grpc.task.abort();
                }
            }
        }
        PeriodStop {
            teardown,
            has_released_lease,
            local_store_close_waiter,
            public_calls_ended,
        }
    }
}

/// Returns the longer of two budgets, usable in a constant.
const fn longer_duration(first: Duration, second: Duration) -> Duration {
    if first.as_nanos() >= second.as_nanos() {
        first
    } else {
        second
    }
}

/// Returns what the earlier steps left of [`SHUTDOWN_BUDGET`], and at least the embedded S3 minimum.
fn embedded_s3_stop_budget(teardown: &BoundedTeardown) -> Duration {
    teardown.remaining_budget(SHUTDOWN_BUDGET, EMBEDDED_S3_MINIMUM_STOP_BUDGET)
}

/// Returns builtin SearchFlows over committed views of the engine.
fn builtin_search_backend(
    config: &Config,
    engine: &EngineNode,
    partition_count: u32,
) -> Result<Arc<dyn SearchBackend>, ServerError> {
    let scan_budget =
        usize::try_from(config.search.builtin_scan_budget_documents).map_err(|_| {
            ServerError::ConfigValueOutOfRange {
                field: "search.builtinScanBudgetDocuments",
            }
        })?;
    let backend = BuiltinSearchBackend::with_scan_budget(
        Arc::new(CommittedSearchViews {
            committer: engine.committer().clone(),
        }),
        partition_count,
        scan_budget,
    )
    .map_err(|source| ServerError::SearchBackend { source })?;
    Ok(Arc::new(backend))
}

/// Stops what the hooks factory started when the engine start ended without serving.
async fn stop_startup_parts(parts: Option<ServerNodeStartupParts>) {
    if let Some(parts) = parts {
        parts.registry.close_long_polls();
        stop_rpc_tasks(&parts.registry).await;
        parts.effect_tasks.shutdown().await;
    }
}

async fn stop_rpc_tasks(registry: &FlowCoordinatorRegistry) {
    registry.cancel_rpc_tasks();
    if tokio::time::timeout(RPC_TASKS_STOP_BUDGET, registry.wait_for_rpc_tasks())
        .await
        .is_err()
    {
        tracing::warn!("RPC tasks did not stop within their shutdown budget");
    }
}

async fn close_stream_store(stream_store: &StreamStore) {
    if let Err(error) = stream_store.close().await {
        tracing::warn!(%error, "Stream Store did not close cleanly");
    }
}

/// Returns the output of `operation`, or `None` when cancellation arrives first.
async fn until_cancelled<T>(
    operation: impl std::future::Future<Output = T>,
    cancellation: &mut watch::Receiver<bool>,
) -> Option<T> {
    tokio::select! {
        biased;
        output = operation => Some(output),
        () = wait_for_cancellation(cancellation) => None,
    }
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

/// Opens committed state views of the engine for builtin SearchFlows.
struct CommittedSearchViews {
    committer: CommandCommitter,
}

impl CommittedSearchViewProvider for CommittedSearchViews {
    fn committed_view(&self) -> Result<Arc<dyn FlowStateReader + Send + Sync>, SearchError> {
        let view = self
            .committer
            .committed_view()
            .map_err(|error| SearchError::Unavailable(error.to_string()))?;
        Ok(Arc::new(view))
    }
}
