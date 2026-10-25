// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::io::Write as _;

use rcgen::{BasicConstraints, CertificateParams, CertifiedIssuer, DnType, IsCa, KeyPair};
use tempfile::NamedTempFile;
use tokio::net::TcpListener;
use tokio_postgres::config::{SslMode, SslNegotiation};
use tokio_rustls::LazyConfigAcceptor;

use super::ConnectionSettings;
use crate::dsn::{TlsParameters, split_tls_parameters};

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
    let ca_pem = issue_ca_certificate();
    let ca_file = write_ca(&ca_pem);
    let ca_path = ca_file.path().display().to_string();
    for (dsn, ssl_mode, has_tls) in [
        (
            "host=localhost sslmode=disable".to_owned(),
            SslMode::Disable,
            false,
        ),
        ("host=localhost".to_owned(), SslMode::Prefer, true),
        (
            "host=localhost sslsni=true".to_owned(),
            SslMode::Prefer,
            true,
        ),
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
        assert_eq!(settings.configuration().get_ssl_mode(), ssl_mode, "{dsn}");
        assert_eq!(settings.tls.is_some(), has_tls, "{dsn}");
    }
    for (dsn, message) in [
        ("host=localhost sslmode=bogus", "sslmode is invalid"),
        ("host=localhost sslnegotiation=bogus", "sslnegotiation"),
        (
            "host=localhost sslmode=allow sslnegotiation=direct",
            "sslnegotiation=direct",
        ),
        (
            "host=localhost sslmode=verify-full sslrootcert=/nonexistent/ca.pem",
            "read the sslrootcert file",
        ),
        (
            "host=localhost port=notanumber password=secret",
            "connection string keyword port has an invalid value",
        ),
    ] {
        let error = ConnectionSettings::parse(dsn).err().expect(dsn).to_string();
        assert!(error.contains(message), "{dsn}: {error}");
        assert!(!error.contains("secret"), "{error}");
    }
}

fn issue_ca_certificate() -> String {
    let ca_key = KeyPair::generate().expect("generate a CA key");
    let mut ca_params = CertificateParams::new(Vec::<String>::new()).expect("CA parameters");
    ca_params.is_ca = IsCa::Ca(BasicConstraints::Unconstrained);
    ca_params
        .distinguished_name
        .push(DnType::CommonName, "Postgres connection test CA");
    let ca = CertifiedIssuer::self_signed(ca_params, ca_key).expect("self-sign the CA");
    ca.pem()
}

fn write_ca(ca_pem: &str) -> NamedTempFile {
    let mut file = NamedTempFile::new().expect("create a CA bundle file");
    file.write_all(ca_pem.as_bytes())
        .expect("write the CA bundle");
    file
}

#[test]
fn search_plaintext_default_preserves_no_tls_connections() {
    let plain = ConnectionSettings::parse_for_search("postgres://dex:secret@localhost/dex")
        .expect("valid plaintext DSN");
    assert_eq!(plain.configuration().get_ssl_mode(), SslMode::Disable);
    assert!(plain.tls.is_none());

    let timed_plain = ConnectionSettings::parse_for_search(
        "postgres://dex:secret@localhost/dex?connect_timeout=5",
    )
    .expect("valid plaintext DSN with a timeout");
    assert_eq!(timed_plain.configuration().get_ssl_mode(), SslMode::Disable);
    assert!(timed_plain.tls.is_none());

    let direct = ConnectionSettings::parse_for_search(
        "postgres://dex:secret@localhost/dex?sslnegotiation=direct",
    )
    .expect("valid direct negotiation DSN");
    assert_eq!(direct.configuration().get_ssl_mode(), SslMode::Require);
    assert_eq!(
        direct.configuration().get_ssl_negotiation(),
        SslNegotiation::Direct
    );
    assert!(direct.tls.is_some());

    let invalid_sni = ConnectionSettings::parse_for_search("host=localhost sslsni=bogus")
        .err()
        .expect("search must reject invalid sslsni");
    assert!(invalid_sni.to_string().contains("sslsni is invalid"));
}

#[tokio::test]
async fn direct_negotiation_sends_tls_client_hello_with_postgresql_alpn() {
    let listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("bind fake PostgreSQL listener");
    let port = listener.local_addr().expect("listener address").port();
    let server = tokio::spawn(async move {
        let (stream, _) = listener.accept().await.expect("client connects");
        let mut first_byte = [0_u8; 1];
        stream
            .peek(&mut first_byte)
            .await
            .expect("peek at first byte");
        assert_eq!(first_byte[0], 0x16, "direct TLS starts with ClientHello");
        let handshake = LazyConfigAcceptor::new(rustls::server::Acceptor::default(), stream)
            .await
            .expect("parse ClientHello");
        let offered_alpn = handshake
            .client_hello()
            .alpn()
            .expect("ClientHello offers ALPN")
            .map(|protocol| protocol.to_vec())
            .collect::<Vec<_>>();
        assert_eq!(offered_alpn, vec![b"postgresql".to_vec()]);
    });
    let dsn = format!(
        "host=localhost hostaddr=127.0.0.1 port={port} user=dex sslmode=require sslnegotiation=direct connect_timeout=5"
    );
    let settings = ConnectionSettings::parse_for_search(&dsn).expect("valid direct TLS DSN");
    let error = match settings.connect().await {
        Ok(_) => panic!("fake listener has no TLS server"),
        Err(error) => error,
    };
    assert!(matches!(error, super::ConnectionError::Database(_)));
    server.await.expect("fake server inspects ClientHello");
}
