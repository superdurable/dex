// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::error::Error;
use std::io::Write as _;
use std::sync::Arc;

use rcgen::{BasicConstraints, CertificateParams, CertifiedIssuer, DnType, IsCa, KeyPair};
use rustls::pki_types::{CertificateDer, PrivateKeyDer, PrivatePkcs8KeyDer};
use tempfile::NamedTempFile;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpListener;
use tokio_rustls::TlsAcceptor;

use super::ConnectionSettings;
use crate::error::AttributeStoreError;
use crate::settings::AttributeStoreSettings;
use crate::store::AttributeStore;

/// The Postgres SSLRequest: length 8 and request code 80877103.
const SSL_REQUEST: [u8; 8] = [0, 0, 0, 8, 4, 210, 22, 47];

/// The SQLSTATE the fake server returns once TLS and the startup message succeed.
const REACHED_SERVER_SQLSTATE: &str = "28000";

#[tokio::test]
async fn verify_full_accepts_a_matching_certificate_from_the_bundle() {
    let certificates = issue_certificates(&["localhost"]);
    let ca_file = write_ca(&certificates.ca_pem);
    let error = connect_through_fake_server(
        &certificates,
        &format!(
            "sslmode=verify-full sslrootcert={}",
            ca_file.path().display()
        ),
    )
    .await;
    assert_eq!(error.sqlstate(), Some(REACHED_SERVER_SQLSTATE), "{error}");
}

#[tokio::test]
async fn verify_full_rejects_a_certificate_for_another_host() {
    let certificates = issue_certificates(&["db.internal"]);
    let ca_file = write_ca(&certificates.ca_pem);
    let error = connect_through_fake_server(
        &certificates,
        &format!(
            "sslmode=verify-full sslrootcert={}",
            ca_file.path().display()
        ),
    )
    .await;
    assert_eq!(error.sqlstate(), None, "{error}");
    assert!(error.is_retryable(), "{error}");
}

#[tokio::test]
async fn verify_full_rejects_a_certificate_from_another_authority() {
    let certificates = issue_certificates(&["localhost"]);
    let other_authority = issue_certificates(&["localhost"]);
    let ca_file = write_ca(&other_authority.ca_pem);
    let error = connect_through_fake_server(
        &certificates,
        &format!(
            "sslmode=verify-full sslrootcert={}",
            ca_file.path().display()
        ),
    )
    .await;
    assert_eq!(error.sqlstate(), None, "{error}");
}

#[tokio::test]
async fn verify_ca_ignores_the_host_name() {
    let certificates = issue_certificates(&["db.internal"]);
    let ca_file = write_ca(&certificates.ca_pem);
    let error = connect_through_fake_server(
        &certificates,
        &format!("sslmode=verify-ca sslrootcert={}", ca_file.path().display()),
    )
    .await;
    assert_eq!(error.sqlstate(), Some(REACHED_SERVER_SQLSTATE), "{error}");
}

#[tokio::test]
async fn require_without_a_bundle_accepts_any_certificate() {
    let certificates = issue_certificates(&["db.internal"]);
    let error = connect_through_fake_server(&certificates, "sslmode=require").await;
    assert_eq!(error.sqlstate(), Some(REACHED_SERVER_SQLSTATE), "{error}");
}

#[tokio::test]
async fn require_with_a_bundle_verifies_the_chain() {
    let certificates = issue_certificates(&["localhost"]);
    let other_authority = issue_certificates(&["localhost"]);
    let ca_file = write_ca(&other_authority.ca_pem);
    let error = connect_through_fake_server(
        &certificates,
        &format!("sslmode=require sslrootcert={}", ca_file.path().display()),
    )
    .await;
    assert_eq!(error.sqlstate(), None, "{error}");
}

#[tokio::test]
async fn unreachable_database_fails_startup_with_a_redacted_retryable_error() {
    let listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("bind a free port");
    let port = listener.local_addr().expect("listener address").port();
    drop(listener);
    let dsn = format!(
        "postgres://dex:secret-password@127.0.0.1:{port}/dex?sslmode=disable&connect_timeout=5"
    );
    let error = match AttributeStore::connect_and_validate(AttributeStoreSettings::new(
        "orders", dsn, "orders",
    ))
    .await
    {
        Ok(_) => panic!("startup must fail without a database"),
        Err(error) => error,
    };
    assert!(
        matches!(error, AttributeStoreError::Database { .. }),
        "{error}"
    );
    assert!(error.is_retryable());
    let message = error.to_string();
    assert!(message.contains("connect to Postgres"), "{message}");
    assert!(!message.contains("secret-password"), "{message}");
}

#[tokio::test]
async fn dsn_errors_never_reveal_dsn_content() {
    let s3cr3t_directory = tempfile::Builder::new()
        .prefix("s3cr3t")
        .tempdir()
        .expect("create a directory with a secret name");
    let s3cr3t_ca_path = s3cr3t_directory.path().join("missing-ca.pem");
    let invalid = [
        (
            "host=localhost password=s3cr3t-a s3cr3t-b".to_owned(),
            "connection string has a keyword without '='",
        ),
        (
            "host=localhost password=s3cr3t-a s3cr3t-b=c".to_owned(),
            "connection string has an unsupported keyword or is malformed",
        ),
        (
            "host=localhost password='s3cr3t-a".to_owned(),
            "connection string has an unterminated quoted value",
        ),
        (
            r"host=localhost password=s3cr3t\".to_owned(),
            "connection string ends with an unfinished escape",
        ),
        (
            "host=localhost password=s3cr3t s3cr3t-key=".to_owned(),
            "connection string has a keyword without a value",
        ),
        (
            "host=localhost password=s3cr3t =s3cr3t".to_owned(),
            "connection string has a parameter without a keyword",
        ),
        (
            "host=localhost password=s3cr3t port=s3cr3t".to_owned(),
            "connection string keyword port has an invalid value",
        ),
        (
            "host=localhost password=s3cr3t sslmode=s3cr3t".to_owned(),
            "sslmode is invalid",
        ),
        (
            format!(
                "host=localhost password=s3cr3t sslmode=verify-full sslrootcert={}",
                s3cr3t_ca_path.display()
            ),
            "read the sslrootcert file",
        ),
        (
            "postgres://dex:s3cr3t@localhost/dex?s3cr3t-key=s3cr3t".to_owned(),
            "connection string has an unsupported keyword or is malformed",
        ),
        (
            "postgres://dex:s3cr3t@localhost/dex?sslmode".to_owned(),
            "connection URL has an unterminated parameter",
        ),
        (
            "postgres://dex:s3cr3t@localhost:s3cr3t/dex".to_owned(),
            "connection string keyword port has an invalid value",
        ),
        (
            "postgres://dex:s3cr3t@localhost/dex?application_name=%FFs3cr3t".to_owned(),
            "connection URL parameter is not UTF-8",
        ),
    ];
    for (dsn, expected) in invalid {
        let error =
            connect_and_validate_error(AttributeStoreSettings::new("tls", &dsn, "orders")).await;
        let AttributeStoreError::InvalidConnectionSettings { reason, .. } = &error else {
            panic!("{dsn}: {error:?}");
        };
        assert!(reason.starts_with(expected), "{dsn}: {reason}");
        assert_reveals_no_secret(&error);
    }

    let listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("bind a free port");
    let port = listener.local_addr().expect("listener address").port();
    drop(listener);
    let error = connect_and_validate_error(AttributeStoreSettings::new(
        "tls",
        format!("host=127.0.0.1 port={port} user=s3cr3t-user password='s3cr3t a' sslmode=disable"),
        "orders",
    ))
    .await;
    assert!(
        matches!(error, AttributeStoreError::Database { .. }),
        "{error:?}"
    );
    assert_reveals_no_secret(&error);
}

#[tokio::test]
async fn empty_row_key_is_unset_like_go() {
    let mut settings = AttributeStoreSettings::new(
        "orders",
        "host=localhost sslmode=disable unsupported_keyword=1",
        "orders",
    );
    settings.row_key = Some(String::new());
    let error = connect_and_validate_error(settings).await;
    assert!(
        matches!(error, AttributeStoreError::InvalidConnectionSettings { .. }),
        "{error:?}"
    );
}

async fn connect_and_validate_error(settings: AttributeStoreSettings) -> AttributeStoreError {
    match AttributeStore::connect_and_validate(settings).await {
        Ok(_) => panic!("startup must fail"),
        Err(error) => error,
    }
}

fn assert_reveals_no_secret(error: &AttributeStoreError) {
    let display = error.to_string();
    let debug = format!("{error:?}");
    assert!(!display.contains("s3cr3t"), "{display}");
    assert!(!debug.contains("s3cr3t"), "{debug}");
}

struct TestCertificates {
    ca_pem: String,
    server_chain: Vec<CertificateDer<'static>>,
    server_key: Vec<u8>,
}

fn issue_certificates(server_names: &[&str]) -> TestCertificates {
    let ca_key = KeyPair::generate().expect("generate a CA key");
    let mut ca_params = CertificateParams::new(Vec::<String>::new()).expect("CA parameters");
    ca_params.is_ca = IsCa::Ca(BasicConstraints::Unconstrained);
    ca_params
        .distinguished_name
        .push(DnType::CommonName, "Attribute Store test CA");
    let ca = CertifiedIssuer::self_signed(ca_params, ca_key).expect("self-sign the CA");
    let server_key = KeyPair::generate().expect("generate a server key");
    let server_names = server_names
        .iter()
        .map(|name| (*name).to_owned())
        .collect::<Vec<_>>();
    let server_certificate = CertificateParams::new(server_names)
        .expect("server parameters")
        .signed_by(&server_key, &*ca)
        .expect("sign the server certificate");
    TestCertificates {
        ca_pem: ca.pem(),
        server_chain: vec![server_certificate.der().clone()],
        server_key: server_key.serialize_der(),
    }
}

fn write_ca(ca_pem: &str) -> NamedTempFile {
    let mut file = NamedTempFile::new().expect("create a CA bundle file");
    file.write_all(ca_pem.as_bytes())
        .expect("write the CA bundle");
    file
}

/// The fake server rejects startup with SQLSTATE 28000, which proves the TLS handshake succeeded.
async fn connect_through_fake_server(
    certificates: &TestCertificates,
    tls_parameters: &str,
) -> AttributeStoreError {
    let server_config = rustls::ServerConfig::builder_with_provider(Arc::new(
        rustls::crypto::aws_lc_rs::default_provider(),
    ))
    .with_safe_default_protocol_versions()
    .expect("server protocol versions")
    .with_no_client_auth()
    .with_single_cert(
        certificates.server_chain.clone(),
        PrivateKeyDer::Pkcs8(PrivatePkcs8KeyDer::from(certificates.server_key.clone())),
    )
    .expect("server certificate");
    let listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("bind the fake server");
    let port = listener.local_addr().expect("fake server address").port();
    let server = tokio::spawn(serve_one_connection(listener, Arc::new(server_config)));
    let dsn = format!(
        "host=localhost hostaddr=127.0.0.1 port={port} user=dex dbname=dex connect_timeout=5 {tls_parameters}"
    );
    let settings = AttributeStoreSettings::new("tls", dsn.clone(), "orders");
    let connection_settings = ConnectionSettings::parse(&dsn).expect("valid DSN");
    let result = connection_settings.connect(&settings).await;
    server.abort();
    match result {
        Ok(_) => panic!("the fake server always rejects the startup message"),
        Err(error) => error,
    }
}

async fn serve_one_connection(
    listener: TcpListener,
    server_config: Arc<rustls::ServerConfig>,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let (mut socket, _) = listener.accept().await?;
    let mut ssl_request = [0_u8; 8];
    socket.read_exact(&mut ssl_request).await?;
    if ssl_request != SSL_REQUEST {
        return Err("the client did not request TLS".into());
    }
    socket.write_all(b"S").await?;
    let mut tls = TlsAcceptor::from(server_config).accept(socket).await?;
    let mut length = [0_u8; 4];
    tls.read_exact(&mut length).await?;
    let mut startup = vec![0_u8; usize::try_from(u32::from_be_bytes(length))? - 4];
    tls.read_exact(&mut startup).await?;
    let fields = format!(
        "SFATAL\0VFATAL\0C{REACHED_SERVER_SQLSTATE}\0Mthe test server was reached over TLS\0\0"
    );
    let mut response = vec![b'E'];
    response.extend_from_slice(&u32::try_from(fields.len() + 4)?.to_be_bytes());
    response.extend_from_slice(fields.as_bytes());
    tls.write_all(&response).await?;
    tls.flush().await?;
    Ok(())
}
