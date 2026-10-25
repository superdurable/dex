// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Parses PostgreSQL connection strings and opens TLS or plaintext connections for server-zd storage and search.

mod dsn;
mod tls;

#[cfg(test)]
mod connection_tests;

use std::fmt;
use std::future::Future;
use std::pin::Pin;
use std::time::Duration;

use tokio_postgres::config::SslMode;
use tokio_postgres::{Client, Config, NoTls};
use tokio_postgres_rustls::MakeRustlsConnect;

use dsn::{TlsParameters, split_tls_parameters};
use tls::{CertificateVerification, client_config, load_root_certificates};

/// Applies when the DSN has no `connect_timeout`; it also bounds the TLS handshake and authentication.
const DEFAULT_CONNECT_TIMEOUT: Duration = Duration::from_secs(10);

/// The keywords tokio-postgres names when their value is invalid; errors name only these constants.
const VALUE_CHECKED_KEYWORDS: [&str; 14] = [
    "host",
    "hostaddr",
    "port",
    "sslmode",
    "sslnegotiation",
    "connect_timeout",
    "tcp_user_timeout",
    "keepalives",
    "keepalives_idle",
    "keepalives_interval",
    "keepalives_retries",
    "target_session_attrs",
    "channel_binding",
    "load_balance_hosts",
];

/// Selects the TLS mode when a connection string omits `sslmode`.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum MissingSslMode {
    /// Try TLS first, following the existing Attribute Store behavior.
    Prefer,
    /// Use plaintext unless another TLS parameter is present; then try TLS first.
    Disable,
}

/// A parsed DSN: the tokio-postgres configuration and the TLS connector its `sslmode` requires.
#[derive(Clone)]
pub struct ConnectionSettings {
    config: Config,
    tls: Option<MakeRustlsConnect>,
    /// The DSN as written, kept to redact it from error text; None for [`Self::from_config`].
    dsn: Option<String>,
}

/// Drives one PostgreSQL connection until it closes or its owner drops the task.
pub type ConnectionDriver = Pin<Box<dyn Future<Output = Result<(), tokio_postgres::Error>> + Send>>;

/// Contains a connected client and the future that drives its connection.
pub struct OpenConnection {
    /// Sends PostgreSQL requests over the connection.
    pub client: Client,
    /// Must be polled or spawned while the client is in use.
    pub driver: ConnectionDriver,
}

/// Reports a connection string or TLS configuration error without exposing the DSN.
#[derive(Debug, thiserror::Error)]
pub enum ConnectionSettingsError {
    /// The connection string is malformed or contains an unsupported option.
    #[error("{0}")]
    InvalidDsn(String),
    /// A TLS option or certificate could not be used.
    #[error("{0}")]
    InvalidTls(String),
}

/// Reports a PostgreSQL connection failure or timeout.
#[derive(thiserror::Error)]
pub enum ConnectionError {
    /// The PostgreSQL connection or TLS handshake failed.
    #[error("connect to Postgres failed")]
    Database(#[source] tokio_postgres::Error),
    /// The connection exceeded its configured timeout.
    #[error("connect to Postgres timed out after {0:?}")]
    Timeout(Duration),
}

impl fmt::Debug for ConnectionError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        fmt::Display::fmt(self, formatter)
    }
}

impl ConnectionSettings {
    /// Parses a DSN with pgx's `sslmode` and `sslrootcert` semantics; the error never quotes the DSN.
    pub fn parse(dsn: &str) -> Result<Self, ConnectionSettingsError> {
        Self::parse_with_missing_ssl_mode(dsn, MissingSslMode::Prefer)
    }

    /// Parses a search DSN, defaulting to plaintext and accepting only `sslsni=0` or `sslsni=1`.
    pub fn parse_for_search(dsn: &str) -> Result<Self, ConnectionSettingsError> {
        let (remaining, parameters) =
            split_tls_parameters(dsn).map_err(ConnectionSettingsError::InvalidDsn)?;
        if parameters
            .ssl_sni
            .as_deref()
            .is_some_and(|value| value != "0" && value != "1")
        {
            return Err(ConnectionSettingsError::InvalidTls(
                "sslsni is invalid; use 0 or 1".to_owned(),
            ));
        }
        Self::from_parsed_dsn(dsn, remaining, parameters, MissingSslMode::Disable)
    }

    /// Parses a DSN using the selected mode when `sslmode` is absent.
    fn parse_with_missing_ssl_mode(
        dsn: &str,
        missing_ssl_mode: MissingSslMode,
    ) -> Result<Self, ConnectionSettingsError> {
        let (remaining, parameters) =
            split_tls_parameters(dsn).map_err(ConnectionSettingsError::InvalidDsn)?;
        Self::from_parsed_dsn(dsn, remaining, parameters, missing_ssl_mode)
    }

    fn from_parsed_dsn(
        dsn: &str,
        remaining: String,
        parameters: TlsParameters,
        missing_ssl_mode: MissingSslMode,
    ) -> Result<Self, ConnectionSettingsError> {
        let mut config = remaining.parse::<Config>().map_err(|error| {
            ConnectionSettingsError::InvalidDsn(describe_config_parse_error(&error))
        })?;
        let (ssl_mode, verification) = resolve_tls(&parameters, missing_ssl_mode)
            .map_err(ConnectionSettingsError::InvalidTls)?;
        config.ssl_mode(ssl_mode);
        let tls = match verification {
            None => None,
            Some(verification) => {
                let is_direct = parameters.ssl_negotiation.as_deref() == Some("direct");
                let is_sni_enabled = parameters.ssl_sni.as_deref() != Some("0");
                let client_config = client_config(verification, is_direct, is_sni_enabled)
                    .map_err(ConnectionSettingsError::InvalidTls)?;
                Some(MakeRustlsConnect::new(client_config))
            }
        };
        Ok(Self {
            config,
            tls,
            dsn: Some(dsn.to_owned()),
        })
    }

    /// Wraps a tokio-postgres configuration for plaintext connections.
    pub fn from_config(config: Config) -> Self {
        Self {
            config,
            tls: None,
            dsn: None,
        }
    }

    /// Returns the configuration for changes before opening a connection.
    pub fn configuration_mut(&mut self) -> &mut Config {
        &mut self.config
    }

    /// Returns the configuration used to open connections.
    pub fn configuration(&self) -> &Config {
        &self.config
    }

    /// Returns `message` with every occurrence of the original DSN, then of the configured password,
    /// replaced by `[REDACTED]`. Settings built by [`Self::from_config`] have no DSN, so only the
    /// password is replaced.
    pub fn redact_credentials(&self, message: String) -> String {
        let message = match &self.dsn {
            Some(dsn) => redact_secret(message, dsn),
            None => message,
        };
        match self.config.get_password() {
            Some(password) => redact_secret(message, &String::from_utf8_lossy(password)),
            None => message,
        }
    }

    /// Opens a connection bounded by the DSN's `connect_timeout` or ten seconds.
    pub async fn connect(&self) -> Result<OpenConnection, ConnectionError> {
        let timeout = self
            .config
            .get_connect_timeout()
            .copied()
            .unwrap_or(DEFAULT_CONNECT_TIMEOUT);
        let connecting = async {
            match &self.tls {
                None => {
                    let (client, connection) = self.config.connect(NoTls).await?;
                    Ok(OpenConnection {
                        client,
                        driver: Box::pin(connection),
                    })
                }
                Some(tls) => {
                    let (client, connection) = self.config.connect(tls.clone()).await?;
                    Ok(OpenConnection {
                        client,
                        driver: Box::pin(connection),
                    })
                }
            }
        };
        tokio::time::timeout(timeout, connecting)
            .await
            .map_err(|_| ConnectionError::Timeout(timeout))?
            .map_err(ConnectionError::Database)
    }
}

/// Formats an error followed by its sources, from outermost to innermost.
///
/// Use this for PostgreSQL connection failures so TLS and I/O causes remain visible.
/// The returned text is unredacted. Callers handling errors that may contain a DSN or password
/// must redact the result before displaying it.
/// [`ConnectionSettings::redact_credentials`] removes both.
///
/// # Examples
///
/// ```
/// use dexzd_postgres_connection::error_with_sources;
///
/// let error = std::io::Error::other("connection reset");
/// assert_eq!(error_with_sources(&error), "connection reset");
/// ```
pub fn error_with_sources(error: &(dyn std::error::Error + 'static)) -> String {
    let mut message = error.to_string();
    let mut source = error.source();
    while let Some(cause) = source {
        message.push_str(": ");
        message.push_str(&cause.to_string());
        source = cause.source();
    }
    message
}

/// Go's `redactStorageSecret`: replaces every occurrence of a non-empty secret with `[REDACTED]`.
pub fn redact_secret(message: String, secret: &str) -> String {
    if secret.is_empty() || !message.contains(secret) {
        return message;
    }
    message.replace(secret, "[REDACTED]")
}

/// pgx's `configTLS` decision: the tokio-postgres mode and how to verify the certificate, or None for no TLS.
fn resolve_tls(
    parameters: &TlsParameters,
    missing_ssl_mode: MissingSslMode,
) -> Result<(SslMode, Option<CertificateVerification>), String> {
    let mut ssl_mode = parameters
        .ssl_mode
        .clone()
        .unwrap_or_else(|| match missing_ssl_mode {
            MissingSslMode::Prefer => "prefer".to_owned(),
            MissingSslMode::Disable
                if parameters.ssl_negotiation.is_some()
                    || parameters.ssl_root_cert.is_some()
                    || parameters.ssl_sni.is_some() =>
            {
                "prefer".to_owned()
            }
            MissingSslMode::Disable => "disable".to_owned(),
        });
    if parameters.ssl_negotiation.as_deref() == Some("direct") && ssl_mode == "prefer" {
        ssl_mode = "require".to_owned();
    }
    if parameters.ssl_negotiation.as_deref() == Some("direct") && ssl_mode == "allow" {
        return Err("sslmode=allow is invalid with sslnegotiation=direct".to_owned());
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
        // Unlike pgx, `allow` tries TLS first, like `prefer`.
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
        _ => Err(
            "sslmode is invalid; use disable, allow, prefer, require, verify-ca, or verify-full"
                .to_owned(),
        ),
    }
}

/// Describes a tokio-postgres parse failure with fixed text, because its detail can quote the DSN.
fn describe_config_parse_error(error: &tokio_postgres::Error) -> String {
    let cause = std::error::Error::source(error).map(ToString::to_string);
    VALUE_CHECKED_KEYWORDS
        .iter()
        .find(|keyword| cause.as_deref() == Some(&format!("invalid value for option `{keyword}`")))
        .map_or_else(
            || "connection string has an unsupported keyword or is malformed".to_owned(),
            |keyword| format!("connection string keyword {keyword} has an invalid value"),
        )
}
