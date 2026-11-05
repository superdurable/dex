// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use super::map_flow_config;
use crate::FlowConfig;

#[test]
fn attribute_store_names_preserve_protocol_presence() {
    let absent = map_flow_config(Some(&FlowConfig::new())).expect("map absent config");
    let named = map_flow_config(Some(
        &FlowConfig::new().attribute_store_names(vec!["profiles".to_owned(), "audit".to_owned()]),
    ))
    .expect("map named config");
    let disabled = map_flow_config(Some(&FlowConfig::new().attribute_store_names(vec![])))
        .expect("map disabled config");

    assert_eq!(absent.attribute_store_names, None);
    assert_eq!(
        named.attribute_store_names.expect("named stores").names,
        vec!["profiles", "audit"]
    );
    assert_eq!(
        disabled
            .attribute_store_names
            .expect("disabled stores")
            .names,
        Vec::<String>::new()
    );
}

mod hard_delete {
    use std::convert::Infallible;
    use std::sync::atomic::{AtomicU32, Ordering};
    use std::sync::{Arc, LazyLock, Mutex};
    use std::task::{Context, Poll};

    use dex_protocol::dex::ServerInfo;
    use tokio::net::TcpListener;
    use tokio::sync::oneshot;
    use tokio_stream::wrappers::TcpListenerStream;
    use tonic::codegen::{BoxFuture, Service, http};
    use tonic::server::{NamedService, UnaryService};
    use tonic::transport::Server;
    use tonic::{Request, Response, Status};

    use crate::{
        Attribute, AttributeMap, AttributeMatch, BlobCache, BlobCacheConfig, Client, ClientOptions,
        Flow, PersistenceSchema, Registry, SdkError, StartFlowOptions, WaitForAttributeOptions,
    };

    static API_KEY: LazyLock<Attribute<String>> =
        LazyLock::new(|| Attribute::new("api-key").hard_delete_on_null());
    static TENANT_KEYS: LazyLock<AttributeMap<String>> =
        LazyLock::new(|| AttributeMap::new("tenant-keys").hard_delete_on_null());
    static PLAIN_NOTE: LazyLock<Attribute<String>> = LazyLock::new(|| Attribute::new("plain-note"));

    struct HardDeleteClientFlow;

    impl Flow for HardDeleteClientFlow {
        type StartInput = ();

        fn persistence(&self) -> PersistenceSchema {
            PersistenceSchema::new()
                .attribute(&API_KEY)
                .attribute_map(&TENANT_KEYS)
                .attribute(&PLAIN_NOTE)
        }
    }

    #[derive(Clone)]
    struct OldFlowService {
        paths: Arc<Mutex<Vec<String>>>,
        current_protocol_version: Arc<AtomicU32>,
    }

    impl NamedService for OldFlowService {
        const NAME: &'static str = "dex.FlowService";
    }

    impl<B> Service<http::Request<B>> for OldFlowService
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
            let path = request.uri().path().to_string();
            self.paths.lock().expect("paths lock").push(path.clone());
            let server_info = GetServerInfo {
                current_protocol_version: self.current_protocol_version.load(Ordering::SeqCst),
            };
            Box::pin(async move {
                if path == "/dex.FlowService/GetServerInfo" {
                    let mut grpc = tonic::server::Grpc::new(tonic_prost::ProstCodec::default());
                    Ok(grpc.unary(server_info, request).await)
                } else {
                    Ok(Status::unimplemented("fake FlowService").into_http())
                }
            })
        }
    }

    struct GetServerInfo {
        current_protocol_version: u32,
    }

    impl UnaryService<()> for GetServerInfo {
        type Response = ServerInfo;
        type Future = BoxFuture<Response<Self::Response>, Status>;

        fn call(&mut self, _: Request<()>) -> Self::Future {
            let current_protocol_version = self.current_protocol_version;
            Box::pin(async move {
                if current_protocol_version == u32::MAX {
                    return Err(Status::unavailable("transient negotiation failure"));
                }
                Ok(Response::new(ServerInfo {
                    server_version: "fake".to_string(),
                    minimum_supported_protocol_version: 1,
                    current_protocol_version,
                    server_engine: 0,
                }))
            })
        }
    }

    fn client_for(address: &str, cache_directory: &tempfile::TempDir) -> Client {
        let cache = Arc::new(
            BlobCache::open(
                BlobCacheConfig::new(cache_directory.path(), 1024 * 1024, 0)
                    .expect("blob cache config"),
            )
            .expect("blob cache"),
        );
        Client::try_new(
            Registry::new()
                .register(HardDeleteClientFlow)
                .expect("register hard-delete Flow"),
            cache,
            ClientOptions::new().server_address(address),
        )
        .expect("Client")
    }

    #[test]
    fn wait_for_attribute_rejects_hard_delete_attributes_before_contacting_dex() {
        let cache_directory = tempfile::tempdir().expect("blob cache directory");
        let client = client_for("127.0.0.1:1", &cache_directory);
        let single = client.wait_for_attribute_match(
            "flow-1",
            &API_KEY,
            AttributeMatch::equal_to("key-1".to_string()),
            WaitForAttributeOptions::new(),
        );
        let map = client.wait_for_attribute_map_instance_match(
            "flow-1",
            &TENANT_KEYS,
            "tenant-a",
            AttributeMatch::equal_to("key-a".to_string()),
            WaitForAttributeOptions::new(),
        );
        for result in [single, map] {
            let Err(SdkError::InvalidArgument { message }) = result else {
                panic!("expected a local InvalidArgument");
            };
            assert!(message.contains("does not support hard-delete Attribute"));
        }
    }

    #[test]
    fn start_flow_negotiates_once_and_rejects_hard_delete_writes_to_old_servers() {
        let runtime = tokio::runtime::Runtime::new().expect("server runtime");
        let listener = runtime
            .block_on(TcpListener::bind("127.0.0.1:0"))
            .expect("bind fake FlowService");
        let address = listener.local_addr().expect("fake server address");
        let paths = Arc::new(Mutex::new(Vec::new()));
        let current_protocol_version = Arc::new(AtomicU32::new(3));
        let (shutdown, stopped) = oneshot::channel::<()>();
        runtime.spawn(
            Server::builder()
                .add_service(OldFlowService {
                    paths: Arc::clone(&paths),
                    current_protocol_version: Arc::clone(&current_protocol_version),
                })
                .serve_with_incoming_shutdown(TcpListenerStream::new(listener), async {
                    stopped.await.expect("FlowService shutdown signal");
                }),
        );
        let cache_directory = tempfile::tempdir().expect("blob cache directory");
        let client = client_for(&address.to_string(), &cache_directory);

        for options in [
            StartFlowOptions::new().initial_attribute(&API_KEY, "key-1".to_string()),
            StartFlowOptions::new().initial_attribute_map(
                &TENANT_KEYS,
                "tenant-a",
                "key-a".to_string(),
            ),
        ] {
            let error = client
                .start_flow_with_options(&HardDeleteClientFlow, "flow-1", (), options)
                .expect_err("old Server rejects hard-delete writes");
            let SdkError::Service { service } = error else {
                panic!("expected a Service error, got {error}");
            };
            assert!(service.detail().contains("protocol version 4"));
            assert!(service.detail().contains("protocol versions 1-3"));
        }
        assert_eq!(
            vec!["/dex.FlowService/GetServerInfo".to_string()],
            *paths.lock().expect("paths lock")
        );

        current_protocol_version.store(4, Ordering::SeqCst);
        let error = client
            .start_flow_with_options(
                &HardDeleteClientFlow,
                "flow-1",
                (),
                StartFlowOptions::new().initial_attribute(&API_KEY, "key-1".to_string()),
            )
            .expect_err("cached negotiation still rejects the write");
        assert!(error.to_string().contains("protocol versions 1-3"));
        client
            .start_flow_with_options(
                &HardDeleteClientFlow,
                "flow-1",
                (),
                StartFlowOptions::new().initial_attribute(&PLAIN_NOTE, "note".to_string()),
            )
            .expect_err("fake StartFlow is unimplemented");
        assert_eq!(
            vec!["/dex.FlowService/GetServerInfo".to_string()],
            *paths.lock().expect("paths lock")
        );

        let fresh_client = client_for(&address.to_string(), &cache_directory);
        fresh_client
            .start_flow_with_options(
                &HardDeleteClientFlow,
                "flow-1",
                (),
                StartFlowOptions::new().initial_attribute(&API_KEY, "key-1".to_string()),
            )
            .expect_err("fake StartFlow is unimplemented");
        assert_eq!(
            Some(&"/dex.FlowService/StartFlow".to_string()),
            paths.lock().expect("paths lock").last()
        );
        shutdown.send(()).expect("stop fake FlowService");
    }

    struct ProtocolServer {
        runtime: tokio::runtime::Runtime,
        address: String,
        paths: Arc<Mutex<Vec<String>>>,
        version: Arc<AtomicU32>,
        shutdown: oneshot::Sender<()>,
        server: tokio::task::JoinHandle<Result<(), tonic::transport::Error>>,
    }

    impl ProtocolServer {
        fn new(version: u32) -> Self {
            let runtime = tokio::runtime::Runtime::new().unwrap();
            let listener = runtime.block_on(TcpListener::bind("127.0.0.1:0")).unwrap();
            let address = listener.local_addr().unwrap().to_string();
            let paths = Arc::new(Mutex::new(Vec::new()));
            let version = Arc::new(AtomicU32::new(version));
            let (shutdown, stopped) = oneshot::channel();
            let server = runtime.spawn(
                Server::builder()
                    .add_service(OldFlowService {
                        paths: Arc::clone(&paths),
                        current_protocol_version: Arc::clone(&version),
                    })
                    .serve_with_incoming_shutdown(TcpListenerStream::new(listener), async move {
                        stopped.await.unwrap();
                    }),
            );
            Self {
                runtime,
                address,
                paths,
                version,
                shutdown,
                server,
            }
        }

        fn finish(self) {
            self.shutdown.send(()).unwrap();
            self.runtime.block_on(self.server).unwrap().unwrap();
        }
    }

    #[test]
    fn client_first_call_of_any_method_caches_incompatibility_and_worker_fails_before_sync() {
        let server = ProtocolServer::new(3);
        let directory = tempfile::tempdir().unwrap();
        let client = client_for(&server.address, &directory);
        assert!(server.paths.lock().unwrap().is_empty());
        let first = client.health_check().unwrap_err().to_string();
        assert!(first.contains("Server reports protocol versions 1-3 (server version fake)"));
        assert!(first.contains("server-zd"));
        assert!(first.contains("protocol version 4"));
        assert_eq!(first, client.describe_flow("flow").unwrap_err().to_string());
        assert_eq!(
            first,
            client
                .search_flows("FlowType = 'SlotFlow'", 10)
                .unwrap_err()
                .to_string()
        );
        assert_eq!(
            *server.paths.lock().unwrap(),
            ["/dex.FlowService/GetServerInfo"]
        );
        let cache = Arc::new(
            BlobCache::open(BlobCacheConfig::new(directory.path(), 1024 * 1024, 0).unwrap())
                .unwrap(),
        );
        let worker = crate::Worker::try_new(
            Registry::new(),
            cache,
            crate::WorkerOptions::new().server_address(&server.address),
        )
        .unwrap();
        let failure = worker.start().unwrap_err().to_string();
        assert!(failure.contains("Server reports protocol versions 1-3 (server version fake)"));
        assert!(failure.contains("protocol version 4"));
        assert_eq!(server.paths.lock().unwrap().len(), 2);
        assert!(
            server
                .paths
                .lock()
                .unwrap()
                .iter()
                .all(|path| path == "/dex.FlowService/GetServerInfo")
        );
        server.finish();
    }

    #[test]
    fn client_retries_transient_negotiation_then_caches_success() {
        let server = ProtocolServer::new(u32::MAX);
        let directory = tempfile::tempdir().unwrap();
        let client = client_for(&server.address, &directory);
        assert!(
            client
                .health_check()
                .unwrap_err()
                .to_string()
                .contains("transient negotiation failure")
        );
        server.version.store(4, Ordering::SeqCst);
        for _ in 0..2 {
            assert_eq!(
                client
                    .health_check()
                    .unwrap_err()
                    .service_error()
                    .unwrap()
                    .code(),
                tonic::Code::Unimplemented
            );
        }
        assert_eq!(
            *server.paths.lock().unwrap(),
            [
                "/dex.FlowService/GetServerInfo",
                "/dex.FlowService/GetServerInfo",
                "/dex.FlowService/HealthCheck",
                "/dex.FlowService/HealthCheck",
            ]
        );
        server.finish();
    }
}
