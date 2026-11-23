// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use crate::{AttributeLock, AttributeMapLoad, ChannelMapLoad};

/// Configures the caller's request ID and runtime-selected map instances for one RPC invocation.
///
/// Dex unions runtime selections with the RPC definition's fixed locks and loads, then sorts and
/// deduplicates their physical names. Locks and loads are independent. Read-modify-write handlers
/// must add the same AttributeMap instance through both builder methods.
///
/// # Examples
///
/// ```
/// use dex_sdk::{AttributeMap, RpcInvokeOptions};
///
/// let profiles = AttributeMap::<String>::new("profiles");
/// let options = RpcInvokeOptions::new()
///     .lock_attribute_map_instance(profiles.lock("partition-007"))
///     .load_attribute_map_instance(profiles.load("partition-007"));
/// ```
#[derive(Clone, Debug, Default)]
pub struct RpcInvokeOptions {
    pub(crate) lock_attribute_map_instances: Vec<AttributeLock>,
    pub(crate) load_attribute_map_instances: Vec<AttributeMapLoad>,
    pub(crate) load_channel_map_instances: Vec<ChannelMapLoad>,
    pub(crate) request_id: Option<String>,
}

impl RpcInvokeOptions {
    /// Creates empty invocation options that preserve the RPC definition's behavior.
    pub fn new() -> Self {
        Self::default()
    }

    /// Sets the request ID for one logical RPC call.
    ///
    /// Pass the same `request_id` on every attempt to apply the call's Flow changes at most once
    /// while Dex Server retains the ID. Use a stable identifier, such as the ID of an event delivered
    /// at least once. By default, the SDK generates a new UUID for every call. The SDK forwards an
    /// empty string, and Dex Server rejects it with [`crate::GrpcCode::InvalidArgument`].
    ///
    /// Each Flow run keeps its newest request IDs, up to `limits.maxRequestIdsPerRun`.
    /// Other recorded Client requests, such as Attribute writes, share that window. A forgotten ID
    /// is treated as new. Each new run starts with an empty window.
    ///
    /// A transactional or locking RPC returns its recorded reply before dispatching the handler,
    /// even after the Flow closes while Dex retains the run. Concurrent attempts with the same ID
    /// share the in-flight invocation. For other RPCs, the handler runs again. If the repeat changes
    /// Flow state or records an event, Dex discards its changes and returns the recorded reply.
    /// A repeat that changes no Flow state and records no event returns its fresh output without
    /// checking the request ID. Side effects outside Dex can recur if the handler runs again.
    ///
    /// Use a separate ID for each logical call. Starting with Dex Server 0.1.37, the server matches
    /// only the ID where deduplication applies. A different RPC name, input, or options under a
    /// recorded ID can therefore return the earlier reply. Dex Server 0.1.36 and earlier reject a
    /// different request under a recorded ID with [`crate::GrpcCode::InvalidArgument`].
    ///
    /// A server-side failure before commit records nothing. A caller-side deadline or cancellation
    /// leaves the outcome unknown because the server can still commit. `UNAVAILABLE` after the
    /// server's commit budget also leaves the outcome unknown. Retry with the same request ID.
    /// A transactional or locking RPC that changes no Flow state keeps no ID if the run exceeds its
    /// Flow state budget. In that case a retry runs as a new request.
    ///
    /// Returns these options with the supplied ID.
    ///
    /// Since 1.45.8.
    ///
    /// # Examples
    ///
    /// ```
    /// use dex_sdk::{Client, Rpc, RpcInvokeOptions, SdkResult};
    ///
    /// // The Flow registers this RPC as transactional.
    /// const INCREMENT_COUNTER: Rpc<i32, i32> = Rpc::new("increment_counter");
    ///
    /// fn deliver_counter_event(
    ///     client: &Client,
    ///     flow_id: &str,
    ///     event_id: &str,
    ///     delta: i32,
    /// ) -> SdkResult<i32> {
    ///     client.invoke_rpc_with_options(
    ///         flow_id,
    ///         INCREMENT_COUNTER,
    ///         delta,
    ///         RpcInvokeOptions::new().request_id(format!("increment-counter/{event_id}")),
    ///     )
    /// }
    /// ```
    #[must_use]
    pub fn request_id(mut self, request_id: impl Into<String>) -> Self {
        self.request_id = Some(request_id.into());
        self
    }

    /// Adds one AttributeMap instance lock for this invocation.
    ///
    /// This does not load the instance into the handler snapshot.
    #[must_use]
    pub fn lock_attribute_map_instance(mut self, lock: AttributeLock) -> Self {
        self.lock_attribute_map_instances.push(lock);
        self
    }

    /// Adds one exact AttributeMap instance to the handler snapshot.
    #[must_use]
    pub fn load_attribute_map_instance(mut self, load: AttributeMapLoad) -> Self {
        self.load_attribute_map_instances.push(load);
        self
    }

    /// Adds one exact ChannelMap instance's pending messages to the handler snapshot.
    #[must_use]
    pub fn load_channel_map_instance(mut self, load: ChannelMapLoad) -> Self {
        self.load_channel_map_instances.push(load);
        self
    }
}
