// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::HashMap;
use std::sync::Arc;
use std::sync::atomic::{AtomicU8, Ordering};

use crate::credential::{CredentialChannel, CredentialInterceptor};
use dex_protocol::dex::SyncAttributeIndexRequest;
use dex_protocol::dex::flow_service_client::FlowServiceClient;
use tokio::runtime::Runtime;
use tokio::sync::watch;
use tonic::transport::Endpoint;

use crate::server_protocol::{
    NegotiatedServerProtocolVersion, negotiate_server_protocol, server_info_request_error,
};
use crate::value_hydrator::ValueHydrator;
use crate::worker_dispatcher::WorkerDispatcher;
use crate::worker_pull::PullWorker;
use crate::{BlobCache, Registry, SdkError, SdkResult, WorkerOptions};

const CREATED: u8 = 0;
const RUNNING: u8 = 1;
const STOPPED: u8 = 2;

/// Runs registered Flow Step and RPC handlers through outbound task streams.
///
/// A Worker owns an internal Tokio runtime and is one-shot. [`Self::start`] negotiates the Server
/// protocol, synchronizes Attribute indexes, then blocks while serving. Call [`Self::stop`]
/// from another thread to request graceful shutdown. The Worker runs at most the configured number
/// of handlers concurrently and needs only an outbound connection to Dex. See [`crate::PullWorkerOptions`].
///
/// # Examples
///
/// ```no_run
/// use dex_sdk::{BlobCache, BlobCacheConfig, Registry, Worker, WorkerOptions};
/// use std::sync::Arc;
///
/// let cache = Arc::new(BlobCache::open(BlobCacheConfig::new(
///     "/tmp/dex-blobs", 64 * 1024 * 1024, 0,
/// )?)?);
/// let worker = Worker::try_new(Registry::new(), cache, WorkerOptions::new())?;
/// worker.start()?;
/// # Ok::<(), Box<dyn std::error::Error>>(())
/// ```
pub struct Worker {
    runtime: Runtime,
    flow_service: FlowServiceClient<CredentialChannel>,
    attribute_indexes: HashMap<String, i32>,
    attribute_index_sync_timeout: std::time::Duration,
    pull: PullWorker,
    shutdown: watch::Sender<bool>,
    state: AtomicU8,
    negotiated_protocol_version: NegotiatedServerProtocolVersion,
}

impl Worker {
    /// Creates a Worker or panics when options or runtime initialization are invalid.
    ///
    /// # Panics
    ///
    /// Panics for invalid pull options, zero index-sync timeout, invalid Dex endpoint, or Tokio
    /// runtime creation failure. Prefer [`Self::try_new`] for recoverable startup.
    pub fn new(registry: Registry, blob_cache: Arc<BlobCache>, options: WorkerOptions) -> Self {
        Self::try_new(registry, blob_cache, options)
            .unwrap_or_else(|error| panic!("cannot create Rust Worker: {error}"))
    }

    /// Creates a one-shot pull Worker with a lazy outbound connection.
    ///
    /// # Errors
    ///
    /// Returns [`SdkError::FlowDefinition`] for invalid Worker options, or Service for runtime
    /// and endpoint initialization failures. The Dex
    /// connection is lazy.
    pub fn try_new(
        registry: Registry,
        blob_cache: Arc<BlobCache>,
        options: WorkerOptions,
    ) -> SdkResult<Self> {
        let runtime = Runtime::new().map_err(service_error)?;
        if options.attribute_index_sync_timeout_value().is_zero() {
            return Err(SdkError::FlowDefinition {
                message: "attribute index sync timeout must be positive".to_string(),
            });
        }
        let endpoint = Endpoint::from_shared(endpoint_address(options.server_address_value()))
            .map_err(service_error)?;
        let flow_service = {
            let _runtime_guard = runtime.enter();
            FlowServiceClient::new(CredentialChannel::new(
                endpoint.connect_lazy(),
                CredentialInterceptor::new(options.credential_value()),
            ))
        };
        let pull_channel = {
            let _runtime_guard = runtime.enter();
            crate::worker_pull::outbound_task_channel(endpoint.clone(), options.credential_value())
        };
        let attribute_indexes = registry.attribute_indexes().clone();
        let hydrator = ValueHydrator::new(flow_service.clone(), blob_cache);
        let negotiated_protocol_version = NegotiatedServerProtocolVersion::default();
        let dispatcher =
            WorkerDispatcher::new(registry, hydrator, negotiated_protocol_version.clone());
        let pull = PullWorker::new(options.pull_value(), &dispatcher, pull_channel)?;
        let (shutdown, _) = watch::channel(false);
        Ok(Self {
            runtime,
            flow_service,
            attribute_indexes,
            attribute_index_sync_timeout: options.attribute_index_sync_timeout_value(),
            pull,
            shutdown,
            state: AtomicU8::new(CREATED),
            negotiated_protocol_version,
        })
    }

    /// Negotiates protocol, synchronizes Attribute indexes, and serves until stopped or failed.
    ///
    /// Stream errors are logged and retried. After [`Self::stop`], this method drains
    /// active handlers for the configured timeout, cancels any remaining handlers, and waits for
    /// them to return. Call this method only once, on a dedicated thread.
    ///
    /// # Errors
    ///
    /// Returns FlowDefinition if called more than once, or Service for index synchronization,
    /// timeout, and server failures.
    /// Returns a service error exposing [`SdkError::pull_registration_error`] after draining when permanent-rejection shutdown is enabled.
    pub fn start(&self) -> SdkResult<()> {
        let result = self.start_worker();
        self.pull
            .registration
            .finish(result.as_ref().err().cloned());
        result
    }

    fn start_worker(&self) -> SdkResult<()> {
        self.state
            .compare_exchange(CREATED, RUNNING, Ordering::AcqRel, Ordering::Acquire)
            .map_err(|_| SdkError::FlowDefinition {
                message: "Worker can only be started once".to_string(),
            })?;
        let shutdown = self.shutdown.subscribe();
        let mut flow_service = self.flow_service.clone();
        let server_info_result = self.runtime.block_on(async {
            tokio::time::timeout(
                self.attribute_index_sync_timeout,
                flow_service.get_server_info(()),
            )
            .await
        });
        let server_info = match server_info_result {
            Ok(Ok(response)) => response.into_inner(),
            Ok(Err(status)) => {
                self.state.store(STOPPED, Ordering::Release);
                return Err(service_error(server_info_request_error(status)));
            }
            Err(elapsed) => {
                self.state.store(STOPPED, Ordering::Release);
                return Err(service_error(server_info_request_error(elapsed)));
            }
        };
        let negotiated_protocol_version =
            negotiate_server_protocol(&server_info).map_err(|error| {
                self.state.store(STOPPED, Ordering::Release);
                service_error(error)
            })?;
        self.negotiated_protocol_version
            .store(negotiated_protocol_version);
        let sync_result = self.runtime.block_on(async {
            tokio::time::timeout(
                self.attribute_index_sync_timeout,
                flow_service.sync_attribute_indexes(SyncAttributeIndexRequest {
                    attribute_indexes: self.attribute_indexes.clone(),
                }),
            )
            .await
        });
        match sync_result {
            Ok(Ok(_)) => {}
            Ok(Err(status)) => {
                self.state.store(STOPPED, Ordering::Release);
                return Err(service_error(status));
            }
            Err(elapsed) => {
                self.state.store(STOPPED, Ordering::Release);
                return Err(service_error(elapsed));
            }
        }
        let result = self.runtime.block_on(self.pull.run(shutdown));
        self.state.store(STOPPED, Ordering::Release);
        result
    }

    /// Waits until every Flow type queue and the app Worker ID queue has registered once.
    ///
    /// Safe before or after [`Self::start`] and for concurrent callers. Start the Worker on a
    /// dedicated thread. Drop this future to cancel the wait, or use `tokio::time::timeout`.
    /// Completion remains latched after shutdown; use [`Self::pull_registration_state`] for probes.
    ///
    /// # Errors
    ///
    /// Returns a service error exposing [`SdkError::pull_registration_error`] for a permanent rejection without another registered
    /// stream before readiness. Returns the startup error if startup fails, or InvalidArgument
    /// for shutdown before readiness.
    ///
    /// ```no_run
    /// # async fn ready(worker: &dex_sdk::Worker) -> Result<(), Box<dyn std::error::Error>> {
    /// tokio::time::timeout(std::time::Duration::from_secs(10), worker.wait_pull_registered()).await??;
    /// assert!(worker.pull_registration_state().is_ready);
    /// # Ok(()) }
    /// ```
    pub async fn wait_pull_registered(&self) -> SdkResult<()> {
        self.pull.registration.wait().await
    }

    /// Returns cumulative Step declines caused by reserved RPC capacity for this Worker.
    ///
    /// The counter counts decline events. Dex may decline the same Step dispatch repeatedly
    /// while retrying it, so read the counter as a rate. Full-capacity rejections and unknown names are excluded. The count never resets and
    /// remains available after stop. Concurrent reads are safe.
    /// Export the count to a monitoring counter to identify reserve pressure.
    ///
    /// ```no_run
    /// # fn inspect(worker: &dex_sdk::Worker) {
    /// let declines: u64 = worker.pull_reserved_rpc_declines();
    /// # }
    /// ```
    pub fn pull_reserved_rpc_declines(&self) -> u64 {
        self.pull.reserved_rpc_declines()
    }

    /// Returns an independent snapshot of current pull queue registrations.
    ///
    /// Readiness latches after every queue registers once. Only a permanent rejection with no
    /// registered stream makes it false. Successful registration clears that queue's error.
    /// Transient disconnects and Stop do not clear readiness. See [`Self::wait_pull_registered`] for a startup and probe example.
    pub fn pull_registration_state(&self) -> crate::PullRegistrationState {
        self.pull.registration.snapshot()
    }

    /// Requests shutdown of a running Worker and returns immediately.
    ///
    /// Calling `stop` before or after `start` is safe; [`Self::start`] observes the shutdown signal.
    /// The Worker first drains active handlers. After the drain timeout it cancels them, and
    /// [`Self::start`] waits for their return. Half-closed pull streams wait for server EOF
    /// until the remaining drain deadline. Stop is idempotent.
    pub fn stop(&self) {
        self.pull.registration.finish(None);
        self.shutdown.send_replace(true);
    }
}

fn endpoint_address(address: &str) -> String {
    if address.contains("://") {
        address.to_string()
    } else {
        format!("http://{address}")
    }
}

fn service_error(error: impl std::fmt::Display) -> SdkError {
    SdkError::Service {
        service: crate::ServiceError::local("worker", error.to_string()),
    }
}
