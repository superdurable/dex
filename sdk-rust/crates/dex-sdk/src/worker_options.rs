// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::Arc;
use std::time::Duration;

use crate::Credential;

/// Configures outbound polling for a Worker.
///
/// Pass this to [`WorkerOptions::pull`]. The Worker polls its sticky queue and each registered
/// Flow type, runs at most `max_concurrent_tasks` handlers, and keeps `idle_streams_per_queue`
/// waiting streams on each queue. An omitted task limit uses the CPU count; an omitted idle
/// stream count uses one. An omitted drain timeout uses 30 seconds.
///
/// ```no_run
/// use std::sync::Arc;
/// use dex_sdk::{BlobCache, BlobCacheConfig, Client, ClientOptions, PullWorkerOptions,
///     Registry, Worker, WorkerOptions};
///
/// let cache = Arc::new(BlobCache::open(BlobCacheConfig::new(
///     "/tmp/dex-pull-blobs", 64 * 1024 * 1024, 0,
/// )?)?);
/// let registry = Registry::new();
/// let worker = Worker::try_new(registry.clone(), cache.clone(),
///     WorkerOptions::new().pull(PullWorkerOptions::new().max_concurrent_tasks(8)))?;
/// let client = Client::try_new(registry, cache,
///     ClientOptions::new())?;
/// // Run worker.start() on a dedicated thread; call worker.stop() during shutdown.
/// # Ok::<(), Box<dyn std::error::Error>>(())
/// ```
#[derive(Clone, Debug, Default)]
pub struct PullWorkerOptions {
    pub(crate) max_concurrent_tasks: Option<usize>,
    pub(crate) reserved_rpc_tasks: Option<usize>,
    pub(crate) idle_streams_per_queue: Option<usize>,
    pub(crate) reserve_pressure_linger: Option<Duration>,
    pub(crate) app_worker_id: Option<String>,
    pub(crate) stop_drain_timeout: Option<Duration>,
    pub(crate) stop_on_permanent_stream_rejection: bool,
}

impl PullWorkerOptions {
    /// Creates pull options with CPU-count concurrency, one idle stream per queue, and a 30-second drain.
    ///
    /// The app Worker ID is generated from process identity and eight random base32 characters.
    pub fn new() -> Self {
        Self::default()
    }

    /// Sets the maximum number of concurrent Step and RPC handlers.
    ///
    /// The default is the available CPU count. Zero returns [`crate::SdkError::FlowDefinition`]
    /// from [`crate::Worker::try_new`]. Additional assignments are rejected at capacity.
    pub fn max_concurrent_tasks(mut self, value: usize) -> Self {
        self.max_concurrent_tasks = Some(value);
        self
    }

    /// Reserves capacity to protect synchronous RPC latency from long-running Steps.
    ///
    /// Unset reserves one slot when resolved capacity is at least two, otherwise zero.
    /// With the CPU-count default, Steps get one fewer slot than before. Set zero to opt out.
    /// With one slot and no explicit reserve, start warns once that RPCs may wait behind Steps.
    /// Flows must stay responsive because RPCs share Worker task capacity with Steps.
    /// WaitFor and Execute tasks, including local attempts, are declined when
    /// total busy handlers reach the resolved task limit minus this reserve. RPCs use the full limit.
    /// API-direct and transactional RPCs both use the full limit. Declines reassign without consuming an attempt.
    /// If all Workers remain at the Step limit through the dispatch wait, NO_WORKER_AVAILABLE
    /// consumes an attempt and follows the Step retry policy. Local attempts wait the local dispatch
    /// wait, then fall back according to their policy. Readiness is unaffected.
    /// Declines emit a tracing debug event and increment [`crate::Worker::pull_reserved_rpc_declines`].
    /// A queue keeps one extra idle stream for 30 seconds after its last reserve decline.
    /// The Worker does not open it while all task slots are busy.
    ///
    /// `value` must be less than the task limit after its CPU-count default is resolved.
    /// [`crate::Worker::try_new`] returns [`crate::SdkError::FlowDefinition`] otherwise.
    /// Returns these options with the reserve applied.
    ///
    /// ```
    /// use dex_sdk::PullWorkerOptions;
    /// let pull = PullWorkerOptions::new().max_concurrent_tasks(8).reserved_rpc_tasks(2);
    /// ```
    pub fn reserved_rpc_tasks(mut self, value: usize) -> Self {
        self.reserved_rpc_tasks = Some(value);
        self
    }

    /// Sets the number of waiting streams per queue.
    ///
    /// The default is one. Zero returns [`crate::SdkError::FlowDefinition`] from
    /// [`crate::Worker::try_new`]. The Worker also retains busy streams until their tasks finish.
    /// A queue keeps one additional idle stream after a recent reserve decline. Use at least two for heavy Step backlogs.
    pub fn idle_streams_per_queue(mut self, value: usize) -> Self {
        self.idle_streams_per_queue = Some(value);
        self
    }

    /// Overrides the process Worker ID used for sticky routing.
    ///
    /// Use a new ID after each process restart. It must contain 1–63 ASCII letters, digits,
    /// dots, underscores, or hyphens and must differ from every registered Flow type.
    /// Invalid values return [`crate::SdkError::FlowDefinition`] from [`crate::Worker::try_new`].
    /// If omitted, the Worker generates a fresh ID during construction.
    pub fn app_worker_id(mut self, value: impl Into<String>) -> Self {
        self.app_worker_id = Some(value.into());
        self
    }

    /// Stops when a permanent rejection leaves a queue without any registered stream.
    ///
    /// Defaults to false, retrying indefinitely. When enabled, [`crate::Worker::start`] drains
    /// within the configured timeout, cancels remaining handlers, and waits for their return.
    /// It returns a service error exposing [`crate::SdkError::pull_registration_error`] before or after initial readiness.
    /// Calling [`crate::Worker::stop`] afterward remains safe.
    pub fn stop_on_permanent_stream_rejection(mut self, value: bool) -> Self {
        self.stop_on_permanent_stream_rejection = value;
        self
    }

    /// Bounds graceful drain before active handlers are cancelled.
    ///
    /// The default is 30 seconds. Zero returns [`crate::SdkError::FlowDefinition`] from
    /// [`crate::Worker::try_new`].
    /// A busy server-directed Reconnect uses this duration to wait for server EOF after the final frame.
    /// During Stop, half-closed streams use the remaining drain deadline.
    /// [`crate::Worker::start`] waits for cancelled handlers to return before it exits.
    pub fn stop_drain_timeout(mut self, value: Duration) -> Self {
        self.stop_drain_timeout = Some(value);
        self
    }
}

#[derive(Clone, Debug)]
/// Configures a pull [`crate::Worker`] and its outbound Dex connection.
///
/// Defaults connect to Dex at `127.0.0.1:8801`, use [`PullWorkerOptions::default`], and allow
/// two minutes for Attribute-index synchronization. Workers need only an outbound connection.
pub struct WorkerOptions {
    server_address: String,
    pull: PullWorkerOptions,
    attribute_index_sync_timeout: Duration,
    credential: Option<Arc<dyn Credential>>,
}

impl WorkerOptions {
    /// Creates Worker options with local development defaults.
    pub fn new() -> Self {
        Self {
            server_address: "127.0.0.1:8801".to_string(),
            pull: PullWorkerOptions::default(),
            attribute_index_sync_timeout: Duration::from_secs(120),
            credential: None,
        }
    }

    /// Sets the plaintext Dex FlowService `host:port` used by the Worker.
    pub fn server_address(mut self, value: impl Into<String>) -> Self {
        self.server_address = value.into();
        self
    }

    /// Sends a bearer token on FlowService calls and pull Worker task streams.
    ///
    /// The default sends no authorization metadata. The credential may be called concurrently;
    /// errors fail calls locally. Pull streams evaluate it at opening and again on reconnect.
    /// The token uses Dex's plaintext connection, so protect the network path.
    pub fn credential(mut self, value: Arc<dyn Credential>) -> Self {
        self.credential = Some(value);
        self
    }

    /// Configures pull capacity, queue registration, and graceful shutdown.
    ///
    /// Every Worker uses pull; this builder overrides [`PullWorkerOptions::default`].
    /// Takes ownership of `value` and returns these Worker options with its settings applied.
    /// [`crate::Worker::try_new`] validates capacity, identity, and timeout settings.
    /// See [`PullWorkerOptions`] for defaults and a complete Worker and Client example.
    pub fn pull(mut self, value: PullWorkerOptions) -> Self {
        self.pull = value;
        self
    }

    /// Sets the startup Indexed Attribute synchronization deadline.
    ///
    /// The default is two minutes. [`Worker::try_new`](crate::Worker::try_new)
    /// rejects [`Duration::ZERO`].
    pub fn attribute_index_sync_timeout(mut self, value: Duration) -> Self {
        self.attribute_index_sync_timeout = value;
        self
    }

    pub(crate) fn pull_value(&self) -> &PullWorkerOptions {
        &self.pull
    }

    pub(crate) fn server_address_value(&self) -> &str {
        &self.server_address
    }

    pub(crate) fn attribute_index_sync_timeout_value(&self) -> Duration {
        self.attribute_index_sync_timeout
    }

    pub(crate) fn credential_value(&self) -> Option<Arc<dyn Credential>> {
        self.credential.clone()
    }
}

impl Default for WorkerOptions {
    fn default() -> Self {
        Self::new()
    }
}

pub(crate) struct WorkerEnvironmentConfig {
    pub(crate) identity: String,
}

impl WorkerEnvironmentConfig {
    pub(crate) fn from_environment() -> Self {
        let identity = ["DEX_WORKER_INSTANCE", "POD_NAME", "HOSTNAME"]
            .into_iter()
            .filter_map(std::env::var_os)
            .map(|value| value.to_string_lossy().into_owned())
            .find(|value| !value.trim().is_empty())
            .unwrap_or_else(|| gethostname::gethostname().to_string_lossy().into_owned());
        Self { identity }
    }
}
