// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The dex-server-zd executable: typed configuration, startup, shutdown, the serve mode for external object storage, and the dev mode with an embedded S3 emulator.

mod command_line_options;
mod component_configs;
pub mod config;
mod embedded_s3;
mod metrics_server;
mod object_stores;
mod registry_committed_command_sink;
mod server_logging;
mod server_node;
mod worker_rpc_invoker;

pub use command_line_options::{
    CommandLineOptions, DevCommandOptions, ServeCommandOptions, ServerCommand,
};
pub use server_node::RunningServer;

use std::future::Future;
use std::net::SocketAddr;
use std::path::{Path, PathBuf};
use std::time::Duration;

use clap::Parser;
use dexzd_engine::{EngineStartError, NodeStopReason};
use tokio::sync::watch;

use crate::embedded_s3::EmbeddedS3;
use crate::server_logging::TerminationSignals;
use crate::server_node::LeaseExpiryResponse;

/// Wait for blocking tasks at exit; one stuck in object storage must not keep the process alive.
const RUNTIME_SHUTDOWN_BUDGET: Duration = Duration::from_secs(1);

/// Why the server could not start or stopped abnormally. Every variant exits with a failure status.
#[derive(Debug, thiserror::Error)]
pub enum ServerError {
    /// Configuration could not be loaded or validated.
    #[error(transparent)]
    Config(#[from] config::ConfigError),
    /// A configuration value does not fit the component that uses it.
    #[error("configuration value {field} is out of range for this platform")]
    ConfigValueOutOfRange {
        /// CamelCase YAML field path.
        field: &'static str,
    },
    /// The configured object store credentials cannot be used.
    #[error("{field}: {detail}")]
    UnsupportedCredentials {
        /// CamelCase YAML field path.
        field: &'static str,
        /// Why the credentials cannot be used.
        detail: String,
    },
    /// The S3 client for a bucket could not be created.
    #[error("connecting to object storage bucket {bucket}: {source}")]
    ObjectStore {
        /// Bucket name.
        bucket: String,
        /// Client error.
        #[source]
        source: dexzd_object_store::ObjectStoreError,
    },
    /// The object store does not support the conditional writes the engine relies on.
    #[error("object storage bucket {bucket} failed the startup check: {source}")]
    ObjectStoreSelfCheck {
        /// Bucket name.
        bucket: String,
        /// Failed check.
        #[source]
        source: dexzd_object_store::StartupSelfCheckError,
    },
    /// The embedded S3 emulator of dev or local mode could not start.
    #[error("starting the embedded S3 emulator in {}: {detail}", directory.display())]
    EmbeddedS3 {
        /// Emulator data directory.
        directory: PathBuf,
        /// What failed.
        detail: String,
    },
    /// The Stream Store configuration is invalid.
    #[error("starting the Stream Store: {source}")]
    StreamStore {
        /// Store error.
        #[source]
        source: dexzd_stream_store::StreamStoreError,
    },
    /// The SearchFlows backend could not be created.
    #[error("starting the search backend: {source}")]
    SearchBackend {
        /// Search error.
        #[source]
        source: dexzd_search::SearchError,
    },
    /// An Attribute Store could not be connected or failed its table checks.
    #[error("starting Attribute Store synchronization: {source}")]
    AttributeStore {
        /// Store error, naming the store and table, with the DSN redacted.
        #[source]
        source: dexzd_attribute_store::AttributeStoreError,
    },
    /// The engine could not recover or start serving.
    #[error("starting the engine: {source}")]
    EngineStart {
        /// Engine error.
        #[source]
        source: EngineStartError,
    },
    /// Public API authentication could not be set up, for example because a token file is missing.
    #[error("setting up public API authentication: {source}")]
    Authentication {
        /// Setup error, naming files and URLs but never a credential.
        #[source]
        source: dexzd_api::AuthenticationSetupError,
    },
    /// The gRPC listener could not be bound.
    #[error("listening on {address}: {source}")]
    Listen {
        /// Configured address.
        address: SocketAddr,
        /// Socket error.
        #[source]
        source: std::io::Error,
    },
    /// The gRPC server failed.
    #[error("serving gRPC: {source}")]
    Serve {
        /// Transport error.
        #[source]
        source: tonic::transport::Error,
    },
    /// The node stopped serving, for example after losing its lease; the process must restart.
    #[error("the node stopped: {reason}")]
    NodeStopped {
        /// Why the node stopped.
        reason: NodeStopReason,
    },
    /// Graceful shutdown abandoned steps that did not finish within their budgets, or a step failed.
    ///
    /// The node lease may still be held until it expires when the engine step is listed.
    #[error("shutdown did not finish: {steps}")]
    ShutdownIncomplete {
        /// Comma-separated descriptions of the abandoned or failed steps, such as
        /// `engine exceeded its 5500 ms budget` or `engine failed: releasing node ... lease failed`.
        steps: String,
    },
    /// A server task ended unexpectedly.
    #[error("server task failed: {detail}")]
    ServingTaskFailed {
        /// What failed.
        detail: String,
    },
    /// Logging could not be set up.
    #[error("setting up logging: {detail}")]
    Logging {
        /// What failed.
        detail: String,
    },
    /// The async runtime could not start.
    #[error("starting the async runtime: {source}")]
    AsyncRuntime {
        /// Runtime error.
        #[source]
        source: std::io::Error,
    },
    /// The thread that enforces the lease validity deadline could not start.
    #[error("starting the lease validity watchdog thread: {source}")]
    LeaseWatchdog {
        /// Thread creation error.
        #[source]
        source: std::io::Error,
    },
}

/// Parses the command line, runs the selected command until SIGINT or SIGTERM, and returns how it ended.
pub fn run_from_command_line() -> Result<(), ServerError> {
    let options = CommandLineOptions::parse();
    match options.command {
        ServerCommand::Dev(options) => {
            serve_single_node_until_terminated(&options, config::SingleNodeProfile::Dev)
        }
        ServerCommand::Local(options) => {
            serve_single_node_until_terminated(&options, config::SingleNodeProfile::Local)
        }
        ServerCommand::Serve(options) => {
            let configuration = config::Config::load_file(&options.config)?;
            serve_until_terminated(None, move |mut cancellation| async move {
                server_node::start_server(
                    &configuration,
                    None,
                    &mut cancellation,
                    LeaseExpiryResponse::ExitProcess,
                )
                .await
            })
        }
    }
}

/// Runs `dev` or `local`, selected by `profile`, until SIGINT or SIGTERM.
fn serve_single_node_until_terminated(
    options: &DevCommandOptions,
    profile: config::SingleNodeProfile,
) -> Result<(), ServerError> {
    let configuration = config::Config::load_single_node(options, profile)?;
    let log_directory = configuration.dev.log_directory.clone();
    serve_until_terminated(
        log_directory.as_deref(),
        move |mut cancellation| async move {
            start_single_node_server_with_config(
                configuration,
                &mut cancellation,
                LeaseExpiryResponse::ExitProcess,
            )
            .await
        },
    )
}

/// Installs logging, starts the server, and stops it gracefully on the first SIGINT or SIGTERM.
fn serve_until_terminated<StartFuture>(
    log_directory: Option<&Path>,
    start: impl FnOnce(watch::Receiver<bool>) -> StartFuture,
) -> Result<(), ServerError>
where
    StartFuture: Future<Output = Result<Option<RunningServer>, ServerError>>,
{
    server_logging::install(log_directory)?;
    let runtime = tokio::runtime::Builder::new_multi_thread()
        .enable_all()
        .build()
        .map_err(|source| ServerError::AsyncRuntime { source })?;
    let (result, lease_watchdog) = runtime.block_on(async {
        let mut termination = TerminationSignals::register();
        let (cancellation_sender, cancellation) = watch::channel(false);
        let mut start = std::pin::pin!(start(cancellation));
        let mut server = tokio::select! {
            started = &mut start => match started {
                Ok(Some(server)) => server,
                Ok(None) => return (Ok(()), None),
                Err(error) => return (Err(error), None),
            },
            () = termination.received() => {
                cancellation_sender.send_replace(true);
                match start.await {
                    Ok(Some(server)) => {
                        // Dropping a leased startup delays release until its cleanup task runs.
                        tracing::info!("termination signal during startup; stopping once startup finishes");
                        return (server.shutdown().await, None);
                    }
                    Ok(None) => {
                        tracing::info!("termination signal during startup; startup cancelled");
                        return (Ok(()), None);
                    }
                    Err(error) => return (Err(error), None),
                }
            }
        };
        let lease_watchdog = server.take_lease_watchdog();
        let shutdown = server.shutdown_token();
        let signals = tokio::spawn(async move {
            termination.received().await;
            shutdown.cancel();
        });
        let result = server.wait().await;
        signals.abort();
        (result, lease_watchdog)
    });
    // The watchdog stays armed here, since a blocking task stuck in object storage delays this.
    runtime.shutdown_timeout(RUNTIME_SHUTDOWN_BUDGET);
    if let Some(lease_watchdog) = lease_watchdog {
        lease_watchdog.shutdown();
    }
    result
}

/// Starts the single-node dev server with an embedded S3 emulator, serving on `--listen`.
///
/// The caller installs logging.
pub async fn start_dev_server(options: &DevCommandOptions) -> Result<RunningServer, ServerError> {
    let (_cancellation_sender, mut cancellation) = watch::channel(false);
    let started = start_single_node_server_with_config(
        config::Config::load_dev(options)?,
        &mut cancellation,
        LeaseExpiryResponse::StopNodeOnly,
    )
    .await?;
    Ok(started.expect("dev startup without cancellation must return a server"))
}

async fn start_single_node_server_with_config(
    mut configuration: config::Config,
    cancellation: &mut watch::Receiver<bool>,
    lease_expiry_response: LeaseExpiryResponse,
) -> Result<Option<RunningServer>, ServerError> {
    warn_about_unauthenticated_non_loopback_listen(&configuration);
    let embedded_s3 =
        EmbeddedS3::start(&configuration.dev, &configuration.object_store.bucket).await?;
    configuration.object_store.endpoint = embedded_s3.endpoint();
    server_node::start_server(
        &configuration,
        Some(embedded_s3),
        cancellation,
        lease_expiry_response,
    )
    .await
}

/// Warns at startup when a single-node server accepts unauthenticated calls from other hosts.
fn warn_about_unauthenticated_non_loopback_listen(configuration: &config::Config) {
    if !configuration.api.listen.ip().is_loopback() && configuration.auth.authenticators.is_empty()
    {
        tracing::warn!(
            listen = %configuration.api.listen,
            "public API authentication is off on a non-loopback listen address; anyone who can \
             reach it can call every API, so configure auth.authenticators"
        );
    }
}

#[cfg(test)]
mod tests {
    use std::io;
    use std::net::SocketAddr;
    use std::sync::{Arc, Mutex};

    use super::warn_about_unauthenticated_non_loopback_listen;
    use crate::config::Config;

    const WARNING_TEXT: &str = "public API authentication is off on a non-loopback listen address";

    #[test]
    fn only_a_non_loopback_listen_without_authenticators_logs_the_warning() {
        for (listen, has_warning) in [
            ("0.0.0.0:8801", true),
            ("192.0.2.10:8801", true),
            ("[::]:8801", true),
            ("127.0.0.1:8801", false),
            ("127.1.2.3:8801", false),
            ("[::1]:8801", false),
        ] {
            let mut configuration = Config::default();
            configuration.api.listen = listen.parse::<SocketAddr>().expect("test address");
            let log = startup_warnings(&configuration);
            assert_eq!(log.contains(WARNING_TEXT), has_warning, "{listen}: {log}");
        }
        let mut authenticated = Config::parse_yaml(
            "auth:\n  authenticators:\n    - kind: staticTokens\n      file: /etc/dex/tokens.yaml\n",
        )
        .expect("the auth section parses");
        authenticated.api.listen = "0.0.0.0:8801".parse().expect("test address");
        let log = startup_warnings(&authenticated);
        assert!(!log.contains(WARNING_TEXT), "{log}");
    }

    /// Returns what the startup warning check logs for `configuration`.
    fn startup_warnings(configuration: &Config) -> String {
        let buffer = Arc::new(Mutex::new(Vec::new()));
        let writer_buffer = buffer.clone();
        let subscriber = tracing_subscriber::fmt()
            .with_ansi(false)
            .with_writer(move || LogBuffer(writer_buffer.clone()))
            .finish();
        tracing::subscriber::with_default(subscriber, || {
            warn_about_unauthenticated_non_loopback_listen(configuration);
        });
        let bytes = buffer
            .lock()
            .expect("the log buffer lock is not poisoned")
            .clone();
        String::from_utf8(bytes).expect("logs are UTF-8")
    }

    struct LogBuffer(Arc<Mutex<Vec<u8>>>);

    impl io::Write for LogBuffer {
        fn write(&mut self, bytes: &[u8]) -> io::Result<usize> {
            self.0
                .lock()
                .expect("the log buffer lock is not poisoned")
                .extend_from_slice(bytes);
            Ok(bytes.len())
        }

        fn flush(&mut self) -> io::Result<()> {
            Ok(())
        }
    }
}
