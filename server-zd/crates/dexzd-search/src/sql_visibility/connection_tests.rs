// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::future::pending;
use std::io::Write as _;
use std::sync::Arc;
use std::time::Duration;

use dexzd_postgres_connection::ConnectionSettings;
use rcgen::generate_simple_self_signed;
use rustls::pki_types::{PrivateKeyDer, PrivatePkcs8KeyDer};
use tempfile::NamedTempFile;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpListener;
use tokio_postgres::{Config, NoTls};
use tokio_rustls::TlsAcceptor;

use super::{ConnectionSlot, classify_database_error, is_transient_database_error};
use crate::SearchError;

#[tokio::test]
async fn classifies_transient_and_permanent_sqlstates() {
    for (sqlstate, is_transient) in [
        ("08006", true),
        ("08P01", true),
        ("53100", true),
        ("55P03", true),
        ("57014", true),
        ("57P01", true),
        ("57P02", true),
        ("57P03", true),
        ("57P04", true),
        ("57P05", true),
        ("40001", true),
        ("40P01", true),
        ("57P06", false),
        ("40002", false),
        ("40P02", false),
        ("23505", false),
        ("54023", false),
    ] {
        let error = rejected_startup_error(sqlstate).await;
        assert_eq!(
            error
                .as_db_error()
                .map(|database_error| database_error.code().code()),
            Some(sqlstate)
        );
        assert_eq!(
            is_transient_database_error(&error),
            is_transient,
            "{sqlstate}"
        );
        let classified = classify_database_error(
            &ConnectionSettings::from_config(Config::new()),
            "connect to SQL visibility database",
            error,
        );
        match (is_transient, classified) {
            (true, SearchError::Unavailable(message)) | (false, SearchError::Internal(message)) => {
                assert!(message.contains(&format!("SQLSTATE {sqlstate}")));
            }
            (_, classified) => panic!("SQLSTATE {sqlstate} classified as {classified:?}"),
        }
    }
}

#[tokio::test]
async fn connect_error_without_sqlstate_is_unavailable() {
    let listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("fake PostgreSQL listener binds");
    let port = listener
        .local_addr()
        .expect("listener has an address")
        .port();
    let server = tokio::spawn(async move {
        let (mut stream, _) = listener.accept().await.expect("client connects");
        let startup_length = stream.read_u32().await.expect("startup has a length");
        let mut startup = vec![0; startup_length as usize - 4];
        stream
            .read_exact(&mut startup)
            .await
            .expect("client sends startup parameters");
    });
    let mut configuration = Config::new();
    configuration.host("127.0.0.1").port(port).user("test");
    let error = match configuration.connect(NoTls).await {
        Ok(_) => panic!("the closed startup connection cannot connect"),
        Err(error) => error,
    };
    server.await.expect("fake server closes the connection");
    assert!(error.as_db_error().is_none());
    assert!(is_transient_database_error(&error));
    assert!(matches!(
        classify_database_error(
            &ConnectionSettings::from_config(Config::new()),
            "connect to SQL visibility database",
            error
        ),
        SearchError::Unavailable(message) if message.contains("SQLSTATE none")
    ));
}

#[tokio::test]
async fn tls_certificate_failure_includes_rustls_cause_without_dsn() {
    let server_certificate = generate_simple_self_signed(vec!["localhost".to_owned()])
        .expect("generate server certificate");
    let unrelated_ca = generate_simple_self_signed(vec!["unrelated.example".to_owned()])
        .expect("generate unrelated CA");
    let mut root_file = NamedTempFile::new().expect("create root certificate file");
    root_file
        .write_all(unrelated_ca.cert.pem().as_bytes())
        .expect("write unrelated root certificate");
    let server_config = rustls::ServerConfig::builder_with_provider(Arc::new(
        rustls::crypto::aws_lc_rs::default_provider(),
    ))
    .with_safe_default_protocol_versions()
    .expect("server protocol versions")
    .with_no_client_auth()
    .with_single_cert(
        vec![server_certificate.cert.der().clone()],
        PrivateKeyDer::Pkcs8(PrivatePkcs8KeyDer::from(
            server_certificate.signing_key.serialize_der(),
        )),
    )
    .expect("server certificate config");
    let listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("fake TLS listener binds");
    let port = listener.local_addr().expect("listener address").port();
    let server = tokio::spawn(async move {
        let (stream, _) = listener.accept().await.expect("client connects");
        let handshake = TlsAcceptor::from(Arc::new(server_config))
            .accept(stream)
            .await;
        assert!(handshake.is_err(), "client rejects untrusted certificate");
    });
    let dsn = format!(
        "host=localhost hostaddr=127.0.0.1 port={port} user=dex password=hidden-password sslmode=verify-full sslnegotiation=direct sslrootcert={} connect_timeout=5",
        root_file.path().display()
    );
    let settings = ConnectionSettings::parse_for_search(&dsn).expect("valid TLS DSN");
    let error = match ConnectionSlot::open(&settings).await {
        Ok(_) => panic!("untrusted certificate cannot connect"),
        Err(error) => error,
    };
    let message = error.to_string();
    assert!(
        message.contains("certificate") || message.contains("UnknownIssuer"),
        "{message}"
    );
    assert!(!message.contains(&dsn), "{message}");
    assert!(!message.contains("hidden-password"), "{message}");
    server.await.expect("fake server observes TLS rejection");
}

#[tokio::test]
async fn schema_setup_timeout_retires_the_connection() {
    let listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("fake PostgreSQL listener binds");
    let port = listener
        .local_addr()
        .expect("listener has an address")
        .port();
    let server = tokio::spawn(async move {
        let (mut stream, _) = listener.accept().await.expect("client connects");
        let startup_length = stream.read_u32().await.expect("startup has a length");
        let mut startup = vec![0; startup_length as usize - 4];
        stream
            .read_exact(&mut startup)
            .await
            .expect("client sends startup parameters");
        stream
            .write_all(&[b'R', 0, 0, 0, 8, 0, 0, 0, 0, b'Z', 0, 0, 0, 5, b'I'])
            .await
            .expect("server accepts startup");
        assert_eq!(stream.read_u8().await.expect("schema query starts"), b'Q');
        let query_length = stream.read_u32().await.expect("query has a length");
        let mut query = vec![0; query_length as usize - 4];
        stream
            .read_exact(&mut query)
            .await
            .expect("client sends schema query");
        let mut next_byte = [0];
        assert_eq!(stream.read(&mut next_byte).await.expect("socket closes"), 0);
    });
    let mut configuration = Config::new();
    configuration.host("127.0.0.1").port(port).user("test");
    let connection_settings = ConnectionSettings::from_config(configuration);
    let mut slot = ConnectionSlot::open(&connection_settings)
        .await
        .expect("fake server accepts startup");
    let result = tokio::time::timeout(
        Duration::from_secs(1),
        slot.ensure_schema_with_timeout(
            &connection_settings,
            super::VisibilityDialect::ParadeDb,
            Duration::from_millis(50),
        ),
    )
    .await
    .expect("schema setup has a client-side deadline");
    assert!(matches!(
        result,
        Err(SearchError::Unavailable(message)) if message.contains("schema") && message.contains("timed out")
    ));
    assert!(slot.is_disconnected());
    tokio::time::timeout(Duration::from_secs(1), server)
        .await
        .expect("stalled connection closes promptly")
        .expect("stalled connection is retired");
}

#[tokio::test]
async fn connect_error_redacts_the_dsn_and_password_from_server_text() {
    let listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("fake PostgreSQL listener binds");
    let port = listener
        .local_addr()
        .expect("listener has an address")
        .port();
    let dsn =
        format!("host=127.0.0.1 port={port} user=dex password=hidden-password connect_timeout=5");
    let server = tokio::spawn(reject_startup(
        listener,
        format!("SFATAL\0VFATAL\0C28P01\0Mrejected {dsn}\0Dpassword hidden-password expired\0\0"),
    ));
    let settings = ConnectionSettings::parse_for_search(&dsn).expect("valid plaintext DSN");
    let error = match ConnectionSlot::open(&settings).await {
        Ok(_) => panic!("the rejected startup cannot connect"),
        Err(error) => error,
    };
    server.await.expect("fake server sends its rejection");
    let message = error.to_string();
    assert!(message.contains("SQLSTATE 28P01"), "{message}");
    assert!(message.contains("[REDACTED]"), "{message}");
    assert!(!message.contains(&dsn), "{message}");
    assert!(!message.contains("hidden-password"), "{message}");
}

async fn rejected_startup_error(sqlstate: &str) -> tokio_postgres::Error {
    let listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("fake PostgreSQL listener binds");
    let port = listener
        .local_addr()
        .expect("listener has an address")
        .port();
    let response = format!("SFATAL\0VFATAL\0C{sqlstate}\0Mforced rejection\0\0");
    let server = tokio::spawn(reject_startup(listener, response));
    let mut configuration = Config::new();
    configuration.host("127.0.0.1").port(port).user("test");
    let error = match configuration.connect(NoTls).await {
        Ok(_) => panic!("the rejected startup cannot connect"),
        Err(error) => error,
    };
    server.await.expect("fake server sends its rejection");
    error
}

/// Answers one client's startup message with the ErrorResponse fields in `response`.
async fn reject_startup(listener: TcpListener, response: String) {
    let (mut stream, _) = listener.accept().await.expect("client connects");
    let startup_length = stream.read_u32().await.expect("startup has a length");
    let mut startup = vec![0; startup_length as usize - 4];
    stream
        .read_exact(&mut startup)
        .await
        .expect("client sends startup parameters");
    let mut message = vec![b'E'];
    message.extend_from_slice(&((response.len() + 4) as u32).to_be_bytes());
    message.extend_from_slice(response.as_bytes());
    stream
        .write_all(&message)
        .await
        .expect("server rejects startup");
}

#[tokio::test]
async fn reconnects_after_a_stalled_postgres_handshake() {
    let listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("fake PostgreSQL listener binds");
    let port = listener
        .local_addr()
        .expect("listener has an address")
        .port();
    let server = tokio::spawn(async move {
        let mut connections = Vec::new();
        for connection_number in 0..3 {
            let (mut stream, _) = listener.accept().await.expect("client connects");
            let startup_length = stream.read_u32().await.expect("startup has a length");
            let mut startup = vec![0; startup_length as usize - 4];
            stream
                .read_exact(&mut startup)
                .await
                .expect("client sends startup parameters");
            if connection_number != 1 {
                stream
                    .write_all(&[b'R', 0, 0, 0, 8, 0, 0, 0, 0, b'Z', 0, 0, 0, 5, b'I'])
                    .await
                    .expect("server accepts startup");
            }
            connections.push(stream);
        }
        pending::<()>().await;
    });
    let mut configuration = tokio_postgres::Config::new();
    configuration
        .host("127.0.0.1")
        .port(port)
        .user("test")
        .dbname("test")
        .connect_timeout(Duration::from_secs(1));

    let connection_settings = ConnectionSettings::from_config(configuration);
    let mut slot = ConnectionSlot::open(&connection_settings)
        .await
        .expect("initial connection opens");
    slot.invalidate();
    assert!(slot.reconnect(&connection_settings).await.is_err());
    assert!(slot.is_disconnected());
    slot.reconnect(&connection_settings)
        .await
        .expect("a fresh connection replaces the failed slot");
    assert!(!slot.is_disconnected());
    server.abort();
    let error = server.await.expect_err("fake server is aborted");
    assert!(error.is_cancelled());
}
