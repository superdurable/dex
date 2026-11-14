// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::any::TypeId;
use std::collections::{BTreeMap, BTreeSet};
use std::sync::Arc;
use std::time::{Duration, SystemTime};

use dex_protocol::dex::flow_service_client::FlowServiceClient;
use dex_protocol::dex::{
    ActiveStepSearchMode as ProtoSearchMode, AttributeStoreNames, FlowAlreadyStartedOptions,
    FlowConfig as ProtoFlowConfig, FlowResetStepMethod, FlowResetType, FlowRetryPolicy,
    FlowStartOptions, FlowStatus as ProtoFlowStatus, FlowTimeoutPolicy as ProtoFlowTimeoutPolicy,
    GetFlowSummaryRequest, IdReusePolicy as ProtoIdReusePolicy, InvokeRpcRequest,
    ListStreamMessagesRequest, ReadStreamRequest, ResetFlowRequest, SearchFlowsRequest,
    SkipTimerRequest, StartFlowRequest, StepDurability as ProtoStepDurability, StopFlowRequest,
    StopType as ProtoStopType, UpdateFlowConfigRequest, WaitForAttributeRequest,
    WaitForFlowRequest, WaitForStepCompletionRequest, WorkerTarget as ProtoWorkerTarget,
    WriteStreamRequest,
};
use tokio::runtime::Runtime;
use tonic::{Code, Request};
use uuid::Uuid;

mod attribute_reads;
mod flow_definitions;
pub(crate) mod flow_service_tls;
pub(crate) mod task_queue_name;

pub use flow_service_tls::FlowServiceTlsOptions;
use flow_service_tls::flow_service_endpoint;

pub use attribute_reads::{AttributeRead, AttributeReads};

use crate::credential::{CredentialChannel, CredentialInterceptor};
use crate::sdk_error::{FlowTargetRequirement, ServiceError};
use crate::server_protocol::{NegotiatedServerProtocolVersion, negotiate_server_protocol};
use crate::stop_flow_options::StopType;
use crate::time_travel_options::{TimeTravelPoint, TimeTravelStepMethod};
use crate::value_hydrator::ValueHydrator;
use crate::value_mapper;
use crate::wait_options::ClientRequestBudget;
use crate::worker_dispatcher::{map_flow_timeout_handler_options, map_step_options};
use crate::{
    ActiveStepSearchMode, Attribute, AttributeMap, AttributeMatch, BlobCache, ClientOptions, Flow,
    FlowConfig, FlowErrorType, FlowInfo, FlowResult, FlowStatus, FlowTimeoutPolicy, IdReusePolicy,
    Registry, RetryPolicy, Rpc, RpcInvokeOptions, SdkError, SdkResult, SearchFlowEntry,
    SearchFlowsPage, StartFlowOptions, StepCompletion, StepDurability, StepExecutionId,
    StopFlowOptions, Stream, StreamMessage, StreamMessagesPage, TimeTravelOptions, TimerId, Value,
    WaitForAttributeOptions, WaitForStepCompletionOptions,
};

/// Provides blocking, typed control of registered Dex Flows.
///
/// Client methods block the calling thread while an internal Tokio runtime performs gRPC and blob
/// hydration. The Client owns its transport resources; dropping it releases them. Flow, Step, RPC,
/// Attribute, and Channel arguments are checked against the [`Registry`].
/// Construction performs no network I/O. Before its first service request, every Client negotiates
/// protocol version 4. Successful negotiation and incompatibility errors are cached for its lifetime;
/// transient negotiation failures are retried on the next call. Incompatibility errors name the
/// Server version and required server-zd release. Concurrent first calls share one negotiation.
/// An unreachable Server during negotiation returns the usual service error with its gRPC code.
/// A positive request timeout on a wait method also bounds negotiation and the wait for another
/// caller's negotiation; its expiry returns [`SdkError::RequestTimeout`] and is not cached.
///
/// Changed in 1.35.0: protocol 4 is required (server-zd newer than v0.1.16).
/// Upgrade the Server first.
///
/// Changed in 1.26.0: only server-zd is supported as the Dex Server.
///
/// A Client call never retries a failed application RPC automatically.
/// Inspect [`SdkError`] variants and [`crate::ServiceError`] status details before retrying an
/// operation whose external effects may already have happened.
///
/// # Examples
///
/// ```no_run
/// use dex_sdk::{BlobCache, BlobCacheConfig, Client, ClientOptions, Registry};
/// use std::sync::Arc;
///
/// let cache = Arc::new(BlobCache::open(BlobCacheConfig::new(
///     "/tmp/dex-blobs", 64 * 1024 * 1024, 0,
/// )?)?);
/// let client = Client::try_new(Registry::new(), cache, ClientOptions::new())?;
/// client.health_check()?;
/// # Ok::<(), Box<dyn std::error::Error>>(())
/// ```
pub struct Client {
    runtime: Runtime,
    registry: Registry,
    service: FlowServiceClient<CredentialChannel>,
    hydrator: ValueHydrator,
    negotiated_protocol_version: NegotiatedServerProtocolVersion,
    protocol_negotiation: tokio::sync::Mutex<Option<Result<u32, String>>>,
}

impl Client {
    /// Creates a Client or panics when runtime or endpoint initialization fails.
    ///
    /// Prefer [`Self::try_new`] when the application can recover from startup configuration errors.
    ///
    /// # Panics
    ///
    /// Panics if a Tokio runtime cannot be created or the server address or TLS settings are invalid.
    pub fn new(registry: Registry, blob_cache: Arc<BlobCache>, options: ClientOptions) -> Self {
        Self::try_new(registry, blob_cache, options)
            .unwrap_or_else(|error| panic!("cannot create Dex Client: {error}"))
    }

    /// Creates a Client from validated definitions, a shared BlobCache, and connection options.
    ///
    /// # Errors
    ///
    /// Returns [`SdkError::Service`] if runtime or endpoint initialization fails. The transport connects lazily,
    /// so server availability is checked by the first operation. Invalid TLS options return
    /// [`SdkError::InvalidArgument`]. Certificate and handshake failures occur on the first call.
    pub fn try_new(
        registry: Registry,
        blob_cache: Arc<BlobCache>,
        options: ClientOptions,
    ) -> SdkResult<Self> {
        let runtime = Runtime::new().map_err(service_error)?;
        let endpoint = flow_service_endpoint(
            options.server_address_value(),
            options.flow_service_tls_value(),
            "client",
        )?;
        let service = {
            let _runtime_guard = runtime.enter();
            FlowServiceClient::new(CredentialChannel::new(
                endpoint.connect_lazy(),
                CredentialInterceptor::new(options.credential_value()),
            ))
        };
        let hydrator = ValueHydrator::new(service.clone(), blob_cache);
        Ok(Self {
            runtime,
            registry,
            service,
            hydrator,
            negotiated_protocol_version: NegotiatedServerProtocolVersion::default(),
            protocol_negotiation: tokio::sync::Mutex::new(None),
        })
    }

    /// Starts a registered Flow on pull Workers with other options using server defaults.
    ///
    /// Returns the server-assigned run ID. `input` is encoded for the registered starting Step.
    /// Changed in 1.25.0: starts always route to pull Workers.
    /// Success acknowledges the start; it does not wait for a Worker or Flow completion.
    ///
    /// ```no_run
    /// use dex_sdk::{Client, Flow, FlowStatus, SdkResult};
    /// use std::time::Duration;
    ///
    /// fn start_and_wait<F: Flow>(client: &Client, flow: &F, input: F::StartInput)
    ///     -> SdkResult<(String, FlowStatus)> {
    ///     let run_id = client.start_flow(flow, "order-1", input)?;
    ///     let result = client.wait_for_flow_with_timeout("order-1", Duration::from_secs(60))?;
    ///     Ok((run_id, result.status()))
    /// }
    /// ```
    ///
    /// Use [`Self::wait_for_flow`] to observe the terminal result.
    ///
    /// # Errors
    ///
    /// Returns a definition or mapping error locally, [`SdkError::FlowAlreadyStarted`] for a reuse
    /// conflict, or another service-backed [`SdkError`] when Dex rejects the request.
    pub fn start_flow<SomeFlow: Flow>(
        &self,
        flow: &SomeFlow,
        flow_id: &str,
        input: SomeFlow::StartInput,
    ) -> SdkResult<String> {
        self.start_flow_with_options(flow, flow_id, input, StartFlowOptions::new())
    }

    /// Starts a registered Flow with explicit lifecycle, retry, and initial persistence options.
    ///
    /// Always selects pull Workers, overriding any server-configured push target.
    /// Returns the server-assigned run ID. A missing request ID is replaced with a generated UUID.
    /// When the options contain an initial value for a hard-delete Attribute, the Client first
    /// uses the Server protocol version negotiated before the first service request.
    ///
    /// # Errors
    ///
    /// Returns [`SdkError`] for invalid IDs, unregistered or mismatched Flow types, encoding
    /// failures, invalid option durations, reuse conflicts, and service failures. Returns
    /// [`SdkError::InvalidArgument`] for an invalid initial hard-delete Attribute value or a
    /// definition that does not match the registered one. Returns [`SdkError::Service`] when the
    /// Server does not support the protocol version this SDK requires.
    pub fn start_flow_with_options<SomeFlow: Flow>(
        &self,
        flow: &SomeFlow,
        flow_id: &str,
        input: SomeFlow::StartInput,
        options: StartFlowOptions,
    ) -> SdkResult<String> {
        require_name(flow_id, "Flow ID")?;
        let registered = self.registry.flow(flow.flow_type())?;
        if registered.type_id != TypeId::of::<SomeFlow>() {
            return Err(SdkError::FlowDefinition {
                message: format!(
                    "Flow {} does not match the registered Rust type",
                    flow.flow_type()
                ),
            });
        }
        let (start_step_type, step_input, step_options) = match &registered.start_step {
            Some(step) => (
                step.name.to_string(),
                Some(value_mapper::encode(&input)?),
                Some(
                    map_step_options(
                        registered,
                        registered
                            .handler
                            .step_options(step.name)
                            .map_err(sdk_handler_error)?,
                    )
                    .map_err(sdk_handler_error)?,
                ),
            ),
            None => (String::new(), None, None),
        };
        let flow_timeout_seconds = optional_seconds(options.timeout)?;
        let flow_timeout_policy =
            map_flow_timeout_policy(registered, flow_timeout_seconds, options.timeout_policy)?;
        let request = StartFlowRequest {
            flow_id: flow_id.to_string(),
            flow_type: registered.name.to_string(),
            flow_timeout_seconds,
            flow_timeout_policy,
            start_step_type,
            step_input,
            step_options,
            flow_start_options: Some(self.map_start_options(
                registered,
                &options,
                flow_timeout_seconds,
                flow_timeout_policy,
            )?),
            request_id: options
                .request_id
                .clone()
                .unwrap_or_else(|| Uuid::new_v4().to_string()),
        };
        self.require_server_protocol("start_flow")?;
        self.negotiated_protocol_version
            .require_named_task_queue_support(&options.task_queue, "start_flow")
            .map_err(|error| SdkError::Service {
                service: ServiceError::local("start_flow", error),
            })?;
        let mut service = self.service.clone();
        self.runtime.block_on(async {
            service
                .start_flow(request)
                .await
                .map(|response| response.into_inner().run_id)
                .map_err(|status| {
                    SdkError::from_status(
                        status,
                        "start_flow",
                        Some(flow_id),
                        FlowTargetRequirement::None,
                    )
                })
        })
    }

    /// Invokes a registered RPC with typed input and decodes its typed output.
    ///
    /// ```no_run
    /// use dex_sdk::{Client, Rpc, SdkResult};
    /// fn describe_order(client: &Client, rpc: Rpc<(), String>) -> SdkResult<String> {
    ///     client.invoke_rpc("order-1", rpc, ())
    /// }
    /// ```
    ///
    /// The call blocks until the handler result is decoded or a request error occurs.
    /// RPCs do not enter the durable Step queue. When no Worker takes the RPC within the dispatch
    /// wait, Dex returns an Unavailable service error. The dispatch wait is the Server's configurable
    /// `matching.dispatchWait` setting, which defaults to ten seconds and is bounded by the caller's
    /// deadline.
    /// A timeout or lost response does not prove the handler performed no effects; make retries safe.
    ///
    /// A non-transactional RPC without Attribute locks starts from a backend query. A retained
    /// terminal execution can serve it when the handler returns no durable effects. Locks,
    /// transactional execution, returned effects, or server policy can require an active
    /// execution.
    ///
    /// # Errors
    ///
    /// Returns [`SdkError::RpcLockConflict`] when locks cannot be acquired, WorkerInvocation for a
    /// handler failure, FlowNotActiveOrNotFound when the target is missing or the selected path cannot
    /// use its closed execution, or a
    /// mapping/service error.
    pub fn invoke_rpc<Input: Value, Output: Value>(
        &self,
        flow_id: &str,
        rpc: Rpc<Input, Output>,
        input: Input,
    ) -> SdkResult<Output> {
        self.do_invoke_rpc(flow_id, rpc.name(), &input, &RpcInvokeOptions::new())
    }

    /// Invokes a registered RPC with typed input and additive runtime map-instance selections.
    ///
    /// # Errors
    ///
    /// Returns a local definition error for an unregistered or wrong-kind selection. Invalid map
    /// instance names and all errors documented by [`Self::invoke_rpc`] are also returned.
    pub fn invoke_rpc_with_options<Input: Value, Output: Value>(
        &self,
        flow_id: &str,
        rpc: Rpc<Input, Output>,
        input: Input,
        options: RpcInvokeOptions,
    ) -> SdkResult<Output> {
        self.do_invoke_rpc(flow_id, rpc.name(), &input, &options)
    }

    /// Invokes a registered no-input RPC and decodes its typed output.
    ///
    /// Returns the same errors as [`Self::invoke_rpc`].
    pub fn invoke_rpc_without_input<Output: Value>(
        &self,
        flow_id: &str,
        rpc: Rpc<(), Output>,
    ) -> SdkResult<Output> {
        self.do_invoke_rpc(flow_id, rpc.name(), &(), &RpcInvokeOptions::new())
    }

    /// Invokes a registered no-input RPC with additive runtime map-instance selections.
    ///
    /// # Errors
    ///
    /// Returns the same errors as [`Self::invoke_rpc_with_options`].
    pub fn invoke_rpc_without_input_with_options<Output: Value>(
        &self,
        flow_id: &str,
        rpc: Rpc<(), Output>,
        options: RpcInvokeOptions,
    ) -> SdkResult<Output> {
        self.do_invoke_rpc(flow_id, rpc.name(), &(), &options)
    }

    /// Appends one typed best-effort Stream message with source metadata.
    ///
    /// The Flow instance need not exist or be active. Every call appends a message, including calls
    /// that repeat the same source. A source may contain `#`.
    ///
    /// # Errors
    ///
    /// Returns a definition error for an unregistered Stream, InvalidArgument for an empty Flow ID
    /// or source, a mapping error, or a FlowService failure.
    pub fn write_stream<T: Value>(
        &self,
        flow_id: &str,
        stream: &Stream<T>,
        source: &str,
        value: T,
    ) -> SdkResult<()> {
        require_name(flow_id, "Flow ID")?;
        require_name(source, "Stream source")?;
        let flow_type = self.registry.flow_for_stream(stream)?.name.to_string();
        let request = WriteStreamRequest {
            flow_id: flow_id.to_string(),
            flow_type,
            stream_name: stream.name().to_string(),
            stream_capacity_bytes: stream.stream_capacity_bytes(),
            value: Some(value_mapper::encode(&value)?),
            source: source.to_string(),
        };
        self.call_empty(
            "write_stream",
            Some(flow_id),
            FlowTargetRequirement::None,
            |mut service| async move { service.write_stream(request).await },
        )
    }

    /// Blocks for the next retained Stream message using the server default wait.
    ///
    /// An empty token starts at the retained head. Pass the returned token unchanged to resume.
    pub fn read_stream<T: Value>(
        &self,
        flow_id: &str,
        stream: &Stream<T>,
        resume_token: &str,
    ) -> SdkResult<StreamMessage<T>> {
        self.read_stream_result(flow_id, stream, resume_token, None)
    }

    /// Blocks for the next retained Stream message with an explicit server wait.
    ///
    /// # Errors
    ///
    /// Returns [`SdkError::LongPollTimeout`] when no message arrives, plus the same definition,
    /// duration, mapping, and service errors as [`Self::read_stream`].
    pub fn read_stream_with_timeout<T: Value>(
        &self,
        flow_id: &str,
        stream: &Stream<T>,
        resume_token: &str,
        timeout: Duration,
    ) -> SdkResult<StreamMessage<T>> {
        self.read_stream_result(flow_id, stream, resume_token, Some(timeout))
    }

    /// Returns one newest-first page of retained Stream messages without waiting for writes.
    ///
    /// An empty `before_page_token` starts at the retained tail. Pass
    /// [`StreamMessagesPage::next_page_token`] unchanged to read the next, older page. Stream
    /// trimming may remove messages between pages.
    /// Tokens are exclusive and scoped to the Flow ID, the selected Flow type, and the Stream. A
    /// token from another Flow ID or Stream is rejected. If trimming passes the page anchor, the
    /// server returns an empty page rather than restarting at the tail. New writes after the first
    /// page do not enter that older-page chain.
    ///
    /// `page_size` must be between 1 and the Server's `streamStore.maxReadMessages` setting, which
    /// defaults to 1000. The Server rejects a larger value; it does not reduce it.
    ///
    /// # Errors
    ///
    /// Returns InvalidArgument when `flow_id` is empty, `page_size` is not positive, or
    /// `page_size` exceeds `streamStore.maxReadMessages`. Returns a
    /// definition error for an unregistered Stream, a mapping error for an incompatible retained
    /// value, or a FlowService error when the request fails.
    pub fn list_stream_messages<T: Value>(
        &self,
        flow_id: &str,
        stream: &Stream<T>,
        page_size: i32,
        before_page_token: &str,
    ) -> SdkResult<StreamMessagesPage<T>> {
        require_name(flow_id, "Flow ID")?;
        if page_size < 1 {
            return Err(invalid("Stream page size must be positive"));
        }
        let flow_type = self.registry.flow_for_stream(stream)?.name.to_string();
        let request = ListStreamMessagesRequest {
            flow_id: flow_id.to_string(),
            flow_type,
            stream_name: stream.name().to_string(),
            page_size,
            before_page_token: before_page_token.to_string(),
        };
        self.require_server_protocol("list_stream_messages")?;
        let mut service = self.service.clone();
        let response = self.runtime.block_on(async {
            service
                .list_stream_messages(request)
                .await
                .map(|response| response.into_inner())
                .map_err(|status| {
                    SdkError::from_status(
                        status,
                        "list_stream_messages",
                        Some(flow_id),
                        FlowTargetRequirement::None,
                    )
                })
        })?;
        let messages = response
            .messages
            .into_iter()
            .map(|message| {
                if message.resume_token.is_empty() {
                    return Err(invalid("ListStreamMessages returned an empty resume token"));
                }
                let value = message
                    .value
                    .ok_or_else(|| invalid("ListStreamMessages omitted a Stream message Value"))?;
                let created_time = message.created_time.map(timestamp).ok_or_else(|| {
                    invalid("ListStreamMessages omitted a Stream message creation time")
                })?;
                Ok(StreamMessage {
                    value: value_mapper::decode(&value)?,
                    resume_token: message.resume_token,
                    created_time,
                    source: message.source,
                })
            })
            .collect::<SdkResult<Vec<_>>>()?;
        Ok(StreamMessagesPage {
            messages,
            next_page_token: response.next_page_token,
        })
    }

    /// Blocks until a Flow closes and returns its terminal result.
    ///
    /// # Errors
    ///
    /// Returns FlowNotFound for an unknown ID or a mapping/service error. Every terminal status is
    /// represented by [`FlowResult`]. This overload has no client-side timeout.
    pub fn wait_for_flow(&self, flow_id: &str) -> SdkResult<FlowResult> {
        self.wait_for_flow_result(flow_id, None)
    }

    /// Blocks up to `timeout` and returns the terminal result with all output-bearing completions.
    ///
    /// Like [`Self::wait_for_flow`], terminal failures are returned as [`FlowResult`], not as
    /// request errors. A wait timeout does not stop the Flow. Durations use whole seconds.
    /// See the [`Self::start_flow`] example for a start followed by a bounded wait.
    ///
    /// # Errors
    ///
    /// Returns [`SdkError::LongPollTimeout`] when the timeout elapses while the Flow remains active;
    /// other errors match [`Self::wait_for_flow`].
    pub fn wait_for_flow_with_timeout(
        &self,
        flow_id: &str,
        timeout: Duration,
    ) -> SdkResult<FlowResult> {
        self.wait_for_flow_result(flow_id, Some(timeout))
    }

    /// Returns current identity, status, type, and start time for the latest run.
    ///
    /// # Errors
    ///
    /// Returns [`SdkError::FlowNotFound`] for an unknown ID or a service/protocol error.
    pub fn describe_flow(&self, flow_id: &str) -> SdkResult<FlowInfo> {
        self.require_server_protocol("describe_flow")?;
        let mut service = self.service.clone();
        let response = self.runtime.block_on(async {
            service
                .get_flow_summary(GetFlowSummaryRequest {
                    flow_id: flow_id.to_string(),
                    run_id: String::new(),
                })
                .await
                .map(|response| response.into_inner())
                .map_err(|status| {
                    SdkError::from_status(
                        status,
                        "describe_flow",
                        Some(flow_id),
                        FlowTargetRequirement::Existing,
                    )
                })
        })?;
        let execution = response
            .flow_run_id
            .ok_or_else(|| service_error("GetFlowSummary omitted FlowRunID"))?;
        Ok(FlowInfo {
            flow_id: execution.flow_id,
            run_id: execution.run_id,
            flow_type: response.flow_type,
            status: map_flow_status(response.flow_status)?,
            started_at: response
                .start_time
                .map_or(SystemTime::UNIX_EPOCH, timestamp),
        })
    }

    /// Executes a server search query and returns its first page.
    ///
    /// `page_size` must be non-negative; zero uses the server default. Indexed values are hydrated
    /// and decoded as [`serde_json::Value`].
    pub fn search_flows(&self, query: &str, page_size: i32) -> SdkResult<SearchFlowsPage> {
        self.search_flows_page(query, page_size, "")
    }

    /// Continues a search with the opaque token returned by a previous page.
    ///
    /// Keep `query` and `page_size` consistent across pages. An empty returned token marks the last
    /// page.
    pub fn search_flows_page(
        &self,
        query: &str,
        page_size: i32,
        next_page_token: &str,
    ) -> SdkResult<SearchFlowsPage> {
        if page_size < 0 {
            return Err(invalid("search page size must not be negative"));
        }
        self.require_server_protocol("search_flows")?;
        let mut service = self.service.clone();
        self.runtime.block_on(async {
            let response = service
                .search_flows(SearchFlowsRequest {
                    query: query.to_string(),
                    page_size,
                    next_page_token: next_page_token.to_string(),
                })
                .await
                .map(|response| response.into_inner())
                .map_err(|status| {
                    SdkError::from_status(status, "search_flows", None, FlowTargetRequirement::None)
                })?;
            let values = response
                .flow_runs
                .iter()
                .flat_map(|entry| {
                    entry.indexed_attributes.iter().map(|attribute| {
                        attribute
                            .value
                            .clone()
                            .map(|value| (entry.flow_id.clone(), value))
                            .ok_or_else(|| invalid("Indexed Attribute has no Value"))
                    })
                })
                .collect::<SdkResult<Vec<_>>>()?;
            let mut values = self.hydrator.hydrate_for_flows(values).await?.into_iter();
            let mut flows = Vec::with_capacity(response.flow_runs.len());
            for entry in response.flow_runs {
                let mut indexed_attributes = BTreeMap::new();
                for attribute in entry.indexed_attributes {
                    let value = values
                        .next()
                        .ok_or_else(|| invalid("Indexed Attribute hydration count mismatch"))?;
                    indexed_attributes.insert(attribute.key, value_mapper::decode_untyped(&value)?);
                }
                flows.push(SearchFlowEntry {
                    flow_id: entry.flow_id,
                    run_id: entry.run_id,
                    flow_type: entry.flow_type,
                    status: map_flow_status(entry.flow_status)?,
                    started_at: entry.start_time.map(timestamp),
                    closed_at: entry.close_time.map(timestamp),
                    indexed_attributes,
                });
            }
            Ok(SearchFlowsPage {
                flows,
                next_page_token: response.next_page_token,
            })
        })
    }

    /// Cancels, terminates, or fails an active Flow according to `options`.
    ///
    /// Success acknowledges the stop request. Use [`Self::wait_for_flow`] to observe its terminal
    /// status. This does not stop the Worker or undo effects in an external service.
    ///
    /// # Errors
    ///
    /// Returns [`SdkError::FlowNotActiveOrNotFound`] when no active run exists or another service error.
    pub fn stop_flow(&self, flow_id: &str, options: StopFlowOptions) -> SdkResult<()> {
        let stop_type = match options.stop_type {
            StopType::Cancel => ProtoStopType::Cancel,
            StopType::Terminate => ProtoStopType::Terminate,
            StopType::Fail => ProtoStopType::Fail,
        };
        self.call_empty(
            "stop_flow",
            Some(flow_id),
            FlowTargetRequirement::Active,
            |mut service| async move {
                service
                    .stop_flow(StopFlowRequest {
                        flow_id: flow_id.to_string(),
                        run_id: String::new(),
                        reason: options.reason.unwrap_or_default(),
                        stop_type: stop_type as i32,
                    })
                    .await
            },
        )
    }

    /// Re-runs an existing Flow from the selected historical point.
    ///
    /// Time travel keeps the Flow's run ID and starts the next run attempt of that run. Re-run Steps
    /// reuse the Flow ID, run ID, and Step execution IDs of the original execution. An external
    /// idempotency key built only from those IDs therefore matches the effect from before time
    /// travel. Reconcile such effects, or build keys that the application changes for each re-run.
    ///
    /// Returns the run ID, which is the same run ID the Flow had before time travel.
    ///
    /// # Errors
    ///
    /// Returns InvalidArgument for an unrepresentable time, FlowNotFound for an unknown ID, or
    /// another service error.
    pub fn time_travel(&self, flow_id: &str, options: TimeTravelOptions) -> SdkResult<String> {
        let mut request = ResetFlowRequest {
            flow_id: flow_id.to_string(),
            reset_type: FlowResetType::Unspecified as i32,
            reason: options.reason.unwrap_or_default(),
            skip_writes_reapply: options.skip_writes_reapply,
            ..Default::default()
        };
        match options.point {
            TimeTravelPoint::Beginning => request.reset_type = FlowResetType::Beginning as i32,
            TimeTravelPoint::HistoryEventTime(time) => {
                request.reset_type = FlowResetType::HistoryEventTime as i32;
                request.history_event_time = rfc3339(time)?;
            }
            TimeTravelPoint::StepType(step_type) => {
                request.reset_type = FlowResetType::StepType as i32;
                request.step_type = step_type.to_string();
            }
            TimeTravelPoint::StepExecution(execution, method) => {
                request.reset_type = FlowResetType::StepExecutionId as i32;
                request.step_execution_id =
                    format!("{}-{}", execution.step_type, execution.execution_number);
                request.step_method = match method {
                    TimeTravelStepMethod::WaitFor => FlowResetStepMethod::WaitFor as i32,
                    TimeTravelStepMethod::Execute => FlowResetStepMethod::Execute as i32,
                };
            }
        }
        self.require_server_protocol("time_travel")?;
        let mut service = self.service.clone();
        self.runtime.block_on(async {
            service
                .reset_flow(request)
                .await
                .map(|response| response.into_inner().run_id)
                .map_err(|status| {
                    SdkError::from_status(
                        status,
                        "time_travel",
                        Some(flow_id),
                        FlowTargetRequirement::Existing,
                    )
                })
        })
    }

    /// Marks one timer in an active Step execution as satisfied immediately.
    ///
    /// # Errors
    ///
    /// Returns InvalidArgument for an oversized index, FlowNotActiveOrNotFound for a terminal Flow, or a
    /// service error when the Step execution or timer cannot be targeted.
    pub fn skip_timer(
        &self,
        flow_id: &str,
        step_execution: StepExecutionId,
        timer: TimerId,
    ) -> SdkResult<()> {
        let (timer_condition_id, timer_condition_index) = match timer {
            TimerId::ConditionId(id) => (id, None),
            TimerId::ConditionIndex(index) => (
                String::new(),
                Some(i32::try_from(index).map_err(|_| invalid("timer index exceeds int32"))?),
            ),
        };
        self.call_empty(
            "skip_timer",
            Some(flow_id),
            FlowTargetRequirement::Active,
            |mut service| async move {
                service
                    .skip_timer(SkipTimerRequest {
                        flow_id: flow_id.to_string(),
                        run_id: String::new(),
                        step_execution_id: format!(
                            "{}-{}",
                            step_execution.step_type, step_execution.execution_number
                        ),
                        timer_condition_id,
                        timer_condition_index,
                    })
                    .await
            },
        )
    }

    /// Blocks until one Step execution completes or its request budget expires.
    ///
    /// The Server does not read the request ID for this wait.
    /// All long-poll waits on one Flow share a cap that defaults to 1,000
    /// (`limits.maxLongPollWaitersPerFlow`). A wait over the cap returns a service error with
    /// `ResourceExhausted`. A wait frees its slot when it returns or the caller disconnects.
    ///
    /// # Errors
    ///
    /// Returns [`SdkError::RequestTimeout`] when a positive request timeout expires,
    /// FlowNotActiveOrNotFound when appropriate, or another service error. Successful completion returns
    /// `()` and does not decode Step output.
    pub fn wait_for_step_completion(
        &self,
        flow_id: &str,
        step_execution: StepExecutionId,
        options: WaitForStepCompletionOptions,
    ) -> SdkResult<()> {
        let request_budget = ClientRequestBudget::new(options.request_timeout)?;
        self.require_server_protocol_within_budget(
            "wait_for_step_completion",
            flow_id,
            &request_budget,
        )?;
        loop {
            let request_attempt =
                request_budget.next_attempt("wait_for_step_completion", flow_id)?;
            let request_flow_id = flow_id.to_string();
            let request_step_type = step_execution.step_type.to_string();
            let request_step_execution_number = step_execution.execution_number.to_string();
            let request_id = options.request_id.clone();
            let result = self.call_empty(
                "wait_for_step_completion",
                Some(flow_id),
                FlowTargetRequirement::Active,
                |mut service| async move {
                    let mut request = Request::new(WaitForStepCompletionRequest {
                        flow_id: request_flow_id,
                        step_type: request_step_type,
                        step_execution_number: request_step_execution_number,
                        request_timeout_seconds: request_attempt.request_timeout_seconds,
                        request_id,
                    });
                    if let Some(transport_timeout) = request_attempt.transport_timeout {
                        request.set_timeout(transport_timeout);
                    }
                    service.wait_for_step_completion(request).await
                },
            );
            match result {
                Err(SdkError::LongPollTimeout { .. }) => continue,
                Err(error)
                    if error.service_error().is_some_and(|service| {
                        matches!(service.code(), Code::DeadlineExceeded | Code::Cancelled)
                    }) && request_budget.has_deadline() =>
                {
                    return Err(SdkError::request_timeout(
                        "wait_for_step_completion",
                        flow_id,
                    ));
                }
                result => return result,
            }
        }
    }

    /// Blocks until a singleton Attribute in the current run satisfies `attribute_match`.
    ///
    /// Returns the current value observed by the successful wait. The Server does not read the
    /// request ID for this wait. String and Boolean
    /// Attributes support equality matches. Integer and floating-point Attributes support every
    /// match. A positive request-timeout expiry returns [`SdkError::RequestTimeout`]. A
    /// hard-delete Attribute, defined with [`Attribute::hard_delete_on_null`], returns
    /// [`SdkError::InvalidArgument`] before the Client contacts Dex.
    ///
    /// All long-poll waits on one Flow share a cap that defaults to 1,000
    /// (`limits.maxLongPollWaitersPerFlow`). A wait over the cap returns a service error with
    /// `ResourceExhausted`. A wait frees its slot when it returns or the caller disconnects.
    pub fn wait_for_attribute_match<T: Value>(
        &self,
        flow_id: &str,
        attribute: &Attribute<T>,
        attribute_match: AttributeMatch<T>,
        options: WaitForAttributeOptions,
    ) -> SdkResult<T> {
        require_waitable_attribute(attribute.name(), attribute.is_hard_delete_on_null())?;
        self.wait_for_attribute_value(flow_id, attribute.name(), &attribute_match, options)
    }

    /// Blocks until one AttributeMap instance satisfies `attribute_match`.
    ///
    /// This targets the current run and otherwise has the same match, request-timeout,
    /// request-ID, wait-cap, return-value, and error behavior as [`Self::wait_for_attribute_match`]. A
    /// hard-delete AttributeMap, defined with [`AttributeMap::hard_delete_on_null`], returns
    /// [`SdkError::InvalidArgument`] before the Client contacts Dex.
    pub fn wait_for_attribute_map_instance_match<T: Value>(
        &self,
        flow_id: &str,
        attribute: &AttributeMap<T>,
        instance: &str,
        attribute_match: AttributeMatch<T>,
        options: WaitForAttributeOptions,
    ) -> SdkResult<T> {
        require_waitable_attribute(attribute.name(), attribute.is_hard_delete_on_null())?;
        self.wait_for_attribute_value(
            flow_id,
            &map_physical_name(attribute.name(), instance)?,
            &attribute_match,
            options,
        )
    }

    fn wait_for_attribute_value<T: Value>(
        &self,
        flow_id: &str,
        key: &str,
        attribute_match: &AttributeMatch<T>,
        options: WaitForAttributeOptions,
    ) -> SdkResult<T> {
        let mut encoded_match = attribute_match.encode()?;
        encoded_match.key = key.to_string();
        let request_budget = ClientRequestBudget::new(options.request_timeout)?;
        self.require_server_protocol_within_budget(
            "wait_for_attribute_match",
            flow_id,
            &request_budget,
        )?;
        let response = loop {
            let request_attempt =
                request_budget.next_attempt("wait_for_attribute_match", flow_id)?;
            let mut service = self.service.clone();
            let mut request = Request::new(WaitForAttributeRequest {
                flow_id: flow_id.to_string(),
                r#match: Some(encoded_match.clone()),
                request_timeout_seconds: request_attempt.request_timeout_seconds,
                request_id: options.request_id.clone(),
            });
            if let Some(transport_timeout) = request_attempt.transport_timeout {
                request.set_timeout(transport_timeout);
            }
            let result = self
                .runtime
                .block_on(service.wait_for_attribute(request))
                .map_err(|status| {
                    SdkError::from_status(
                        status,
                        "wait_for_attribute_match",
                        Some(flow_id),
                        FlowTargetRequirement::Active,
                    )
                });
            match result {
                Ok(response) => break response.into_inner(),
                Err(SdkError::LongPollTimeout { .. }) => continue,
                Err(error)
                    if error.service_error().is_some_and(|service| {
                        matches!(service.code(), Code::DeadlineExceeded | Code::Cancelled)
                    }) && request_budget.has_deadline() =>
                {
                    return Err(SdkError::request_timeout(
                        "wait_for_attribute_match",
                        flow_id,
                    ));
                }
                Err(error) => return Err(error),
            }
        };
        let matched_value = response.matched_value.ok_or_else(|| SdkError::Service {
            service: ServiceError::local(
                "wait_for_attribute_match",
                "WaitForAttribute response is incomplete",
            ),
        })?;
        value_mapper::decode(&matched_value)
    }

    /// Replaces mutable runtime configuration fields on an active Flow.
    ///
    /// Leaves the Worker target unset, preserving the active Flow target.
    /// Other unset fields also retain the active Flow setting.
    pub fn update_flow_config(&self, flow_id: &str, config: FlowConfig) -> SdkResult<()> {
        let flow_config = self.map_flow_config(Some(&config))?;
        self.call_empty(
            "update_flow_config",
            Some(flow_id),
            FlowTargetRequirement::Active,
            |mut service| async move {
                service
                    .update_flow_config(UpdateFlowConfigRequest {
                        flow_id: flow_id.to_string(),
                        run_id: String::new(),
                        flow_config: Some(flow_config),
                    })
                    .await
            },
        )
    }

    /// Verifies that Dex FlowService responds successfully.
    ///
    /// Returns `Ok(())` for a healthy service and a service-backed [`SdkError`] otherwise.
    pub fn health_check(&self) -> SdkResult<()> {
        self.call_empty(
            "health_check",
            None,
            FlowTargetRequirement::None,
            |mut service| async move { service.health_check(()).await },
        )
    }

    fn do_invoke_rpc<Input: Value, Output: Value>(
        &self,
        flow_id: &str,
        rpc_name: &str,
        input: &Input,
        invoke_options: &RpcInvokeOptions,
    ) -> SdkResult<Output> {
        let (flow, rpc) = self.registry.rpc_with_flow(rpc_name)?;
        let mut lock_attribute_keys = rpc
            .locks
            .iter()
            .map(|lock| lock.physical_name())
            .collect::<BTreeSet<_>>();
        for lock in &invoke_options.lock_attribute_map_instances {
            let Some(instance) = lock.instance() else {
                return Err(SdkError::FlowDefinition {
                    message: "RpcInvokeOptions lock must target an AttributeMap instance".into(),
                });
            };
            require_rpc_invoke_definition(
                flow,
                lock.attribute_name(),
                crate::persistence::PersistenceKind::AttributeMap,
            )?;
            crate::registry::validate_map_instance(instance).map_err(invalid)?;
            lock_attribute_keys.insert(lock.physical_name());
        }
        let mut load_attribute_map_instances = rpc
            .load_attribute_maps
            .iter()
            .map(|load| map_load_name(&load.name, load.instance.as_deref()))
            .collect::<BTreeSet<_>>();
        for load in &invoke_options.load_attribute_map_instances {
            let instance = require_rpc_invoke_map_load(
                flow,
                &load.name,
                load.instance.as_deref(),
                crate::persistence::PersistenceKind::AttributeMap,
            )?;
            load_attribute_map_instances
                .insert(crate::registry::physical_name(&load.name, instance));
        }
        let mut load_channel_names = rpc
            .load_channels
            .iter()
            .map(|load| load.name.clone())
            .collect::<Vec<_>>();
        load_channel_names.sort();
        let mut load_channel_map_instances = rpc
            .load_channel_maps
            .iter()
            .map(|load| map_load_name(&load.name, load.instance.as_deref()))
            .collect::<BTreeSet<_>>();
        for load in &invoke_options.load_channel_map_instances {
            let instance = require_rpc_invoke_map_load(
                flow,
                &load.name,
                load.instance.as_deref(),
                crate::persistence::PersistenceKind::ChannelMap,
            )?;
            load_channel_map_instances.insert(crate::registry::physical_name(&load.name, instance));
        }
        let request = InvokeRpcRequest {
            flow_id: flow_id.to_string(),
            run_id: String::new(),
            rpc_name: rpc_name.to_string(),
            input: Some(value_mapper::encode(input)?),
            timeout_seconds: optional_seconds(rpc.timeout)?,
            lock_attribute_keys: lock_attribute_keys.into_iter().collect(),
            request_id: Uuid::new_v4().to_string(),
            is_transactional: rpc.is_transactional,
            load_attribute_map_instances: load_attribute_map_instances.into_iter().collect(),
            load_channel_names,
            load_channel_map_instances: load_channel_map_instances.into_iter().collect(),
        };
        self.require_server_protocol("invoke_rpc")?;
        let mut service = self.service.clone();
        let output = self.runtime.block_on(async {
            service
                .invoke_rpc(request)
                .await
                .map(|response| response.into_inner().output)
                .map_err(|status| {
                    SdkError::from_status(
                        status,
                        "invoke_rpc",
                        Some(flow_id),
                        FlowTargetRequirement::Active,
                    )
                })
        })?;
        let output = output.ok_or_else(|| invalid("InvokeRPC omitted output"))?;
        let output = self
            .runtime
            .block_on(self.hydrator.hydrate(flow_id, output))?;
        value_mapper::decode(&output)
    }

    fn read_stream_result<T: Value>(
        &self,
        flow_id: &str,
        stream: &Stream<T>,
        resume_token: &str,
        timeout: Option<Duration>,
    ) -> SdkResult<StreamMessage<T>> {
        require_name(flow_id, "Flow ID")?;
        let flow_type = self.registry.flow_for_stream(stream)?.name.to_string();
        let wait_time_seconds = optional_seconds(timeout)?;
        let request = ReadStreamRequest {
            flow_id: flow_id.to_string(),
            flow_type,
            stream_name: stream.name().to_string(),
            resume_token: resume_token.to_string(),
            wait_time_seconds,
        };
        self.require_server_protocol("read_stream")?;
        let mut service = self.service.clone();
        let response = self.runtime.block_on(async {
            service
                .read_stream(request)
                .await
                .map(|response| response.into_inner())
                .map_err(|status| {
                    SdkError::from_status(
                        status,
                        "read_stream",
                        Some(flow_id),
                        FlowTargetRequirement::None,
                    )
                })
        })?;
        let message = response
            .message
            .ok_or_else(|| invalid("ReadStream omitted its message"))?;
        if message.resume_token.is_empty() {
            return Err(invalid("ReadStream returned an empty resume token"));
        }
        let value = message
            .value
            .ok_or_else(|| invalid("ReadStream omitted its Value"))?;
        let created_time = message
            .created_time
            .map(timestamp)
            .ok_or_else(|| invalid("ReadStream omitted its creation time"))?;
        Ok(StreamMessage {
            value: value_mapper::decode(&value)?,
            resume_token: message.resume_token,
            created_time,
            source: message.source,
        })
    }

    fn wait_for_flow_result(
        &self,
        flow_id: &str,
        timeout: Option<Duration>,
    ) -> SdkResult<FlowResult> {
        self.require_server_protocol("wait_for_flow")?;
        let mut service = self.service.clone();
        let response = self.runtime.block_on(async {
            service
                .wait_for_flow(WaitForFlowRequest {
                    flow_id: flow_id.to_string(),
                    run_id: String::new(),
                    needs_results: true,
                    wait_time_seconds: optional_seconds(timeout)?,
                })
                .await
                .map(|response| response.into_inner())
                .map_err(|status| {
                    SdkError::from_status(
                        status,
                        "wait_for_flow",
                        Some(flow_id),
                        FlowTargetRequirement::Existing,
                    )
                })
        });
        let response = response?;
        let mut outputs = Vec::with_capacity(response.results.len());
        for result in &response.results {
            outputs.push(result.completed_step_output.clone().ok_or_else(|| {
                SdkError::ValueMapping {
                    message: "Step completion output is required".to_string(),
                }
            })?);
        }
        let outputs = self
            .runtime
            .block_on(self.hydrator.hydrate_all(flow_id, outputs))?;
        let completions = response
            .results
            .into_iter()
            .zip(outputs)
            .map(|(result, output)| {
                StepCompletion::new(
                    result.completed_step_type,
                    result.completed_step_execution_id,
                    output,
                )
            })
            .collect();
        Ok(FlowResult::new(
            map_flow_status(response.flow_status)?,
            map_flow_error_type(response.error_type),
            (!response.error_message.is_empty()).then_some(response.error_message),
            completions,
        ))
    }

    fn map_start_options(
        &self,
        flow: &crate::registry::RegisteredFlow,
        options: &StartFlowOptions,
        flow_timeout_seconds: i32,
        flow_timeout_policy: i32,
    ) -> SdkResult<FlowStartOptions> {
        let attributes = options
            .attributes
            .iter()
            .map(|attribute| {
                let definition =
                    flow.persistence
                        .get(&attribute.attribute_name)
                        .ok_or_else(|| {
                            invalid(format!(
                                "initial Attribute is not registered: {}",
                                attribute.key
                            ))
                        })?;
                attribute.encode_attribute_write(definition)
            })
            .collect::<SdkResult<Vec<_>>>()?;
        if let Some(write) = attributes.iter().find(|write| write.is_hard_delete_on_null) {
            self.require_hard_delete_attribute_support("start_flow", &write.key)?;
        }
        task_queue_name::validate_flow_task_queue(&options.task_queue).map_err(invalid)?;
        let config = options.config_override.as_ref();
        let flow_config_override = Some(map_pull_flow_config(config, &options.task_queue)?);
        #[expect(
            clippy::needless_update,
            reason = "The test preserves forward-compatible construction of SDK options."
        )]
        let flow_start_options = FlowStartOptions {
            id_reuse_policy: match options.id_reuse_policy {
                IdReusePolicy::Default => ProtoIdReusePolicy::Unspecified,
                IdReusePolicy::AllowIfPreviousFailed => {
                    ProtoIdReusePolicy::AllowIfPreviousExistsAbnormally
                }
                IdReusePolicy::AllowIfNotRunning => ProtoIdReusePolicy::AllowIfNoRunning,
                IdReusePolicy::Disallow => ProtoIdReusePolicy::DisallowReuse,
                IdReusePolicy::TerminateIfRunning => ProtoIdReusePolicy::AllowTerminateIfRunning,
            } as i32,
            flow_start_delay_seconds: optional_seconds(options.start_delay)?,
            retry_policy: options
                .retry_policy
                .clone()
                .map(map_flow_retry)
                .transpose()?,
            attributes,
            flow_config_override,
            flow_already_started_options: Some(FlowAlreadyStartedOptions {
                ignore_already_started_error: options.ignore_already_started,
            }),
            timeout_handler_options: map_flow_timeout_handler_options(
                flow,
                flow_timeout_seconds,
                flow_timeout_policy,
                options.timeout_handler_options.as_ref(),
            )
            .map_err(sdk_handler_error)?,
            ..Default::default()
        };
        Ok(flow_start_options)
    }

    fn require_server_protocol(&self, operation: &'static str) -> SdkResult<()> {
        self.runtime
            .block_on(self.negotiate_server_protocol_once(operation))
    }

    /// Negotiates within the caller's remaining request budget and returns
    /// [`SdkError::RequestTimeout`] when it expires first; a timeout is never cached.
    fn require_server_protocol_within_budget(
        &self,
        operation: &'static str,
        flow_id: &str,
        request_budget: &ClientRequestBudget,
    ) -> SdkResult<()> {
        let Some(deadline) = request_budget.deadline() else {
            return self.require_server_protocol(operation);
        };
        self.runtime
            .block_on(async {
                tokio::time::timeout_at(
                    tokio::time::Instant::from_std(deadline),
                    self.negotiate_server_protocol_once(operation),
                )
                .await
            })
            .unwrap_or_else(|_| Err(SdkError::request_timeout(operation, flow_id)))
    }

    async fn negotiate_server_protocol_once(&self, operation: &'static str) -> SdkResult<()> {
        let mut cached = self.protocol_negotiation.lock().await;
        if cached.is_none() {
            let mut service = self.service.clone();
            let server_info = service
                .get_server_info(())
                .await
                .map_err(|status| {
                    SdkError::from_status(status, operation, None, FlowTargetRequirement::None)
                })?
                .into_inner();
            *cached = Some(negotiate_server_protocol(&server_info));
        }
        match cached.as_ref().expect("Server negotiation was cached") {
            Ok(version) => {
                self.negotiated_protocol_version.store(*version);
                Ok(())
            }
            Err(message) => Err(SdkError::Service {
                service: ServiceError::local("server_protocol", message.clone()),
            }),
        }
    }

    fn require_hard_delete_attribute_support(
        &self,
        operation: &'static str,
        attribute_key: &str,
    ) -> SdkResult<()> {
        self.require_server_protocol(operation)?;
        self.negotiated_protocol_version
            .require_hard_delete_attribute_support(attribute_key)
            .map_err(|error| SdkError::Service {
                service: ServiceError::local(operation, error),
            })
    }

    fn map_flow_config(&self, config: Option<&FlowConfig>) -> SdkResult<ProtoFlowConfig> {
        map_flow_config(config)
    }

    fn call_empty<Response, Future, Call>(
        &self,
        operation: &'static str,
        flow_id: Option<&str>,
        requirement: FlowTargetRequirement,
        call: Call,
    ) -> SdkResult<()>
    where
        Call: FnOnce(FlowServiceClient<CredentialChannel>) -> Future,
        Future: std::future::Future<Output = Result<tonic::Response<Response>, tonic::Status>>,
    {
        self.require_server_protocol(operation)?;
        let service = self.service.clone();
        self.runtime
            .block_on(call(service))
            .map(|_| ())
            .map_err(|status| SdkError::from_status(status, operation, flow_id, requirement))
    }
}

/// Returns the Flow configuration with a pull Worker target on `task_queue`; empty selects the
/// default task queue at start and the parent's task queue for a SubFlow.
pub(crate) fn map_pull_flow_config(
    config: Option<&FlowConfig>,
    task_queue: &str,
) -> SdkResult<ProtoFlowConfig> {
    Ok(ProtoFlowConfig {
        worker_target: Some(ProtoWorkerTarget {
            is_pull_mode: true,
            task_queue: task_queue.to_string(),
        }),
        ..map_flow_config(config)?
    })
}

pub(crate) fn map_flow_config(config: Option<&FlowConfig>) -> SdkResult<ProtoFlowConfig> {
    Ok(ProtoFlowConfig {
        active_step_search_mode: config
            .and_then(|config| config.active_step_search_mode)
            .map(|mode| match mode {
                ActiveStepSearchMode::All => ProtoSearchMode::EnabledForAll as i32,
                ActiveStepSearchMode::WithWaitFor => {
                    ProtoSearchMode::EnabledForStepsWithWaitFor as i32
                }
                ActiveStepSearchMode::Disabled => ProtoSearchMode::Disabled as i32,
            }),
        step_durability: config
            .and_then(|config| config.step_durability)
            .map(|durability| {
                (match durability {
                    StepDurability::Default => ProtoStepDurability::Unspecified,
                    StepDurability::Sync => ProtoStepDurability::Sync,
                    StepDurability::Async => ProtoStepDurability::Async,
                }) as i32
            }),
        worker_target: None,
        attribute_store_names: config.and_then(|config| {
            config
                .attribute_store_names
                .as_ref()
                .map(|names| AttributeStoreNames {
                    names: names.clone(),
                })
        }),
        // FlowConfig::history_window_events sets this; when unset, the server default applies.
        history_window_events: config
            .and_then(|config| config.history_window_events)
            .map(i32::try_from)
            .transpose()
            .map_err(|_| invalid("history window events exceed int32"))?,
        // FlowConfig::delete_expired_history_objects is deprecated and never sent.
        delete_expired_history_objects: None,
        offload_history: config.and_then(|config| config.offload_history),
    })
}

pub(crate) fn map_flow_retry(retry: RetryPolicy) -> SdkResult<FlowRetryPolicy> {
    Ok(FlowRetryPolicy {
        initial_interval_seconds: optional_seconds(retry.initial_interval)?,
        backoff_coefficient: retry.backoff_coefficient.unwrap_or_default() as f32,
        maximum_interval_seconds: optional_seconds(retry.maximum_interval)?,
        maximum_attempts: i32::try_from(retry.maximum_attempts.unwrap_or_default())
            .map_err(|_| invalid("maximum attempts exceed int32"))?,
    })
}

pub(crate) fn map_flow_status(status: i32) -> SdkResult<FlowStatus> {
    match ProtoFlowStatus::try_from(status).ok() {
        Some(ProtoFlowStatus::Running) => Ok(FlowStatus::Running),
        Some(ProtoFlowStatus::Completed) => Ok(FlowStatus::Completed),
        Some(ProtoFlowStatus::Failed) => Ok(FlowStatus::Failed),
        Some(ProtoFlowStatus::ServerSideTimeoutInternalOnly) => {
            Ok(FlowStatus::ServerSideTimeoutInternalOnly)
        }
        Some(ProtoFlowStatus::Terminated) => Ok(FlowStatus::Terminated),
        Some(ProtoFlowStatus::Canceled) => Ok(FlowStatus::Canceled),
        _ => Err(invalid(format!("unknown Flow status {status}"))),
    }
}

pub(crate) fn map_flow_error_type(error_type: i32) -> Option<FlowErrorType> {
    use dex_protocol::dex::FlowErrorType as ProtoFlowErrorType;

    match ProtoFlowErrorType::try_from(error_type).ok() {
        Some(ProtoFlowErrorType::StepDecisionFailingFlow) => {
            Some(FlowErrorType::StepDecisionFailed)
        }
        Some(ProtoFlowErrorType::ClientApiFailingFlow) => Some(FlowErrorType::ClientApiFailed),
        Some(ProtoFlowErrorType::WorkerApiFail) => Some(FlowErrorType::WorkerApiFailed),
        Some(ProtoFlowErrorType::InvalidUserFlowCode) => Some(FlowErrorType::InvalidUserFlowCode),
        Some(ProtoFlowErrorType::Internal) => Some(FlowErrorType::Internal),
        Some(ProtoFlowErrorType::FlowTimeout) => Some(FlowErrorType::FlowTimeout),
        _ => None,
    }
}

pub(crate) fn map_flow_timeout_policy(
    flow: &crate::registry::RegisteredFlow,
    timeout_seconds: i32,
    policy: FlowTimeoutPolicy,
) -> SdkResult<i32> {
    if timeout_seconds == 0 {
        if policy != FlowTimeoutPolicy::Default {
            return Err(invalid("Flow timeout policy requires a positive timeout"));
        }
        return Ok(ProtoFlowTimeoutPolicy::Unspecified as i32);
    }
    let resolved = match policy {
        FlowTimeoutPolicy::Default if flow.handler.has_timeout_handler() => {
            FlowTimeoutPolicy::Handler
        }
        FlowTimeoutPolicy::Default => FlowTimeoutPolicy::Fail,
        configured => configured,
    };
    if resolved == FlowTimeoutPolicy::Handler && !flow.handler.has_timeout_handler() {
        return Err(invalid(format!(
            "Flow {} has no timeout handler",
            flow.name
        )));
    }
    Ok(match resolved {
        FlowTimeoutPolicy::Default => ProtoFlowTimeoutPolicy::Unspecified,
        FlowTimeoutPolicy::Fail => ProtoFlowTimeoutPolicy::Fail,
        FlowTimeoutPolicy::Cancel => ProtoFlowTimeoutPolicy::Cancel,
        FlowTimeoutPolicy::Handler => ProtoFlowTimeoutPolicy::Handler,
    } as i32)
}

fn timestamp(timestamp: prost_types::Timestamp) -> SystemTime {
    if timestamp.seconds >= 0 {
        SystemTime::UNIX_EPOCH
            + Duration::from_secs(timestamp.seconds as u64)
            + Duration::from_nanos(timestamp.nanos.max(0) as u64)
    } else {
        SystemTime::UNIX_EPOCH - Duration::from_secs(timestamp.seconds.unsigned_abs())
    }
}

fn rfc3339(time: SystemTime) -> SdkResult<String> {
    let duration = time
        .duration_since(SystemTime::UNIX_EPOCH)
        .map_err(service_error)?;
    let days = (duration.as_secs() / 86_400) as i64;
    let seconds = duration.as_secs() % 86_400;
    let (year, month, day) = civil_from_days(days);
    let hour = seconds / 3_600;
    let minute = seconds % 3_600 / 60;
    let second = seconds % 60;
    Ok(format!(
        "{year:04}-{month:02}-{day:02}T{hour:02}:{minute:02}:{second:02}Z"
    ))
}

fn civil_from_days(days: i64) -> (i64, i64, i64) {
    let days = days + 719_468;
    let era = if days >= 0 { days } else { days - 146_096 } / 146_097;
    let day_of_era = days - era * 146_097;
    let year_of_era =
        (day_of_era - day_of_era / 1_460 + day_of_era / 36_524 - day_of_era / 146_096) / 365;
    let mut year = year_of_era + era * 400;
    let day_of_year = day_of_era - (365 * year_of_era + year_of_era / 4 - year_of_era / 100);
    let month_prime = (5 * day_of_year + 2) / 153;
    let day = day_of_year - (153 * month_prime + 2) / 5 + 1;
    let month = month_prime + if month_prime < 10 { 3 } else { -9 };
    year += i64::from(month <= 2);
    (year, month, day)
}

fn map_load_name(name: &str, instance: Option<&str>) -> String {
    match instance {
        Some(instance) => crate::registry::physical_name(name, instance),
        None => format!("{name}/"),
    }
}

fn require_rpc_invoke_map_load<'a>(
    flow: &crate::registry::RegisteredFlow,
    name: &str,
    instance: Option<&'a str>,
    expected_kind: crate::persistence::PersistenceKind,
) -> SdkResult<&'a str> {
    require_rpc_invoke_definition(flow, name, expected_kind)?;
    let instance = instance.ok_or_else(|| SdkError::FlowDefinition {
        message: "RpcInvokeOptions requires an exact map instance".to_string(),
    })?;
    crate::registry::validate_map_instance(instance).map_err(invalid)?;
    Ok(instance)
}

fn require_rpc_invoke_definition(
    flow: &crate::registry::RegisteredFlow,
    name: &str,
    expected_kind: crate::persistence::PersistenceKind,
) -> SdkResult<()> {
    flow.persistence
        .get(name)
        .filter(|definition| definition.kind == expected_kind)
        .ok_or_else(|| SdkError::FlowDefinition {
            message: format!(
                "Flow {} does not register the selected map {name}",
                flow.name
            ),
        })?;
    Ok(())
}

fn optional_seconds(duration: Option<Duration>) -> SdkResult<i32> {
    duration
        .map(seconds32)
        .transpose()
        .map(Option::unwrap_or_default)
}

pub(crate) fn seconds32(duration: Duration) -> SdkResult<i32> {
    if duration.subsec_nanos() != 0 {
        return Err(invalid("Duration must use whole seconds"));
    }
    i32::try_from(duration.as_secs()).map_err(|_| invalid("Duration exceeds int32"))
}

fn require_name(value: &str, kind: &str) -> SdkResult<()> {
    if value.is_empty() {
        Err(invalid(format!("{kind} is required")))
    } else {
        Ok(())
    }
}

fn map_physical_name(name: &str, instance: &str) -> SdkResult<String> {
    crate::registry::validate_map_instance(instance).map_err(invalid)?;
    Ok(crate::registry::physical_name(name, instance))
}

fn require_waitable_attribute(attribute_name: &str, is_hard_delete_on_null: bool) -> SdkResult<()> {
    if is_hard_delete_on_null {
        return Err(invalid(format!(
            "wait_for_attribute_match does not support hard-delete Attribute {attribute_name}"
        )));
    }
    Ok(())
}

fn sdk_handler_error(error: impl std::fmt::Display) -> SdkError {
    invalid(error.to_string())
}

pub(crate) fn invalid(message: impl Into<String>) -> SdkError {
    SdkError::InvalidArgument {
        message: message.into(),
    }
}

fn service_error(error: impl std::fmt::Display) -> SdkError {
    SdkError::Service {
        service: ServiceError::local("client", error.to_string()),
    }
}

#[cfg(test)]
mod tests;

#[cfg(test)]
#[path = "client_pull_routing_tests.rs"]
mod pull_routing_tests;
