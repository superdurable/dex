// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

mod dsn;
mod tls;

#[cfg(test)]
mod connection_tests;

use std::time::Duration;

use tokio::task::JoinHandle;
use tokio_postgres::config::SslMode;
use tokio_postgres::{Client, Config, NoTls};
use tokio_postgres_rustls::MakeRustlsConnect;

use crate::error::{AttributeStoreError, database_error};
use crate::settings::AttributeStoreSettings;

pub(crate) use dsn::{TlsParameters, split_tls_parameters};
pub(crate) use tls::{CertificateVerification, client_config, load_root_certificates};

/// Applies when the DSN has no `connect_timeout`; it also bounds the TLS handshake and authentication.
const DEFAULT_CONNECT_TIMEOUT: Duration = Duration::from_secs(10);

/// A parsed DSN: the tokio-postgres configuration and the TLS connector its `sslmode` requires.
pub(crate) struct ConnectionSettings {
    config: Config,
    tls: Option<MakeRustlsConnect>,
}

/// One open connection and the task that drives it; dropping it closes the connection.
pub(crate) struct ConnectionSlot {
    pub(crate) client: Client,
    connection_task: JoinHandle<()>,
}

impl ConnectionSettings {
    /// Parses a DSN with pgx's `sslmode` and `sslrootcert` semantics; the error never quotes the DSN.
    pub(crate) fn parse(dsn: &str) -> Result<Self, String> {
        let (remaining, parameters) = split_tls_parameters(dsn)?;
        let mut config = remaining
            .parse::<Config>()
            .map_err(|error| format!("parse connection string: {error}"))?;
        let (ssl_mode, verification) = resolve_tls(&parameters)?;
        config.ssl_mode(ssl_mode);
        let tls = match verification {
            None => None,
            Some(verification) => {
                let is_direct = parameters.ssl_negotiation.as_deref() == Some("direct");
                let is_sni_enabled = parameters.ssl_sni.as_deref() != Some("0");
                let client_config = client_config(verification, is_direct, is_sni_enabled)?;
                Some(MakeRustlsConnect::new(client_config))
            }
        };
        Ok(Self { config, tls })
    }

    /// Opens a connection bounded by the DSN's `connect_timeout` or ten seconds.
    pub(crate) async fn connect(
        &self,
        settings: &AttributeStoreSettings,
    ) -> Result<ConnectionSlot, AttributeStoreError> {
        let timeout = self
            .config
            .get_connect_timeout()
            .copied()
            .unwrap_or(DEFAULT_CONNECT_TIMEOUT);
        let store = settings.name.clone();
        let connecting = async {
            match &self.tls {
                None => {
                    let (client, connection) = self.config.connect(NoTls).await?;
                    Ok(ConnectionSlot::spawn(store, client, connection))
                }
                Some(tls) => {
                    let (client, connection) = self.config.connect(tls.clone()).await?;
                    Ok(ConnectionSlot::spawn(store, client, connection))
                }
            }
        };
        match tokio::time::timeout(timeout, connecting).await {
            Ok(Ok(slot)) => Ok(slot),
            Ok(Err(error)) => Err(database_error(
                &settings.name,
                &settings.dsn,
                "connect to Postgres",
                &error,
            )),
            Err(_) => Err(AttributeStoreError::Timeout {
                store: settings.name.clone(),
                operation: "connect to Postgres".to_owned(),
                timeout,
            }),
        }
    }
}

impl ConnectionSlot {
    fn spawn<S, T>(
        store: String,
        client: Client,
        connection: tokio_postgres::Connection<S, T>,
    ) -> Self
    where
        S: tokio::io::AsyncRead + tokio::io::AsyncWrite + Unpin + Send + 'static,
        T: tokio::io::AsyncRead + tokio::io::AsyncWrite + Unpin + Send + 'static,
    {
        let connection_task = tokio::spawn(async move {
            if let Err(error) = connection.await {
                tracing::warn!(store, %error, "Attribute Store connection closed");
            }
        });
        Self {
            client,
            connection_task,
        }
    }

    pub(crate) fn is_closed(&self) -> bool {
        self.client.is_closed() || self.connection_task.is_finished()
    }
}

impl Drop for ConnectionSlot {
    fn drop(&mut self) {
        self.connection_task.abort();
    }
}

/// pgx's `configTLS` decision: the tokio-postgres mode and how to verify the certificate, or None for no TLS.
fn resolve_tls(
    parameters: &TlsParameters,
) -> Result<(SslMode, Option<CertificateVerification>), String> {
    let mut ssl_mode = parameters
        .ssl_mode
        .clone()
        .unwrap_or_else(|| "prefer".to_owned());
    if parameters.ssl_negotiation.as_deref() == Some("direct") && ssl_mode == "prefer" {
        ssl_mode = "require".to_owned();
    }
    let roots = match parameters.ssl_root_cert.as_deref() {
        None | Some("") => None,
        Some(ssl_root_cert) => {
            if ssl_root_cert == "system" {
                ssl_mode = "verify-full".to_owned();
            }
            Some(load_root_certificates(ssl_root_cert)?)
        }
    };
    let system_roots = || load_root_certificates("system");
    match ssl_mode.as_str() {
        "disable" => Ok((SslMode::Disable, None)),
        "allow" | "prefer" => Ok((SslMode::Prefer, Some(CertificateVerification::None))),
        "require" => match roots {
            Some(roots) => Ok((
                SslMode::Require,
                Some(CertificateVerification::ChainOnly(roots)),
            )),
            None => Ok((SslMode::Require, Some(CertificateVerification::None))),
        },
        "verify-ca" => {
            let roots = roots.map_or_else(system_roots, Ok)?;
            Ok((
                SslMode::Require,
                Some(CertificateVerification::ChainOnly(roots)),
            ))
        }
        "verify-full" => {
            let roots = roots.map_or_else(system_roots, Ok)?;
            Ok((SslMode::Require, Some(CertificateVerification::Full(roots))))
        }
        other => Err(format!("sslmode {other:?} is invalid")),
    }
}
