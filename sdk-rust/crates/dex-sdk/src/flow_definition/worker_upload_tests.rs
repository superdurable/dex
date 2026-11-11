// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeSet;
use std::convert::Infallible;
use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::task::{Context, Poll};
use std::time::{Duration, Instant, SystemTime};

use dex_protocol::dex::flow_service_client::FlowServiceClient;
use dex_protocol::dex::{UploadFlowDefinitionsRequest, UploadFlowDefinitionsResponse};
use tokio::net::TcpListener;
use tokio_stream::wrappers::TcpListenerStream;
use tonic::codegen::{BoxFuture, Service, http};
use tonic::server::{NamedService, UnaryService};
use tonic::transport::{Endpoint, Server};
use tonic::{Request, Response, Status};

use super::{FlowDefinitionDocument, upload_flow_definitions_at_worker_start};
use crate::credential::{CredentialChannel, CredentialInterceptor};

const BATCH_DELAY: Duration = Duration::from_millis(800);
const DOCUMENTS_IN_TWO_BATCHES: usize = 257;

#[derive(Clone)]
struct SlowUploadService {
    calls: Arc<AtomicUsize>,
}

impl NamedService for SlowUploadService {
    const NAME: &'static str = "dex.FlowService";
}

impl<B> Service<http::Request<B>> for SlowUploadService
where
    B: tonic::codegen::Body + Send + 'static,
    B::Error: Into<tonic::codegen::StdError> + Send + 'static,
{
    type Response = http::Response<tonic::body::Body>;
    type Error = Infallible;
    type Future = BoxFuture<Self::Response, Self::Error>;

    fn poll_ready(&mut self, _: &mut Context<'_>) -> Poll<Result<(), Self::Error>> {
        Poll::Ready(Ok(()))
    }

    fn call(&mut self, request: http::Request<B>) -> Self::Future {
        let is_upload = request.uri().path() == "/dex.FlowService/UploadFlowDefinitions";
        if is_upload {
            self.calls.fetch_add(1, Ordering::SeqCst);
        }
        Box::pin(async move {
            if is_upload {
                let mut grpc = tonic::server::Grpc::new(tonic_prost::ProstCodec::default());
                Ok(grpc.unary(SlowUpload, request).await)
            } else {
                Ok(Status::unimplemented("fake FlowService").into_http())
            }
        })
    }
}

struct SlowUpload;

impl UnaryService<UploadFlowDefinitionsRequest> for SlowUpload {
    type Response = UploadFlowDefinitionsResponse;
    type Future = BoxFuture<Response<Self::Response>, Status>;

    fn call(&mut self, _: Request<UploadFlowDefinitionsRequest>) -> Self::Future {
        Box::pin(async move {
            tokio::time::sleep(BATCH_DELAY).await;
            Ok(Response::new(UploadFlowDefinitionsResponse::default()))
        })
    }
}

fn small_document(flow_type: &str) -> FlowDefinitionDocument {
    let content = format!("{{\"schemaVersion\":\"2.0\",\"flow\":{{\"name\":\"{flow_type}\"}}}}");
    FlowDefinitionDocument::new(flow_type, content, SystemTime::UNIX_EPOCH)
}

#[tokio::test]
async fn shares_one_deadline_across_upload_batches() {
    let listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("bind fake FlowService");
    let address = listener.local_addr().expect("fake FlowService address");
    let calls = Arc::new(AtomicUsize::new(0));
    let server = tokio::spawn(
        Server::builder()
            .add_service(SlowUploadService {
                calls: Arc::clone(&calls),
            })
            .serve_with_incoming(TcpListenerStream::new(listener)),
    );
    let endpoint = Endpoint::from_shared(format!("http://{address}")).expect("valid endpoint");
    let service = FlowServiceClient::new(CredentialChannel::new(
        endpoint.connect_lazy(),
        CredentialInterceptor::new(None),
    ));
    let flow_types = (0..DOCUMENTS_IN_TWO_BATCHES)
        .map(|index| format!("BatchFlow{index}"))
        .collect::<BTreeSet<_>>();
    let documents = flow_types
        .iter()
        .map(|flow_type| small_document(flow_type))
        .collect::<Vec<_>>();

    let started = Instant::now();
    upload_flow_definitions_at_worker_start(
        &service,
        Duration::from_millis(1200),
        &documents,
        &flow_types,
    )
    .await;
    let elapsed = started.elapsed();

    assert_eq!(2, calls.load(Ordering::SeqCst));
    assert!(
        elapsed < Duration::from_millis(1450),
        "two batches took {elapsed:?}; per-request timeouts would take 1600ms"
    );
    server.abort();
}
