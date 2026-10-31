// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use super::search_projection::SearchProjectionAdmin;
use super::*;
use dexzd_protocol::dex::admin_service_server::AdminServiceServer;

pub(super) struct GrpcServer {
    pub(super) health: HealthStatus,
    pub(super) flow_service_gate: StartupService<FlowServiceServer<DexFlowService>>,
    pub(super) worker_task_service_gate: StartupService<WorkerTaskServiceServer<WorkerTaskService>>,
    pub(super) admin_service_gate: StartupService<AdminServiceServer<SearchProjectionAdmin>>,
    pub(super) max_message_bytes: usize,
    pub(super) stop: CancellationToken,
    pub(super) task: JoinHandle<Result<(), tonic::transport::Error>>,
}

impl GrpcServer {
    pub(super) async fn start(
        listener: TcpListener,
        max_message_bytes: usize,
        api_requests: dexzd_metrics::ApiRequests,
        api_latency: dexzd_metrics::ApiLatency,
        authenticator: Option<&Arc<PublicApiAuthenticator>>,
        admin_authenticator: Option<&Arc<PublicApiAuthenticator>>,
    ) -> Self {
        let (health, health_server) = HealthStatus::new().await;
        let flow_service_gate = StartupService::<FlowServiceServer<DexFlowService>>::new();
        let worker_task_service_gate =
            StartupService::<WorkerTaskServiceServer<WorkerTaskService>>::new();
        let admin_service_gate = StartupService::<AdminServiceServer<SearchProjectionAdmin>>::new();
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
                .add_service(dexzd_api::AuthenticatedService::for_admin(admin_service_gate.clone(), admin_authenticator.cloned()))
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
            admin_service_gate,
            stop,
            task,
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
