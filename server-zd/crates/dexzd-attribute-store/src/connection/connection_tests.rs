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
use tokio_postgres::config::SslMode;
use tokio_rustls::TlsAcceptor;

use super::{ConnectionSettings, TlsParameters, split_tls_parameters};
use crate::error::AttributeStoreError;
use crate::settings::AttributeStoreSettings;
use crate::store::AttributeStore;

/// The Postgres SSLRequest: length 8 and request code 80877103.
const SSL_REQUEST: [u8; 8] = [0, 0, 0, 8, 4, 210, 22, 47];

/// The SQLSTATE the fake server returns once TLS and the startup message succeed.
const REACHED_SERVER_SQLSTATE: &str = "28000";

#[test]
fn url_dsn_tls_parameters_are_removed_and_decoded() {
    let (remaining, tls) = split_tls_parameters(
        "postgres://dex:p%40ss@db.example.com:5432/dex?sslmode=verify-full\
         &sslrootcert=%2Fetc%2Fssl%2Frds%20ca.pem&application_name=zd",
    )
    .expect("valid URL");
    assert_eq!(
        remaining,
        "postgres://dex:p%40ss@db.example.com:5432/dex?application_name=zd"
    );
    assert_eq!(
        tls,
        TlsParameters {
            ssl_mode: Some("verify-full".to_owned()),
            ssl_root_cert: Some("/etc/ssl/rds ca.pem".to_owned()),
            ssl_sni: None,
            ssl_negotiation: None,
        }
    );

    let (remaining, tls) =
        split_tls_parameters("postgresql://u:pa?ss@h/db?sslmode=require&sslsni=0")
            .expect("valid URL");
    assert_eq!(remaining, "postgresql://u:pa?ss@h/db");
    assert_eq!(tls.ssl_mode.as_deref(), Some("require"));
    assert_eq!(tls.ssl_sni.as_deref(), Some("0"));

    let (remaining, tls) = split_tls_parameters("postgres://u@h/db").expect("valid URL");
    assert_eq!(remaining, "postgres://u@h/db");
    assert_eq!(tls, TlsParameters::default());

    let error = split_tls_parameters("postgres://u:secret@h/db?sslmode").expect_err("unterminated");
    assert!(!error.contains("secret"), "{error}");
}

#[test]
fn key_value_dsn_tls_parameters_are_removed_and_unquoted() {
    let (remaining, tls) = split_tls_parameters(
        r"host=db port=5432 sslmode='verify-full' sslrootcert = '/etc/ssl/my ca.pem' password='a b\'c' sslnegotiation=direct",
    )
    .expect("valid key=value string");
    assert_eq!(
        remaining,
        r"host=db port=5432 password='a b\'c' sslnegotiation=direct"
    );
    assert_eq!(tls.ssl_mode.as_deref(), Some("verify-full"));
    assert_eq!(tls.ssl_root_cert.as_deref(), Some("/etc/ssl/my ca.pem"));
    assert_eq!(tls.ssl_negotiation.as_deref(), Some("direct"));
    let config = remaining
        .parse::<tokio_postgres::Config>()
        .expect("tokio-postgres accepts the rest");
    assert_eq!(config.get_password(), Some(&b"a b'c"[..]));

    let (remaining, tls) = split_tls_parameters(r"host=db sslrootcert=/a\ b.pem user=dex")
        .expect("valid key=value string");
    assert_eq!(remaining, "host=db user=dex");
    assert_eq!(tls.ssl_root_cert.as_deref(), Some("/a b.pem"));

    for invalid in ["host", "password='secret", "host= ", "password=secret\\"] {
        let error = split_tls_parameters(invalid).expect_err(invalid);
        assert!(!error.contains("secret"), "{error}");
    }
}

#[test]
fn ssl_modes_follow_pgx() {
    let ca = issue_certificates(&["localhost"]);
    let ca_file = write_ca(&ca.ca_pem);
    let ca_path = ca_file.path().display().to_string();
    for (dsn, ssl_mode, has_tls) in [
        (
            "host=localhost sslmode=disable".to_owned(),
            SslMode::Disable,
            false,
        ),
        ("host=localhost".to_owned(), SslMode::Prefer, true),
        (
            "host=localhost sslmode=allow".to_owned(),
            SslMode::Prefer,
            true,
        ),
        (
            "host=localhost sslmode=require".to_owned(),
            SslMode::Require,
            true,
        ),
        (
            "host=localhost sslnegotiation=direct".to_owned(),
            SslMode::Require,
            true,
        ),
        (
            format!("host=localhost sslmode=require sslrootcert={ca_path}"),
            SslMode::Require,
            true,
        ),
        (
            format!("host=localhost sslmode=verify-ca sslrootcert={ca_path}"),
            SslMode::Require,
            true,
        ),
        (
            format!("host=localhost sslmode=verify-full sslrootcert={ca_path}"),
            SslMode::Require,
            true,
        ),
        (
            format!("postgres://dex@localhost/dex?sslmode=verify-full&sslrootcert={ca_path}"),
            SslMode::Require,
            true,
        ),
    ] {
        let settings = ConnectionSettings::parse(&dsn).expect(&dsn);
        assert_eq!(settings.config.get_ssl_mode(), ssl_mode, "{dsn}");
        assert_eq!(settings.tls.is_some(), has_tls, "{dsn}");
    }
    for (dsn, message) in [
        (
            "host=localhost sslmode=bogus",
            "sslmode \"bogus\" is invalid",
        ),
        (
            "host=localhost sslmode=verify-full sslrootcert=/nonexistent/ca.pem",
            "read sslrootcert",
        ),
        (
            "host=localhost port=notanumber password=secret",
            "parse connection string",
        ),
    ] {
        let error = ConnectionSettings::parse(dsn).err().expect(dsn);
        assert!(error.contains(message), "{dsn}: {error}");
        assert!(!error.contains("secret"), "{error}");
    }
}

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
