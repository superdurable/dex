// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! One gRPC server instance on the listener that the process keeps across serving periods.

use std::convert::Infallible;
use std::sync::{Arc, RwLock};
use std::task::{Context, Poll};
use std::time::{Duration, Instant};

use dexzd_api::{
    ActiveNodeAdministration, DexAdminService, DexFlowService, HealthStatus,
    PublicApiAuthenticator, RequestMetricsLayer,
};
use dexzd_cluster_metadata::LeaseValidityDeadline;
use dexzd_protocol::dex::flow_service_server::FlowServiceServer;
use dexzd_protocol::dex::worker_task_service_server::WorkerTaskServiceServer;
use dexzd_worker_dispatch::WorkerTaskService;
use http_body_util::BodyExt;
use tokio::net::TcpListener;
use tokio::sync::oneshot;
use tokio::task::JoinHandle;
use tokio_util::sync::CancellationToken;
use tokio_util::task::TaskTracker;
use tonic::Status;
use tonic::body::Body;
use tonic::codegen::{BoxFuture, Service, http};
use tonic::server::NamedService;

use super::{client_deadline, public_api_authentication};

/// gRPC keepalive, equal to the Go Server's.
const KEEPALIVE_INTERVAL: Duration = Duration::from_secs(20);
const KEEPALIVE_TIMEOUT: Duration = Duration::from_secs(10);
/// Lets the step down handler write its answer before GOAWAY reaches that connection.
const ANSWER_TRANSPORT_GRACE: Duration = Duration::from_millis(250);
/// The answer to a public call while the node lease is not valid, such as after a freeze.
const LEASE_NOT_VALID_MESSAGE: &str = "the node lease is not valid";
/// The answer to a public call that was still open when its serving period ended.
const STOPPED_SERVING_MESSAGE: &str =
    "the server process stopped serving during the call; retry it";
/// A standby instance closes each connection this soon, so its clients reconnect to the active.
const STANDBY_CONNECTION_AGE: Duration = Duration::from_secs(3);
/// How long an aged standby connection may finish its calls; AdminService answers at once.
const STANDBY_CONNECTION_AGE_GRACE: Duration = Duration::from_secs(1);

/// Whether an instance belongs to a standby phase or to a serving period.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(super) enum GrpcInstancePhase {
    /// Public services refuse calls, and connections are closed after [`STANDBY_CONNECTION_AGE`].
    Standby,
    /// Public services serve once activated; connections have no age limit.
    Serving,
}

/// What every gRPC server instance of the process shares.
pub(super) struct GrpcSettings {
    /// Bound once, so connections queue between instances instead of being refused.
    pub(super) listener: Arc<TcpListener>,
    pub(super) max_message_bytes: usize,
    pub(super) api_requests: dexzd_metrics::ApiRequests,
    pub(super) api_latency: dexzd_metrics::ApiLatency,
    pub(super) authenticator: Option<Arc<PublicApiAuthenticator>>,
    /// Answers AdminService calls on serving instances.
    pub(super) serving_instance_administration: Arc<dyn ActiveNodeAdministration>,
    /// Answers AdminService calls on standby-phase instances; a serving process refuses their
    /// step downs at once.
    pub(super) standby_instance_administration: Arc<dyn ActiveNodeAdministration>,
    pub(super) health: HealthStatus,
    /// Public calls answer only while it is valid, so a resumed process never serves stale state.
    pub(super) lease_validity_deadline: LeaseValidityDeadline,
}

/// A tonic server instance whose public services answer UNAVAILABLE until activated.
pub(super) struct GrpcServer {
    pub(super) health: HealthStatus,
    pub(super) flow_service_gate: StartupService<FlowServiceServer<DexFlowService>>,
    pub(super) worker_task_service_gate: StartupService<WorkerTaskServiceServer<WorkerTaskService>>,
    pub(super) max_message_bytes: usize,
    pub(super) stop: CancellationToken,
    pub(super) task: JoinHandle<Result<(), tonic::transport::Error>>,
}

impl GrpcServer {
    /// Starts an instance on the shared listener; health and AdminService answer at once.
    pub(super) fn start(settings: &GrpcSettings, phase: GrpcInstancePhase) -> Self {
        let flow_service_gate = StartupService::<FlowServiceServer<DexFlowService>>::new(
            settings.lease_validity_deadline.clone(),
        );
        let worker_task_service_gate =
            StartupService::<WorkerTaskServiceServer<WorkerTaskService>>::new(
                settings.lease_validity_deadline.clone(),
            );
        Self::start_with_gates(settings, phase, flow_service_gate, worker_task_service_gate)
    }

    /// Starts a serving instance whose public services share `standby_instance`'s gates.
    ///
    /// The standby instance keeps accepting connections until it retires, so it serves their calls too.
    pub(super) fn start_serving_sharing_gates(
        settings: &GrpcSettings,
        standby_instance: &GrpcServer,
    ) -> Self {
        Self::start_with_gates(
            settings,
            GrpcInstancePhase::Serving,
            standby_instance.flow_service_gate.clone(),
            standby_instance.worker_task_service_gate.clone(),
        )
    }

    fn start_with_gates(
        settings: &GrpcSettings,
        phase: GrpcInstancePhase,
        flow_service_gate: StartupService<FlowServiceServer<DexFlowService>>,
        worker_task_service_gate: StartupService<WorkerTaskServiceServer<WorkerTaskService>>,
    ) -> Self {
        let administration = match phase {
            GrpcInstancePhase::Standby => settings.standby_instance_administration.clone(),
            GrpcInstancePhase::Serving => settings.serving_instance_administration.clone(),
        };
        let admin_service =
            DexAdminService::authenticated_server(administration, settings.authenticator.clone());
        let stop = CancellationToken::new();
        let stopped = stop.clone();
        let listener = settings.listener.clone();
        let mut builder = tonic::transport::Server::builder()
            .http2_keepalive_interval(Some(KEEPALIVE_INTERVAL))
            .http2_keepalive_timeout(Some(KEEPALIVE_TIMEOUT));
        if phase == GrpcInstancePhase::Standby {
            builder = builder
                .max_connection_age(STANDBY_CONNECTION_AGE)
                .max_connection_age_grace(STANDBY_CONNECTION_AGE_GRACE);
        }
        let task = tokio::spawn(
            builder
                .layer(RequestMetricsLayer::new(
                    settings.api_requests.clone(),
                    settings.api_latency.clone(),
                ))
                .layer(tower_layer::layer_fn(
                    client_deadline::client_deadline_layer,
                ))
                .add_service(settings.health.server(phase == GrpcInstancePhase::Serving))
                .add_service(public_api_authentication::authenticated(
                    flow_service_gate.clone(),
                    settings.authenticator.as_ref(),
                ))
                .add_service(public_api_authentication::authenticated(
                    worker_task_service_gate.clone(),
                    settings.authenticator.as_ref(),
                ))
                .add_service(admin_service)
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
            health: settings.health.clone(),
            flow_service_gate,
            worker_task_service_gate,
            max_message_bytes: settings.max_message_bytes,
            stop,
            task,
        }
    }

    /// Makes FlowService and WorkerTaskService answer UNAVAILABLE again; AdminService keeps answering.
    pub(super) fn deactivate_public_services(&self) {
        self.flow_service_gate.deactivate();
        self.worker_task_service_gate.deactivate();
    }

    /// Ends open FlowService and WorkerTaskService calls with UNAVAILABLE; AdminService calls keep running.
    pub(super) fn end_public_calls(&self) {
        self.flow_service_gate.end_open_calls();
        self.worker_task_service_gate.end_open_calls();
    }

    /// Returns a wait until every accepted public call, response body included, has ended.
    pub(super) fn public_calls_ended(&self) -> impl Future<Output = ()> + Send + 'static {
        let flow_service_gate = self.flow_service_gate.clone();
        let worker_task_service_gate = self.worker_task_service_gate.clone();
        async move {
            tokio::join!(
                flow_service_gate.wait_for_calls(),
                worker_task_service_gate.wait_for_calls(),
            );
        }
    }

    /// Once `answered` resolves, sends GOAWAY and waits up to `budget` for open calls; then aborts.
    pub(super) async fn retire_after(mut self, answered: oneshot::Receiver<()>, budget: Duration) {
        // A dropped sender also means the answer went out.
        drop(answered.await);
        tokio::time::sleep(ANSWER_TRANSPORT_GRACE).await;
        self.stop.cancel();
        match tokio::time::timeout(budget, &mut self.task).await {
            Ok(Ok(Ok(()))) => {}
            Ok(Ok(Err(error))) => tracing::warn!(%error, "a retired gRPC server failed"),
            Ok(Err(error)) => tracing::warn!(%error, "a retired gRPC server task failed"),
            Err(_) => tracing::warn!("a retired gRPC server kept open calls; closing them"),
        }
    }

    pub(super) async fn abort(mut self) {
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
pub(super) struct StartupService<S> {
    serving: Arc<RwLock<Option<S>>>,
    lease_validity_deadline: LeaseValidityDeadline,
    /// Every accepted call until its response body is dropped; a body can still read the store.
    calls: TaskTracker,
    /// Ends the open calls of the period these gates serve; each period gets a new standby instance's gates.
    period_end: CancellationToken,
}

impl<S> StartupService<S> {
    fn new(lease_validity_deadline: LeaseValidityDeadline) -> Self {
        Self {
            serving: Arc::new(RwLock::new(None)),
            lease_validity_deadline,
            calls: TaskTracker::new(),
            period_end: CancellationToken::new(),
        }
    }

    async fn wait_for_calls(&self) {
        self.calls.close();
        self.calls.wait().await;
    }

    /// Answers every open call UNAVAILABLE at once, dropping its service clone and so its store handles.
    fn end_open_calls(&self) {
        self.period_end.cancel();
    }

    pub(super) fn activate(&self, service: S) {
        *self
            .serving
            .write()
            .expect("startup service lock is poisoned only if a holder panicked") = Some(service);
    }

    fn deactivate(&self) {
        *self
            .serving
            .write()
            .expect("startup service lock is poisoned only if a holder panicked") = None;
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
        let lease_validity_deadline = self.lease_validity_deadline.clone();
        let period_end = self.period_end.clone();
        // Taken with the service clone, so a drain that starts after it waits for this call.
        let call = self.calls.token();
        Box::pin(async move {
            let Some(mut service) = serving else {
                return Ok(Status::unavailable("the node is starting").into_http());
            };
            if !lease_validity_deadline.is_valid_at(Instant::now()) {
                return Ok(Status::unavailable(LEASE_NOT_VALID_MESSAGE).into_http());
            }
            let response = tokio::select! {
                biased;
                () = period_end.cancelled() => {
                    return Ok(Status::unavailable(STOPPED_SERVING_MESSAGE).into_http());
                }
                response = service.call(request) => response?,
            };
            // A freeze during the call can outlast the lease, so the answer may already be stale.
            if !lease_validity_deadline.is_valid_at(Instant::now()) {
                return Ok(Status::unavailable(LEASE_NOT_VALID_MESSAGE).into_http());
            }
            Ok(response.map(|body| {
                Body::new(body.map_frame(move |frame| {
                    let _ongoing_call = &call;
                    frame
                }))
            }))
        })
    }
}
