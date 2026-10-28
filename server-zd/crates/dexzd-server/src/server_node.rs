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
mod lease_validity_watchdog;
mod public_api_authentication;
mod search_projection;
#[cfg(test)]
mod startup_cancellation_tests;

use std::convert::Infallible;
use std::net::SocketAddr;
use std::sync::{Arc, Mutex, RwLock};
use std::task::{Context, Poll};
use std::time::Duration;

use dexzd_api::{
    AttributeIndexes, DexFlowService, FlowServiceDependencies, HealthStatus,
    PublicApiAuthenticator, RequestMetricsLayer, StreamOperations,
};
use dexzd_apply::FlowStateReader;
use dexzd_clock::{SystemWallClock, WallClock};
use dexzd_cluster_metadata::LeaseValidityDeadline;
use dexzd_engine::{
    AttributeSyncWaker, CommandApplier, CommandCommitter, DeterministicCommandApplier,
    EffectDependencies, EffectRunner, EffectTasksHandle, EngineCommandAndEffectHooks, EngineNode,
    EngineNodeDependencies, EngineServices, EngineStartError, FlowCoordinatorDependencies,
    FlowCoordinatorRegistry, HardDeleteObjects, NodeMetaUploadLedger, NodeStopReason,
    SystemEngineIdSource,
};
use dexzd_keyspace::PartitionHasher;
use dexzd_metrics::NodeMetrics;
use dexzd_protocol::dex::flow_service_server::FlowServiceServer;
use dexzd_protocol::dex::worker_task_service_server::WorkerTaskServiceServer;
use dexzd_search::builtin::{BuiltinSearchBackend, CommittedSearchViewProvider};
use dexzd_search::{SearchBackend, SearchError};
use dexzd_stream_store::StreamStore;
use dexzd_worker_dispatch::{WorkerDispatch, WorkerTaskService};
use tokio::net::TcpListener;
use tokio::sync::watch;
use tokio::task::JoinHandle;
use tokio_util::sync::CancellationToken;
use tonic::Status;
use tonic::body::Body;
use tonic::codegen::{BoxFuture, Service, http};
use tonic::server::NamedService;

use crate::ServerError;
use crate::component_configs::ComponentConfigs;
use crate::config::{Config, SearchBackend as SearchBackendKind};
use crate::embedded_s3::EmbeddedS3;
use crate::metrics_server::MetricsServer;
use crate::object_stores::ObjectStores;
use crate::registry_committed_command_sink::RegistryCommittedCommandSink;
use crate::worker_rpc_invoker::PullWorkerRpcInvoker;

pub(crate) use search_projection::CommittedSearchFrames;

use attribute_sync::{AttributeStores, AttributeSyncService, AttributeSyncStart};
use bounded_teardown::BoundedTeardown;
pub(crate) use lease_validity_watchdog::LeaseValidityWatchdog;
use search_projection::{ParadeDbSearch, SearchProjection};

/// Part of the budget for open calls, so the engine still stops cleanly after a stuck call.
const GRPC_DRAIN_BUDGET: Duration = Duration::from_secs(4);
/// Shutdown budgets of the later steps; with the gRPC drain they total 15 seconds.
const RPC_TASKS_STOP_BUDGET: Duration = Duration::from_millis(500);
const EFFECT_TASKS_STOP_BUDGET: Duration = Duration::from_secs(2);
/// Also bounds the Attribute Sync projector, which stops at the same time.
const SEARCH_PROJECTION_STOP_BUDGET: Duration = Duration::from_secs(1);
/// Covers the engine's three-second task stop and its lease release.
const ENGINE_STOP_BUDGET: Duration =
    EngineNode::SHUTDOWN_BUDGET.saturating_add(Duration::from_millis(500));
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
/// gRPC keepalive, equal to the Go Server's.
const KEEPALIVE_INTERVAL: Duration = Duration::from_secs(20);
const KEEPALIVE_TIMEOUT: Duration = Duration::from_secs(10);
/// Tenant that serves pull Workers until authentication assigns tenants.
const DEFAULT_TENANT_ID: u64 = 0;

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
    let grpc = GrpcServer::start(
        listener,
        components.grpc_max_message_bytes,
        metrics.api_requests.clone(),
        metrics.api_latency.clone(),
        authenticator.as_ref(),
    )
    .await;
    let lease_validity_deadline = LeaseValidityDeadline::disarmed();
    let lease_watchdog = match lease_expiry_response {
        LeaseExpiryResponse::ExitProcess => {
            match LeaseValidityWatchdog::start(lease_validity_deadline.clone(), grpc.health.clone())
            {
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
    let started = ServerNode::start(
        config,
        &components,
        metrics.clone(),
        lease_validity_deadline,
        cancellation,
    )
    .await;
    let node = match started {
        Ok(Some(node)) => node,
        Ok(None) => {
            grpc.abort().await;
            if let Some(metrics_server) = metrics_server {
                metrics_server.shutdown().await;
            }
            if let Some(embedded_s3) = embedded_s3 {
                embedded_s3.shutdown().await;
            }
            return Ok(None);
        }
        Err(error) => {
            grpc.abort().await;
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
        tokio::spawn(node.serve(grpc, metrics_server, shutdown_request.clone(), embedded_s3));
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

struct GrpcServer {
    health: HealthStatus,
    flow_service_gate: StartupService<FlowServiceServer<DexFlowService>>,
    worker_task_service_gate: StartupService<WorkerTaskServiceServer<WorkerTaskService>>,
    max_message_bytes: usize,
    stop: CancellationToken,
    task: JoinHandle<Result<(), tonic::transport::Error>>,
}

impl GrpcServer {
    async fn start(
        listener: TcpListener,
        max_message_bytes: usize,
        api_requests: dexzd_metrics::ApiRequests,
        api_latency: dexzd_metrics::ApiLatency,
        authenticator: Option<&Arc<PublicApiAuthenticator>>,
    ) -> Self {
        let (health, health_server) = HealthStatus::new().await;
        let flow_service_gate = StartupService::<FlowServiceServer<DexFlowService>>::new();
        let worker_task_service_gate =
            StartupService::<WorkerTaskServiceServer<WorkerTaskService>>::new();
        let stop = CancellationToken::new();
        let stopped = stop.clone();
        let task = tokio::spawn(
            tonic::transport::Server::builder()
                .http2_keepalive_interval(Some(KEEPALIVE_INTERVAL))
                .http2_keepalive_timeout(Some(KEEPALIVE_TIMEOUT))
                .layer(RequestMetricsLayer::new(api_requests, api_latency))
                .layer(tower_layer::layer_fn(
                    client_deadline::client_deadline_layer,
                ))
                .add_service(health_server)
                .add_service(public_api_authentication::authenticated(
                    flow_service_gate.clone(),
                    authenticator,
                ))
                .add_service(public_api_authentication::authenticated(
                    worker_task_service_gate.clone(),
                    authenticator,
                ))
                .serve_with_incoming_shutdown(
                    futures::stream::unfold(listener, |listener| async move {
                        let accepted = listener.accept().await.map(|(stream, _)| {
                            // Without TCP_NODELAY, Nagle plus delayed ACK stalls small replies for about 40 ms.
                            if let Err(error) = stream.set_nodelay(true) {
                                tracing::warn!(%error, "failed to set TCP_NODELAY on a gRPC connection");
                            }
                            stream
                        });
                        Some((accepted, listener))
                    }),
                    async move { stopped.cancelled().await },
                ),
        );
        Self {
            health,
            flow_service_gate,
            worker_task_service_gate,
            max_message_bytes,
            stop,
            task,
        }
    }

    async fn abort(mut self) {
        self.stop.cancel();
        self.task.abort();
        match (&mut self.task).await {
            Ok(Ok(())) => {}
            Ok(Err(error)) => tracing::warn!(%error, "startup gRPC server failed"),
            Err(join_error) if join_error.is_cancelled() => {}
            Err(join_error) => {
                tracing::warn!(%join_error, "startup gRPC server task failed");
            }
        }
    }
}

impl Drop for GrpcServer {
    fn drop(&mut self) {
        self.stop.cancel();
        self.task.abort();
    }
}

#[derive(Clone)]
struct StartupService<S> {
    serving: Arc<RwLock<Option<S>>>,
}

impl<S> StartupService<S> {
    fn new() -> Self {
        Self {
            serving: Arc::new(RwLock::new(None)),
        }
    }

    fn activate(&self, service: S) {
        *self
            .serving
            .write()
            .expect("startup service lock is poisoned only if a holder panicked") = Some(service);
    }
}

impl<S: NamedService> NamedService for StartupService<S> {
    const NAME: &'static str = S::NAME;
}

impl<S> Service<http::Request<Body>> for StartupService<S>
where
    S: Service<http::Request<Body>, Response = http::Response<Body>, Error = Infallible>
        + Clone
        + Send
        + Sync
        + 'static,
    S::Future: Send + 'static,
{
    type Response = http::Response<Body>;
    type Error = Infallible;
    type Future = BoxFuture<Self::Response, Self::Error>;

    fn poll_ready(&mut self, _context: &mut Context<'_>) -> Poll<Result<(), Self::Error>> {
        Poll::Ready(Ok(()))
    }

    fn call(&mut self, request: http::Request<Body>) -> Self::Future {
        let serving = self
            .serving
            .read()
            .expect("startup service lock is poisoned only if a holder panicked")
            .clone();
        Box::pin(async move {
            match serving {
                Some(mut service) => service.call(request).await,
                None => Ok(Status::unavailable("the node is starting").into_http()),
            }
        })
    }
}

/// Every component of one node, in start order.
struct ServerNode {
    metrics: Arc<NodeMetrics>,
    engine: EngineNode,
    registry: FlowCoordinatorRegistry,
    effect_tasks: EffectTasksHandle,
    dispatch: WorkerDispatch,
    stream_store: Arc<StreamStore>,
    flow_service: DexFlowService,
    search_projection: Option<SearchProjection>,
    attribute_sync: AttributeSyncService,
}

/// What the engine's command and effect hooks factory builds; captured so the node can own it after start.
struct ServerNodeStartupParts {
    registry: FlowCoordinatorRegistry,
    effect_tasks: EffectTasksHandle,
    partition_hasher: PartitionHasher,
}

impl ServerNode {
    async fn start(
        config: &Config,
        components: &ComponentConfigs,
        metrics: Arc<NodeMetrics>,
        lease_validity_deadline: LeaseValidityDeadline,
        cancellation: &mut watch::Receiver<bool>,
    ) -> Result<Option<Self>, ServerError> {
        // A cancelled startup check may leave its probe object behind.
        let object_stores = match until_cancelled(
            ObjectStores::connect(
                &config.object_store,
                config.commit.multipart_threshold_bytes,
                metrics.as_ref(),
            ),
            cancellation,
        )
        .await
        {
            Some(connected) => connected?,
            None => return Ok(None),
        };
        let clock: Arc<dyn WallClock> = Arc::new(SystemWallClock);
        let applier: Arc<dyn CommandApplier> = Arc::new(DeterministicCommandApplier);
        let dispatch = WorkerDispatch::new_with_metrics(
            components.matching.clone(),
            clock.clone(),
            metrics.as_ref().clone(),
        );
        let stream_store = Arc::new(
            StreamStore::new(&components.stream_store)
                .map_err(|source| ServerError::StreamStore { source })?,
        );
        let partition_count = config.cluster.partition_count;
        let parade_db_search = match config.search.backend {
            SearchBackendKind::Builtin => None,
            SearchBackendKind::Paradedb => {
                match until_cancelled(ParadeDbSearch::connect(&config.search), cancellation).await {
                    Some(connected) => Some(connected?),
                    None => {
                        close_stream_store(&stream_store).await;
                        return Ok(None);
                    }
                }
            }
        };
        let committed_search_frames = parade_db_search
            .as_ref()
            .map(ParadeDbSearch::committed_frames);
        let attribute_stores = match until_cancelled(
            AttributeStores::connect(&config.attribute_store, &components.attribute_sync),
            cancellation,
        )
        .await
        {
            Some(connected) => connected?,
            None => {
                close_stream_store(&stream_store).await;
                return Ok(None);
            }
        };
        let attribute_sync_waker = AttributeSyncWaker::new();
        let startup_parts: Arc<Mutex<Option<ServerNodeStartupParts>>> = Arc::new(Mutex::new(None));
        let started = EngineNode::start_until_cancelled(
            &components.engine_node,
            EngineNodeDependencies {
                metrics: metrics.clone(),
                object_store: object_stores.primary.clone(),
                clock: clock.clone(),
                applier: applier.clone(),
                id_source: Arc::new(SystemEngineIdSource),
                lease_validity_deadline,
            },
            |services: EngineServices| {
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
                        committed_search_frames.clone(),
                    )),
                    rebuilder: Arc::new(effect_runner),
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
            },
            cancellation,
        )
        .await;
        let parts = startup_parts
            .lock()
            .expect("runtime parts lock is poisoned only if a holder panicked")
            .take();
        let engine = match started {
            Ok(Some(engine)) => engine,
            Ok(None) => {
                close_stream_store(&stream_store).await;
                return Ok(None);
            }
            Err(source) => {
                if let Some(parts) = parts {
                    parts.registry.close_long_polls();
                    stop_rpc_tasks(&parts.registry).await;
                    parts.effect_tasks.shutdown().await;
                }
                close_stream_store(&stream_store).await;
                if matches!(
                    &source,
                    EngineStartError::Stopped {
                        reason: NodeStopReason::Shutdown
                    }
                ) && *cancellation.borrow()
                {
                    return Ok(None);
                }
                return Err(ServerError::EngineStart { source });
            }
        };
        let ServerNodeStartupParts {
            registry,
            effect_tasks,
            partition_hasher,
        } = parts.expect("the engine calls the runtime factory before it starts serving");
        let (search_backend, search_projection) = match parade_db_search {
            Some(parade_db_search) => (
                Some(parade_db_search.search_backend()),
                Some(parade_db_search.start_projection(
                    engine.committer().clone(),
                    partition_count,
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
                attribute_indexes: Arc::new(AttributeIndexes::new(
                    object_stores.primary.clone(),
                    clock,
                )),
                search_backend,
            },
        );
        Ok(Some(Self {
            metrics,
            engine,
            registry,
            effect_tasks,
            dispatch,
            stream_store,
            flow_service,
            search_projection,
            attribute_sync,
        }))
    }

    /// Serves until shutdown is requested or the node stops, then tears everything down.
    async fn serve(
        self,
        grpc: GrpcServer,
        metrics_server: Option<MetricsServer>,
        shutdown_request: CancellationToken,
        embedded_s3: Option<EmbeddedS3>,
    ) -> Result<(), ServerError> {
        let (outcome, mut teardown) = self
            .serve_and_stop_node(grpc, metrics_server, shutdown_request)
            .await;
        // Every node component and its S3 client is dropped before the emulator stops.
        if let Some(embedded_s3) = embedded_s3 {
            teardown
                .run(
                    "embedded S3",
                    embedded_s3_stop_budget(&teardown),
                    embedded_s3.shutdown(),
                )
                .await;
        }
        teardown.finish(outcome)
    }

    /// Serves until shutdown is requested or the node stops, then stops and drops every node component.
    ///
    /// Returns how serving ended and the teardown, whose steps so far stay within their budgets.
    async fn serve_and_stop_node(
        self,
        mut grpc: GrpcServer,
        metrics_server: Option<MetricsServer>,
        shutdown_request: CancellationToken,
    ) -> (Result<(), ServerError>, BoundedTeardown) {
        let Self {
            metrics,
            engine,
            registry,
            effect_tasks,
            dispatch,
            stream_store,
            flow_service,
            search_projection,
            attribute_sync,
        } = self;
        let worker_task_service =
            WorkerTaskServiceServer::new(dispatch.service_for_tenant(DEFAULT_TENANT_ID))
                .max_decoding_message_size(grpc.max_message_bytes)
                .max_encoding_message_size(grpc.max_message_bytes);
        grpc.flow_service_gate
            .activate(flow_service.into_server(grpc.max_message_bytes));
        grpc.worker_task_service_gate.activate(worker_task_service);
        grpc.health.set_serving().await;
        let node_stopped = engine.wait_for_stop();
        let registry_halted = registry.halted();
        let outcome = tokio::select! {
            () = shutdown_request.cancelled() => Ok(()),
            reason = node_stopped => Err(ServerError::NodeStopped { reason }),
            () = registry_halted.cancelled() => Err(ServerError::NodeStopped {
                reason: NodeStopReason::CommitPipelineFailed {
                    detail: "the Flow runtime registry halted".to_owned(),
                },
            }),
            served = &mut grpc.task => Err(match served {
                Ok(Ok(())) => ServerError::ServingTaskFailed {
                    detail: "the gRPC server stopped unexpectedly".to_owned(),
                },
                Ok(Err(source)) => ServerError::Serve { source },
                Err(error) => ServerError::ServingTaskFailed { detail: error.to_string() },
            }),
        };
        match &outcome {
            Ok(()) => tracing::info!("dex-server-zd shutting down"),
            Err(error) => tracing::error!(%error, "dex-server-zd stopping after a failure"),
        }
        metrics.set_role(dexzd_metrics::NodeRole::Draining);
        let mut teardown = BoundedTeardown::start();
        grpc.health.set_not_serving().await;
        dispatch.shutdown();
        registry.close_long_polls();
        registry.close_rpc_admission();
        grpc.stop.cancel();
        let drain = async {
            tokio::join!(
                async {
                    if !grpc.task.is_finished() {
                        match (&mut grpc.task).await {
                            Ok(Ok(())) => {}
                            Ok(Err(error)) => {
                                tracing::warn!(%error, "gRPC server stopped with an error")
                            }
                            Err(error) => tracing::warn!(%error, "gRPC server task failed"),
                        }
                    }
                },
                registry.wait_for_rpc_tasks(),
            );
        };
        if tokio::time::timeout(GRPC_DRAIN_BUDGET, drain)
            .await
            .is_err()
        {
            tracing::warn!("open calls did not finish in time; closing them");
            registry.cancel_rpc_tasks();
            grpc.task.abort();
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
        teardown
            .run_fallible("engine", ENGINE_STOP_BUDGET, engine.shutdown())
            .await;
        if let Some(metrics_server) = metrics_server {
            metrics_server.shutdown().await;
        }
        teardown
            .run(
                "Stream Store",
                STREAM_STORE_CLOSE_BUDGET,
                close_stream_store(&stream_store),
            )
            .await;
        (outcome, teardown)
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_step_budgets_total_the_documented_shutdown_budget() {
        assert_eq!(SHUTDOWN_BUDGET, Duration::from_secs(15));
    }

    #[tokio::test(start_paused = true)]
    async fn a_slow_embedded_s3_close_uses_the_budget_that_earlier_steps_left() {
        let mut teardown = BoundedTeardown::start();
        teardown.run("engine", ENGINE_STOP_BUDGET, async {}).await;
        teardown
            .run(
                "embedded S3",
                embedded_s3_stop_budget(&teardown),
                tokio::time::sleep(Duration::from_secs(3)),
            )
            .await;
        let finished = teardown.finish(Ok(()));
        assert!(finished.is_ok(), "{finished:?}");
    }

    #[tokio::test(start_paused = true)]
    async fn a_stalled_embedded_s3_ends_at_the_shutdown_budget_or_after_its_minimum() {
        let slowest_earlier_steps = SHUTDOWN_BUDGET.saturating_sub(EMBEDDED_S3_MINIMUM_STOP_BUDGET);
        for earlier_steps in [
            Duration::ZERO,
            Duration::from_secs(5),
            slowest_earlier_steps,
            Duration::from_secs(14),
        ] {
            let started = tokio::time::Instant::now();
            let mut teardown = BoundedTeardown::start();
            tokio::time::sleep(earlier_steps).await;
            let budget = embedded_s3_stop_budget(&teardown);
            teardown
                .run("embedded S3", budget, std::future::pending())
                .await;
            assert_eq!(
                started.elapsed(),
                SHUTDOWN_BUDGET.max(earlier_steps + EMBEDDED_S3_MINIMUM_STOP_BUDGET),
                "after {earlier_steps:?}"
            );
            let expected = format!("embedded S3 exceeded its {} ms budget", budget.as_millis());
            assert!(
                matches!(
                    teardown.finish(Ok(())),
                    Err(ServerError::ShutdownIncomplete { steps }) if steps == expected
                ),
                "after {earlier_steps:?}"
            );
        }
    }
}
