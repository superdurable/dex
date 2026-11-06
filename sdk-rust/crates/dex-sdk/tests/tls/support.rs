// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::fs;
use std::net::SocketAddr;
use std::path::Path;
use std::process::{Child, Command, Stdio};
use std::sync::{Arc, Mutex};

use rustls::ServerConfig;
use rustls::pki_types::{CertificateDer, PrivateKeyDer, pem::PemObject};
use tokio::net::{TcpListener, TcpStream};
use tokio::runtime::Runtime;
use tokio::task::JoinSet;
use tokio_rustls::TlsAcceptor;

pub(crate) struct TestCertificates {
    pub(crate) ca_pem: Vec<u8>,
    certificate: Vec<u8>,
    private_key: Vec<u8>,
}

impl TestCertificates {
    pub(crate) fn generate(name: &str) -> Self {
        use rcgen::{
            BasicConstraints, CertificateParams, DnType, ExtendedKeyUsagePurpose, IsCa, Issuer,
            KeyPair, KeyUsagePurpose,
        };
        let mut ca_params =
            CertificateParams::new(Vec::<String>::new()).expect("empty CA names are valid");
        ca_params.is_ca = IsCa::Ca(BasicConstraints::Unconstrained);
        ca_params
            .distinguished_name
            .push(DnType::CommonName, format!("Dex test CA {name}"));
        ca_params.key_usages.push(KeyUsagePurpose::KeyCertSign);
        let ca_key = KeyPair::generate().expect("generate CA key");
        let ca = ca_params.self_signed(&ca_key).expect("issue test CA");
        let issuer = Issuer::new(ca_params, ca_key);
        let mut server_params = CertificateParams::new(vec![
            "localhost".to_string(),
            "127.0.0.1".to_string(),
            "::1".to_string(),
        ])
        .expect("test server names are valid");
        server_params
            .key_usages
            .push(KeyUsagePurpose::DigitalSignature);
        server_params
            .extended_key_usages
            .push(ExtendedKeyUsagePurpose::ServerAuth);
        let server_key = KeyPair::generate().expect("generate server key");
        let certificate = server_params
            .signed_by(&server_key, &issuer)
            .expect("issue server certificate");
        Self {
            ca_pem: ca.pem().into_bytes(),
            certificate: certificate.pem().into_bytes(),
            private_key: server_key.serialize_pem().into_bytes(),
        }
    }
}

pub(crate) type HandshakeObservations = Arc<Mutex<Vec<(Option<String>, Option<Vec<u8>>)>>>;

pub(crate) struct TlsProxy {
    pub(crate) address: SocketAddr,
    pub(crate) handshakes: HandshakeObservations,
    runtime: Runtime,
    task: tokio::task::JoinHandle<()>,
}

impl TlsProxy {
    pub(crate) fn start(
        certificates: &TestCertificates,
        upstream: SocketAddr,
        alpn: &[&[u8]],
    ) -> Self {
        let certificate = CertificateDer::from_pem_slice(&certificates.certificate)
            .expect("parse generated server certificate");
        let private_key = PrivateKeyDer::from_pem_slice(&certificates.private_key)
            .expect("parse generated server key");
        let mut config =
            ServerConfig::builder_with_provider(Arc::new(rustls::crypto::ring::default_provider()))
                .with_safe_default_protocol_versions()
                .expect("ring supports TLS 1.2 and TLS 1.3")
                .with_no_client_auth()
                .with_single_cert(vec![certificate], private_key)
                .expect("server certificate matches its private key");
        config.alpn_protocols = alpn.iter().map(|value| value.to_vec()).collect();
        let runtime = Runtime::new().expect("create TLS proxy runtime");
        let listener = runtime
            .block_on(TcpListener::bind("127.0.0.1:0"))
            .expect("bind TLS proxy listener");
        let address = listener.local_addr().expect("read bound listener address");
        let handshakes = Arc::new(Mutex::new(Vec::new()));
        let acceptor = TlsAcceptor::from(Arc::new(config));
        let observations = Arc::clone(&handshakes);
        let task = runtime.spawn(Self::accept_connections(
            listener,
            acceptor,
            observations,
            upstream,
        ));
        Self {
            address,
            handshakes,
            runtime,
            task,
        }
    }

    async fn accept_connections(
        listener: TcpListener,
        acceptor: TlsAcceptor,
        observations: HandshakeObservations,
        upstream: SocketAddr,
    ) {
        let mut connections = JoinSet::new();
        loop {
            tokio::select! {
                connection = listener.accept() => {
                    let (connection, _) = connection.expect("accept TLS proxy connection");
                    connections.spawn(Self::forward(acceptor.clone(), connection, upstream, Arc::clone(&observations)));
                }
                completion = connections.join_next(), if !connections.is_empty() => { completion.expect("connection task exists").expect("TLS forwarding task completed"); }
            }
        }
    }

    async fn forward(
        acceptor: TlsAcceptor,
        connection: TcpStream,
        upstream: SocketAddr,
        observations: HandshakeObservations,
    ) {
        match acceptor.accept(connection).await {
            Ok(mut downstream) => {
                let session = downstream.get_ref().1;
                observations
                    .lock()
                    .expect("lock TLS handshake observations")
                    .push((
                        session.server_name().map(str::to_owned),
                        session.alpn_protocol().map(<[u8]>::to_vec),
                    ));
                let mut upstream = match TcpStream::connect(upstream).await {
                    Ok(connection) => connection,
                    Err(error) => {
                        tracing::debug!(%error, "test TLS upstream closed");
                        return;
                    }
                };
                if let Err(error) =
                    tokio::io::copy_bidirectional(&mut downstream, &mut upstream).await
                {
                    // Failed verification and teardown can reset either peer; no further forwarding is possible.
                    tracing::debug!(%error, "test TLS forwarding ended");
                }
            }
            Err(error) => tracing::debug!(%error, "test TLS handshake rejected"),
        }
    }
}

impl Drop for TlsProxy {
    fn drop(&mut self) {
        self.task.abort();
        let result = self.runtime.block_on(&mut self.task);
        assert!(
            result
                .expect_err("proxy accept loop was aborted")
                .is_cancelled()
        );
    }
}

pub(crate) struct ServerProcess {
    child: Child,
    pub(crate) address: SocketAddr,
}

impl ServerProcess {
    pub(crate) fn start(directory: &Path, token: &str) -> Self {
        use sha2::{Digest, Sha256};
        let listener = std::net::TcpListener::bind("127.0.0.1:0").expect("reserve server port");
        let address = listener.local_addr().expect("read bound listener address");
        drop(listener);
        let token_file = directory.join("token-hashes.yaml");
        fs::write(
            &token_file,
            format!("tokens:\n  - sha256: {:x}\n", Sha256::digest(token)),
        )
        .expect("write token hashes");
        let config_file = directory.join("server.yaml");
        fs::write(&config_file, format!("auth:\n  authenticators:\n    - kind: staticTokens\n      file: {}\ndev:\n  durabilityMode: processCrashSafe\n", token_file.display())).expect("write authenticated server config");
        let binary = std::env::var("DEX_SERVER_ZD_BINARY")
            .expect("set DEX_SERVER_ZD_BINARY or use run-integration-tests.sh");
        let log = fs::File::create(directory.join("server.log")).expect("create server log");
        let child = Command::new(binary)
            .arg("dev")
            .arg("--data-dir")
            .arg(directory.join("zd"))
            .arg("--listen")
            .arg(address.to_string())
            .arg("--config")
            .arg(config_file)
            .stdout(Stdio::from(
                log.try_clone().expect("clone server log handle"),
            ))
            .stderr(Stdio::from(log))
            .spawn()
            .expect("start test server-zd");
        Self { child, address }
    }
}

impl Drop for ServerProcess {
    fn drop(&mut self) {
        if self
            .child
            .try_wait()
            .expect("check test server exit")
            .is_none()
        {
            self.child.kill().expect("stop test server-zd");
        }
        self.child.wait().expect("reap test server-zd");
    }
}
