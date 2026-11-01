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
use std::time::Duration;

use dexzd_api::{
    ActiveNodeAdministration, DexAdminService, DexFlowService, HealthStatus,
    PublicApiAuthenticator, RequestMetricsLayer,
};
use dexzd_protocol::dex::flow_service_server::FlowServiceServer;
use dexzd_protocol::dex::worker_task_service_server::WorkerTaskServiceServer;
use dexzd_worker_dispatch::WorkerTaskService;
use tokio::net::TcpListener;
use tokio::sync::oneshot;
use tokio::task::JoinHandle;
use tokio_util::sync::CancellationToken;
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

/// What every gRPC server instance of the process shares.
pub(super) struct GrpcSettings {
    /// Bound once, so connections queue between instances instead of being refused.
    pub(super) listener: Arc<TcpListener>,
    pub(super) max_message_bytes: usize,
    pub(super) api_requests: dexzd_metrics::ApiRequests,
    pub(super) api_latency: dexzd_metrics::ApiLatency,
    pub(super) authenticator: Option<Arc<PublicApiAuthenticator>>,
    pub(super) active_node_administration: Arc<dyn ActiveNodeAdministration>,
    pub(super) health: HealthStatus,
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
    pub(super) fn start(settings: &GrpcSettings) -> Self {
        let flow_service_gate = StartupService::<FlowServiceServer<DexFlowService>>::new();
        let worker_task_service_gate =
            StartupService::<WorkerTaskServiceServer<WorkerTaskService>>::new();
        let admin_service = DexAdminService::authenticated_server(
            settings.active_node_administration.clone(),
            settings.authenticator.clone(),
        );
        let stop = CancellationToken::new();
        let stopped = stop.clone();
        let listener = settings.listener.clone();
        let task = tokio::spawn(
            tonic::transport::Server::builder()
                .http2_keepalive_interval(Some(KEEPALIVE_INTERVAL))
                .http2_keepalive_timeout(Some(KEEPALIVE_TIMEOUT))
                .layer(RequestMetricsLayer::new(
                    settings.api_requests.clone(),
                    settings.api_latency.clone(),
                ))
                .layer(tower_layer::layer_fn(
                    client_deadline::client_deadline_layer,
                ))
                .add_service(settings.health.server())
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
}

impl<S> StartupService<S> {
    fn new() -> Self {
        Self {
            serving: Arc::new(RwLock::new(None)),
        }
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
        Box::pin(async move {
            match serving {
                Some(mut service) => service.call(request).await,
                None => Ok(Status::unavailable("the node is starting").into_http()),
            }
        })
    }
}
