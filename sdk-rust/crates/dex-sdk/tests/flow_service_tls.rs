// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::Arc;
use std::time::{Duration, Instant};

use dex_sdk::{
    BlobCache, BlobCacheConfig, Client, ClientOptions, Context, FileCredential, Flow,
    FlowServiceTlsOptions, GrpcCode, HandlerResult, PullWorkerOptions, Registry, SdkError, Step,
    StepDecision, StepList, Worker, WorkerOptions,
};

#[path = "tls/support.rs"]
mod support;
use support::{ServerProcess, TestCertificates, TlsProxy};

fn cache(directory: &std::path::Path) -> Arc<BlobCache> {
    Arc::new(
        BlobCache::open(
            BlobCacheConfig::new(directory, 64 * 1024 * 1024, 0).expect("valid test cache config"),
        )
        .expect("open TLS test blob cache"),
    )
}

#[test]
fn validates_tls_options_at_client_and_worker_construction() {
    let directory = tempfile::tempdir().unwrap();
    let cache = cache(&directory.path().join("cache"));
    let certificates = TestCertificates::generate("validation");
    for address in [
        "localhost:443",
        "dns:///localhost:443",
        "DNS:///localhost:443",
        "dns://resolver:53/localhost:443",
        "[::1]:443",
        "dns:///[::1]:443",
        "localhost:1",
        "localhost:65535",
    ] {
        let options = FlowServiceTlsOptions::default();
        Client::try_new(
            Registry::new(),
            Arc::clone(&cache),
            ClientOptions::new()
                .server_address(address)
                .flow_service_tls(options.clone()),
        )
        .unwrap();
        Worker::try_new(
            Registry::new(),
            Arc::clone(&cache),
            WorkerOptions::new()
                .server_address(address)
                .flow_service_tls(options),
        )
        .unwrap();
    }
    for address in [
        "",
        "localhost",
        "localhost:0",
        "localhost:65536",
        "localhost:+443",
        "localhost:-1",
        "localhost: 443",
        "[localhost]:443",
        "[127.0.0.1]:443",
        "::1:443",
        "https://localhost:443",
        "unix:443",
        "DNS:443",
        "unix:///tmp/dex.sock",
        "dns://localhost:443",
        "dns:///localhost/path:443",
        "dns://bad@authority/localhost:443",
        "dns:///localhost:443?query",
        "local host:443",
        "[fe80::1%lo0]:443",
    ] {
        assert_invalid(
            &cache,
            address,
            FlowServiceTlsOptions::default(),
            "server_address",
        );
    }
    assert_invalid(
        &cache,
        "例子.example:443",
        FlowServiceTlsOptions::new(),
        "host is not a valid DNS name or IP address",
    );
    for name in [
        "",
        "localhost:443",
        "https://localhost",
        "bad name",
        "bad/name",
        "bad?name",
        "bad@name",
        "[::1]",
        "::1%lo0",
    ] {
        assert_invalid(
            &cache,
            "localhost:443",
            FlowServiceTlsOptions::new().server_name(name),
            "server_name",
        );
    }
    for bundle in [
        b"invalid".to_vec(),
        b"-----BEGIN CERTIFICATE-----\nYWJj\n-----END CERTIFICATE-----".to_vec(),
        b"-----BEGIN CERTIFICATE-----\n!invalid!\n-----END CERTIFICATE-----".to_vec(),
    ] {
        assert_invalid(
            &cache,
            "localhost:443",
            FlowServiceTlsOptions::new().additional_root_cas_pem(bundle),
            "additional_root_cas_pem",
        );
    }
    let mut bundle =
        b"-----BEGIN CERTIFICATE-----\n!invalid!\n-----END CERTIFICATE-----\n".to_vec();
    bundle.extend(&certificates.ca_pem);
    for bundle in [Vec::new(), certificates.ca_pem, bundle] {
        Client::try_new(
            Registry::new(),
            Arc::clone(&cache),
            ClientOptions::new()
                .flow_service_tls(FlowServiceTlsOptions::new().additional_root_cas_pem(bundle)),
        )
        .unwrap();
    }
    cache.close().unwrap();
}

fn assert_invalid(
    cache: &Arc<BlobCache>,
    address: &str,
    options: FlowServiceTlsOptions,
    expected: &str,
) {
    let client = Client::try_new(
        Registry::new(),
        Arc::clone(cache),
        ClientOptions::new()
            .server_address(address)
            .flow_service_tls(options.clone()),
    );
    let worker = Worker::try_new(
        Registry::new(),
        Arc::clone(cache),
        WorkerOptions::new()
            .server_address(address)
            .flow_service_tls(options),
    );
    for result in [client.map(|_| ()), worker.map(|_| ())] {
        let error = result.expect_err("invalid TLS options must fail construction");
        assert!(matches!(error, SdkError::InvalidArgument { .. }), "{error}");
        assert!(error.to_string().contains(expected), "{error}");
    }
}

struct TlsTransportFlow;
impl Flow for TlsTransportFlow {
    type StartInput = String;
    fn steps(&self) -> StepList<'_, String> {
        StepList::start(&TlsTransportStep)
    }
}
struct TlsTransportStep;
impl Step for TlsTransportStep {
    type Input = String;
    fn execute(&self, _context: &mut Context, input: String) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(input))
    }
}

struct RunningWorker {
    worker: Arc<Worker>,
    thread: Option<std::thread::JoinHandle<dex_sdk::SdkResult<()>>>,
}
impl RunningWorker {
    fn start(worker: Worker) -> Self {
        let worker = Arc::new(worker);
        let running = Arc::clone(&worker);
        let thread = Some(std::thread::spawn(move || running.start()));
        Self { worker, thread }
    }
}
impl Drop for RunningWorker {
    fn drop(&mut self) {
        self.worker.stop();
        self.thread
            .take()
            .expect("Worker thread exists")
            .join()
            .expect("join TLS Worker thread")
            .expect("TLS Worker stopped successfully");
    }
}

#[test]
#[ignore = "requires dex-server-zd; run ./run-integration-tests.sh"]
fn tls_client_and_pull_worker_use_verified_transport_and_bearer_metadata() {
    let directory = tempfile::tempdir().unwrap();
    let cache = cache(&directory.path().join("cache"));
    let token = uuid::Uuid::new_v4().to_string();
    let token_file = directory.path().join("token");
    std::fs::write(&token_file, &token).unwrap();
    let credential = Arc::new(FileCredential::new(token_file).unwrap());
    let server = ServerProcess::start(directory.path(), &token);
    let registry = Registry::new().register(TlsTransportFlow).unwrap();
    let plaintext = Client::try_new(
        registry.clone(),
        Arc::clone(&cache),
        ClientOptions::new()
            .server_address(server.address.to_string())
            .credential(credential.clone()),
    )
    .unwrap();
    let deadline = Instant::now() + Duration::from_secs(60);
    loop {
        match plaintext.search_flows("", 1) {
            Ok(_) => break,
            Err(error) => {
                assert!(
                    Instant::now() < deadline,
                    "server failed readiness: {error}"
                );
                std::thread::yield_now();
            }
        }
    }
    let certificates = TestCertificates::generate("primary");
    let proxy = TlsProxy::start(&certificates, server.address, &[b"h2"]);
    let address = format!("localhost:{}", proxy.address.port());
    let tls = FlowServiceTlsOptions::new().additional_root_cas_pem(certificates.ca_pem.clone());
    let worker = RunningWorker::start(
        Worker::try_new(
            registry.clone(),
            Arc::clone(&cache),
            WorkerOptions::new()
                .server_address(&address)
                .flow_service_tls(tls.clone())
                .credential(credential.clone())
                .pull(PullWorkerOptions::new().max_concurrent_tasks(2)),
        )
        .unwrap(),
    );
    let runtime = tokio::runtime::Runtime::new().unwrap();
    runtime.block_on(async {
        tokio::time::timeout(
            Duration::from_secs(30),
            worker.worker.wait_pull_registered(),
        )
        .await
        .unwrap()
        .unwrap();
    });
    let client = Client::try_new(
        registry.clone(),
        Arc::clone(&cache),
        ClientOptions::new()
            .server_address(&address)
            .flow_service_tls(tls.clone())
            .credential(credential.clone()),
    )
    .unwrap();
    let flow_id = format!("rust-tls-{}", uuid::Uuid::new_v4());
    let value = "t".repeat(128 * 1024);
    client
        .start_flow(&TlsTransportFlow, &flow_id, value.clone())
        .unwrap();
    let output: String = client
        .wait_for_flow_with_timeout(&flow_id, Duration::from_secs(30))
        .unwrap()
        .single_output()
        .unwrap();
    assert_eq!(value, output);
    assert!(
        proxy
            .handshakes
            .lock()
            .unwrap()
            .iter()
            .any(|(name, protocol)| name.as_deref() == Some("localhost")
                && protocol.as_deref() == Some(b"h2"))
    );
    let no_token = Client::try_new(
        registry.clone(),
        Arc::clone(&cache),
        ClientOptions::new()
            .server_address(&address)
            .flow_service_tls(tls.clone()),
    )
    .unwrap();
    assert_eq!(
        no_token
            .start_flow(
                &TlsTransportFlow,
                &format!("no-token-{}", uuid::Uuid::new_v4()),
                "value".to_string()
            )
            .unwrap_err()
            .service_error()
            .unwrap()
            .code(),
        GrpcCode::Unauthenticated
    );
    for options in [
        None,
        Some(FlowServiceTlsOptions::default()),
        Some(tls.clone().server_name("wrong.example")),
    ] {
        let expected_code = if options.is_none() {
            GrpcCode::Unknown
        } else {
            GrpcCode::Unavailable
        };
        let mut client_options = ClientOptions::new()
            .server_address(&address)
            .credential(credential.clone());
        let mut worker_options = WorkerOptions::new()
            .server_address(&address)
            .credential(credential.clone())
            .attribute_index_sync_timeout(Duration::from_secs(5));
        if let Some(tls) = options {
            client_options = client_options.flow_service_tls(tls.clone());
            worker_options = worker_options.flow_service_tls(tls);
        }
        let rejected =
            Client::try_new(registry.clone(), Arc::clone(&cache), client_options).unwrap();
        let error = rejected.search_flows("", 1).unwrap_err();
        let service = error.service_error().unwrap();
        assert_eq!(service.code(), expected_code);
        if expected_code == GrpcCode::Unavailable {
            assert!(
                service.detail().contains("invalid peer certificate"),
                "TLS failure must include a certificate verification cause: {error}"
            );
        }
        let rejected =
            Worker::try_new(registry.clone(), Arc::clone(&cache), worker_options).unwrap();
        assert_eq!(
            rejected
                .start()
                .unwrap_err()
                .service_error()
                .unwrap()
                .code(),
            expected_code
        );
    }
    for protocols in [vec![b"http/1.1".as_slice()], Vec::new()] {
        let unsupported = TlsProxy::start(&certificates, server.address, &protocols);
        let rejected = Client::try_new(
            registry.clone(),
            Arc::clone(&cache),
            ClientOptions::new()
                .server_address(unsupported.address.to_string())
                .flow_service_tls(tls.clone())
                .credential(credential.clone()),
        )
        .unwrap();
        assert_eq!(
            rejected
                .search_flows("", 1)
                .unwrap_err()
                .service_error()
                .unwrap()
                .code(),
            GrpcCode::Unavailable
        );
    }
    for (address, server_name) in [
        (format!("dns:///{address}"), None),
        (format!("dns://resolver:53/{address}"), None),
        (proxy.address.to_string(), None),
        (proxy.address.to_string(), Some("localhost")),
        (proxy.address.to_string(), Some("127.0.0.1")),
        (proxy.address.to_string(), Some("::1")),
    ] {
        let options = match server_name {
            Some(server_name) => tls.clone().server_name(server_name),
            None => tls.clone(),
        };
        let accepted = Client::try_new(
            registry.clone(),
            Arc::clone(&cache),
            ClientOptions::new()
                .server_address(address)
                .flow_service_tls(options)
                .credential(credential.clone()),
        )
        .unwrap();
        accepted.search_flows("", 1).unwrap();
    }
    assert!(
        proxy
            .handshakes
            .lock()
            .unwrap()
            .iter()
            .any(|(name, protocol)| name.is_none() && protocol.as_deref() == Some(b"h2"))
    );
    drop(worker);
    drop(client);
    drop(plaintext);
    drop(no_token);
    cache.close().unwrap();
}

#[test]
fn default_roots_remain_trusted_with_additional_roots() {
    if let Ok(address) = std::env::var("DEX_TLS_TRUST_PROBE_ADDRESS") {
        let directory = tempfile::tempdir().unwrap();
        let cache = cache(directory.path());
        let additional =
            std::fs::read(std::env::var("DEX_TLS_TRUST_PROBE_ADDITIONAL_ROOT").unwrap()).unwrap();
        let mut options = FlowServiceTlsOptions::new().additional_root_cas_pem(additional);
        if let Ok(server_name) = std::env::var("DEX_TLS_TRUST_PROBE_SERVER_NAME") {
            options = options.server_name(server_name);
        }
        let worker = Worker::try_new(
            Registry::new(),
            Arc::clone(&cache),
            WorkerOptions::new()
                .server_address(&address)
                .flow_service_tls(options.clone()),
        )
        .unwrap();
        let client = Client::try_new(
            Registry::new(),
            Arc::clone(&cache),
            ClientOptions::new()
                .server_address(address)
                .flow_service_tls(options),
        )
        .unwrap();
        let error = client.health_check().unwrap_err();
        let expected = if std::env::var("DEX_TLS_TRUST_PROBE_EMPTY_ROOTS").is_ok() {
            assert!(
                error.to_string().contains("invalid peer certificate"),
                "{error}"
            );
            GrpcCode::Unavailable
        } else {
            GrpcCode::Unimplemented
        };
        assert_eq!(error.service_error().unwrap().code(), expected, "{error}");
        drop(worker);
        drop(client);
        cache.close().unwrap();
    } else {
        let directory = tempfile::tempdir().unwrap();
        let default = TestCertificates::generate("default");
        let additional = TestCertificates::generate("additional");
        let default_file = directory.path().join("default.pem");
        let additional_file = directory.path().join("additional.pem");
        std::fs::write(&default_file, &default.ca_pem).unwrap();
        std::fs::write(&additional_file, &additional.ca_pem).unwrap();
        let empty_file = directory.path().join("empty.pem");
        std::fs::write(&empty_file, []).unwrap();
        for (certificates, native_file, added_file, server_name, expected_trust) in [
            (&default, &default_file, &additional_file, None, true),
            (&additional, &default_file, &additional_file, None, true),
            (&additional, &empty_file, &additional_file, None, true),
            (
                &additional,
                &empty_file,
                &additional_file,
                Some("localhost"),
                true,
            ),
            (
                &additional,
                &empty_file,
                &additional_file,
                Some("::1"),
                true,
            ),
            (&additional, &empty_file, &empty_file, None, false),
        ] {
            let proxy = TlsProxy::start_authority_probe(certificates);
            let address = format!("localhost:{}", proxy.address.port());
            let mut command = std::process::Command::new(std::env::current_exe().unwrap());
            command
                .args([
                    "--exact",
                    "default_roots_remain_trusted_with_additional_roots",
                    "--nocapture",
                ])
                .env("SSL_CERT_FILE", native_file)
                .env("SSL_CERT_DIR", directory.path().join("no-cert-directory"))
                .env("DEX_TLS_TRUST_PROBE_ADDRESS", &address)
                .env("DEX_TLS_TRUST_PROBE_ADDITIONAL_ROOT", added_file);
            if let Some(server_name) = server_name {
                command.env("DEX_TLS_TRUST_PROBE_SERVER_NAME", server_name);
            }
            if !expected_trust {
                command.env("DEX_TLS_TRUST_PROBE_EMPTY_ROOTS", "1");
            }
            let output = command.output().unwrap();
            assert!(
                output.status.success(),
                "trust-store probe failed: {} {}",
                String::from_utf8_lossy(&output.stdout),
                String::from_utf8_lossy(&output.stderr)
            );
            assert_eq!(
                !proxy.handshakes.lock().unwrap().is_empty(),
                expected_trust,
                "TLS handshake must succeed exactly when a root CA is trusted"
            );
            let authorities = proxy.authorities.lock().unwrap();
            if expected_trust {
                let expected_authority = match server_name {
                    Some("::1") => "[::1]",
                    Some(server_name) => server_name,
                    None => &address,
                };
                assert_eq!(authorities.as_slice(), [expected_authority]);
            } else {
                assert!(authorities.is_empty());
            }
        }
    }
}
