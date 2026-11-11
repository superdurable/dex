// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::path::PathBuf;
use std::sync::Arc;
use std::time::Duration;

use crate::{Credential, FlowDefinitionDocument, FlowServiceTlsOptions};

/// Configures outbound polling for a Worker.
///
/// Pass this to [`WorkerOptions::pull`]. The Worker polls its sticky queue and each registered
/// Flow type through exactly `max_concurrent_tasks` long-lived slot streams. An omitted task limit
/// uses the CPU count. An omitted drain timeout uses 30 seconds.
///
/// Changed in 1.35.0: each execution slot owns one stream, replacing per-queue idle streams.
/// Start requires protocol 4 (server-zd newer than v0.1.16); upgrade the Server before the SDK.
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
    pub(crate) app_worker_id: Option<String>,
    pub(crate) stop_drain_timeout: Option<Duration>,
    pub(crate) stop_on_permanent_stream_rejection: bool,
}

impl PullWorkerOptions {
    /// Creates pull options with CPU-count slot concurrency and a 30-second drain.
    ///
    /// The app Worker ID is generated from process identity and eight random base32 characters.
    pub fn new() -> Self {
        Self::default()
    }

    /// Sets the maximum number of concurrent Step and RPC handlers.
    ///
    /// The default is the available CPU count. Zero returns [`crate::SdkError::FlowDefinition`]
    /// from [`crate::Worker::try_new`]. Each slot owns one long-lived poll stream and executes
    /// every task assigned to it. Changed in 1.35.0: [`crate::Worker::start`] fails if this value
    /// exceeds the Server's reported per-Worker limit, configured by `matching.maxConcurrentTasksCap`.
    pub fn max_concurrent_tasks(mut self, value: usize) -> Self {
        self.max_concurrent_tasks = Some(value);
        self
    }

    /// Reserves slots that accept only RPCs, protecting RPC latency from long-running Steps.
    ///
    /// Changed in 1.23.0: reserved RPC capacity remains available under a Step backlog.
    /// Changed in 1.35.0: reservations select RPC-only streams instead of declining
    /// assigned Steps; the Worker executes every assignment.
    ///
    /// The default reserves one slot when resolved concurrency is at least two, otherwise zero.
    /// The remaining slots accept Steps and RPCs. The Server selects eligible slots; the Worker
    /// never declines a Step for capacity. Set zero to allow Steps on every slot. A one-slot
    /// Worker with no explicit reservation warns that RPCs may wait behind Steps.
    /// Readiness requires a registered Step-and-RPC slot and, with a reservation, an RPC-only slot.
    /// `value` must be less than resolved `max_concurrent_tasks`; otherwise
    /// [`crate::Worker::try_new`] returns [`crate::SdkError::FlowDefinition`].
    /// Returns these options with the reservation applied.
    ///
    /// ```
    /// use dex_sdk::PullWorkerOptions;
    /// let pull = PullWorkerOptions::new().max_concurrent_tasks(8).reserved_rpc_tasks(2);
    /// ```
    pub fn reserved_rpc_tasks(mut self, value: usize) -> Self {
        self.reserved_rpc_tasks = Some(value);
        self
    }

    /// Retains the former per-queue idle-stream setting for source compatibility.
    ///
    /// Changed in 1.35.0: this setting became a compatibility-only no-op.
    ///
    /// Deprecated: `value` is accepted, including zero, and has no effect. Every execution slot
    /// now owns exactly one long-lived stream. Use [`Self::max_concurrent_tasks`] to select the
    /// total stream count. Returns these options with the compatibility value retained.
    #[deprecated(note = "one long-lived stream per execution slot; this setting has no effect")]
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

    /// Stops when a permanent rejection leaves a required slot kind without any registered stream.
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
    /// During Stop, idle streams half-close immediately. Busy streams finish and send their final
    /// output before half-closing. Later task frames are dropped without invoking handlers, and
    /// half-closed streams keep reading until the Server ends them or the drain deadline expires.
    /// At the deadline, a stream with a running handler is reset instead of half-closed.
    /// [`crate::Worker::start`] waits for cancelled handlers to return before it exits.
    /// Every other stream still waiting for the Server to end it is reset at the deadline.
    /// A stream whose last final output Dex has not yet acknowledged with a new task waits up to
    /// five seconds past this deadline for the Server to end it, then is reset.
    /// Stop can therefore return up to five seconds after this deadline.
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
/// Changed in 1.25.0: every Worker uses pull mode; no listener or Worker target is configured.
pub struct WorkerOptions {
    server_address: String,
    pull: PullWorkerOptions,
    attribute_index_sync_timeout: Duration,
    credential: Option<Arc<dyn Credential>>,
    flow_service_tls: Option<FlowServiceTlsOptions>,
    flow_definition_paths: Vec<PathBuf>,
    flow_definitions: Vec<FlowDefinitionDocument>,
}

impl WorkerOptions {
    /// Creates Worker options with local development defaults.
    pub fn new() -> Self {
        Self {
            server_address: "127.0.0.1:8801".to_string(),
            pull: PullWorkerOptions::default(),
            attribute_index_sync_timeout: Duration::from_secs(120),
            credential: None,
            flow_service_tls: None,
            flow_definition_paths: Vec::new(),
            flow_definitions: Vec::new(),
        }
    }

    /// Sets the Flow Definition Graph files that the Worker uploads to the Dex Server at start.
    ///
    /// Dex Web draws each Flow type's graph from the uploaded document. Generate the files with
    /// `dexcli visualize`. Each path must exist and be readable. An explicitly listed file must be
    /// a Flow Definition Graph 2.x: a UTF-8 JSON object whose `schemaVersion` starts with `2.` and
    /// whose `flow.name` is a non-empty string; its extension is not checked. A directory is read
    /// recursively for files whose extension is `.json` in any case; a file there that is not a
    /// Flow Definition Graph 2.x is skipped with an info log, so a directory may hold other JSON
    /// files.
    ///
    /// The Flow type of a file is its `flow.name`. Its generation time is its top-level
    /// `generatedAt`, which must be strict RFC 3339 such as `2026-11-01T00:00:00Z`, or the file
    /// modification time when that is missing. Every generation time is truncated to whole
    /// milliseconds.
    ///
    /// [`crate::Worker::try_new`] reads the files and returns [`crate::SdkError::FlowDefinition`]
    /// for a missing or unreadable path, an invalid explicit file, an invalid `generatedAt`, or a
    /// Flow type configured twice. [`crate::Worker::start`] uploads only documents for Flow types
    /// that the Registry registers, after protocol negotiation and Attribute-index synchronization
    /// and before it opens task streams, and sends nothing when none is left. An absolute
    /// top-level `source.path`, including a Windows drive or UNC path, is replaced by a path
    /// relative to the working directory, or by the file name when the file is elsewhere or the
    /// working directory is unknown. Requests hold at most 256 documents and 16 MiB of content and
    /// share one [`Self::attribute_index_sync_timeout`] deadline. [`crate::Worker::stop`] cancels
    /// an upload in progress. An upload failure, including an older Server without upload
    /// support, is logged as a warning; the Worker still starts. The default is no paths. Each
    /// call replaces the previous paths.
    ///
    /// Since 1.45.0.
    ///
    /// ```
    /// use dex_sdk::WorkerOptions;
    /// let options = WorkerOptions::new()
    ///     .server_address("dex:8801")
    ///     .flow_definition_paths(["flow-definitions"]);
    /// ```
    pub fn flow_definition_paths<P: Into<PathBuf>>(
        mut self,
        value: impl IntoIterator<Item = P>,
    ) -> Self {
        self.flow_definition_paths = value.into_iter().map(Into::into).collect();
        self
    }

    /// Sets in-memory Flow Definition Graph documents that the Worker uploads at start.
    ///
    /// Use this when the application builds documents itself, for example from a template with
    /// a fixed generation time. Uploading, filtering to registered Flow types, the `source.path`
    /// rewrite, and failure handling work as for [`Self::flow_definition_paths`]. A Flow type may
    /// come from only one document across both settings. The default is no documents. Each call
    /// replaces the previous documents. [`crate::Worker::try_new`] returns
    /// [`crate::SdkError::FlowDefinition`] when a document is not a UTF-8 JSON object whose
    /// `schemaVersion` starts with `2.`, its `flow.name` differs from its Flow type, or two sources share a Flow type. See [`FlowDefinitionDocument`] for an
    /// example.
    ///
    /// Since 1.45.0.
    pub fn flow_definitions(
        mut self,
        value: impl IntoIterator<Item = FlowDefinitionDocument>,
    ) -> Self {
        self.flow_definitions = value.into_iter().collect();
        self
    }

    pub(crate) fn take_flow_definition_sources(
        &mut self,
    ) -> (Vec<PathBuf>, Vec<FlowDefinitionDocument>) {
        (
            std::mem::take(&mut self.flow_definition_paths),
            std::mem::take(&mut self.flow_definitions),
        )
    }

    /// Sets the Dex FlowService address used by the Worker.
    ///
    /// With TLS, accepts `host:port`, `dns:///host:port`, or `dns://authority/host:port`.
    pub fn server_address(mut self, value: impl Into<String>) -> Self {
        self.server_address = value.into();
        self
    }

    /// Enables TLS for every outbound Worker FlowService connection.
    ///
    /// Omitted by default, preserving plaintext gRPC. Takes ownership of `value` and returns
    /// these options with TLS enabled. [`crate::Worker::try_new`] validates the address,
    /// additional roots, and server name, returning [`crate::SdkError::InvalidArgument`] for
    /// invalid input. See [`FlowServiceTlsOptions`] for defaults and a complete example.
    pub fn flow_service_tls(mut self, value: FlowServiceTlsOptions) -> Self {
        self.flow_service_tls = Some(value);
        self
    }

    /// Sends a bearer token on FlowService calls and pull Worker task streams.
    ///
    /// The default sends no authorization metadata. The credential may be called concurrently;
    /// errors fail calls locally. Pull streams evaluate it at opening and again on reconnect.
    /// Enable [`Self::flow_service_tls`] to protect the token on the network.
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

    pub(crate) fn flow_service_tls_value(&self) -> Option<&FlowServiceTlsOptions> {
        self.flow_service_tls.as_ref()
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
