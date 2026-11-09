// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use crate::StepDurability;

#[derive(Clone, Debug, Default)]
/// Overrides runtime behavior for one Flow.
///
/// StartFlow and SubFlow always select pull Workers, even when no override is supplied.
/// [`crate::Client::update_flow_config`] leaves the Worker target unset, preserving the active Flow
/// target. Other omitted fields use server defaults for StartFlow, inherit the parent configuration
/// for SubFlow, and retain the active Flow setting for updates.
/// Pass a value to [`crate::StartFlowOptions::config_override`] for one execution or to
/// [`crate::Client::update_flow_config`] for an active Flow.
pub struct FlowConfig {
    pub(crate) active_step_search_mode: Option<ActiveStepSearchMode>,
    pub(crate) attribute_store_names: Option<Vec<String>>,
    pub(crate) continue_as_new_threshold: Option<u32>,
    pub(crate) continue_as_new_page_size_bytes: Option<u32>,
    pub(crate) step_durability: Option<StepDurability>,
    pub(crate) history_window_events: Option<u32>,
    pub(crate) offload_history: Option<bool>,
}

impl FlowConfig {
    /// Creates a config with no overrides.
    pub fn new() -> Self {
        Self::default()
    }

    /// Controls which active Step types Dex places in the search index.
    pub fn active_step_search_mode(mut self, value: ActiveStepSearchMode) -> Self {
        self.active_step_search_mode = Some(value);
        self
    }

    /// Selects server-configured Attribute Stores for opted-in Attribute writes.
    ///
    /// Omitting this builder preserves the existing Flow setting. Passing an empty vector retains
    /// protocol presence and disables later synchronization. Each enabled Attribute write is
    /// projected to every selected store; an external store failure does not roll back the Flow
    /// Attribute write.
    #[must_use]
    pub fn attribute_store_names(mut self, values: Vec<String>) -> Self {
        self.attribute_store_names = Some(values);
        self
    }

    /// Sets the event-count threshold that triggers Continue-As-New.
    pub fn continue_as_new_threshold(mut self, value: u32) -> Self {
        self.continue_as_new_threshold = Some(value);
        self
    }

    /// Sets the maximum history page size, in bytes, carried into Continue-As-New.
    pub fn continue_as_new_page_size_bytes(mut self, value: u32) -> Self {
        self.continue_as_new_page_size_bytes = Some(value);
        self
    }

    /// Sets the default persistence durability for Step handler writes.
    pub fn step_durability(mut self, value: StepDurability) -> Self {
        self.step_durability = Some(value);
        self
    }

    /// Has no effect. The SDK does not send this value to the server.
    ///
    /// server-zd 0.1.23 and later ignore the field for runs that start with them. Such a run,
    /// when it offloads its history, always deletes the objects of expired history segments about
    /// one hour after the history window passes them.
    ///
    /// A run that started under server-zd 0.1.22 still follows the value it recorded then. When
    /// that value is false, the run's expired history objects stay in object storage until its
    /// retention ends, and a run that never closes keeps them. To delete them sooner, set the
    /// field to true for that run with UpdateFlowConfig through an SDK older than 1.40.0 or a
    /// direct gRPC request. See <https://docs.superdurable.io/reference/server/configuration#history-offload>.
    ///
    /// The method remains so existing code still compiles. Returns this config unchanged.
    ///
    /// Since 1.39.0.
    ///
    /// Deprecated in 1.40.0: the SDK no longer sends the value, and runs that start with
    /// server-zd 0.1.23 or later ignore it. Remove the call.
    #[deprecated(
        since = "1.40.0",
        note = "has no effect; the SDK no longer sends it, and runs started on server-zd 0.1.23 \
                or later ignore it"
    )]
    #[must_use]
    pub fn delete_expired_history_objects(self, _value: bool) -> Self {
        self
    }

    /// Sets how many of the newest history events the run keeps, approximately.
    ///
    /// The run uses the kept events for history reads and Time Travel. Without this builder, the
    /// run uses the server's `history.windowEvents` setting, which defaults to 10000. Raise it for
    /// a run that you inspect or travel back in often. Lower it for a run that writes many events
    /// and needs less disk.
    ///
    /// Dex prunes the oldest events in chunks, and only between commands, so a run keeps slightly
    /// more or fewer events than the value. A command that changes the Flow but records no event
    /// counts as one event. The first event of the run is always kept, so a reset to the beginning
    /// of the run always works. History reads no longer return pruned events, and Time Travel
    /// targets only commands inside the window.
    ///
    /// Set it at start through [`crate::StartFlowOptions::config_override`], or change it on an
    /// active Flow with [`crate::Client::update_flow_config`]. A smaller value takes effect at the
    /// run's next pruning. A larger value keeps more of the events recorded afterwards; pruned
    /// events never come back. Once set, the value can be changed but not cleared back to the
    /// server default: an update without this builder keeps the current value. A SubFlow inherits
    /// the value from its parent unless its own [`crate::SubFlowOptions::config_override`] sets it.
    ///
    /// The value must be positive and at most the server's `history.maxWindowEvents` setting,
    /// which defaults to 100000. StartFlow and UpdateFlowConfig return [`crate::SdkError::Service`]
    /// whose [`crate::ServiceError::code`] is `InvalidArgument` for zero or a value above that
    /// maximum. A value above `i32::MAX` fails locally with [`crate::SdkError::InvalidArgument`]
    /// before any request. A Step that starts a SubFlow with an invalid override is an invalid Worker
    /// response: the Step attempt fails and Dex retries it under the Step's retry policy. Servers
    /// older than server-zd 0.1.21 ignore the value.
    ///
    /// ```no_run
    /// # fn update(client: &dex_sdk::Client) -> dex_sdk::SdkResult<()> {
    /// client.update_flow_config(
    ///     "order-42",
    ///     dex_sdk::FlowConfig::new().history_window_events(50_000),
    /// )?;
    /// # Ok(())
    /// # }
    /// ```
    ///
    /// Since 1.38.0.
    #[must_use]
    pub fn history_window_events(mut self, value: u32) -> Self {
        self.history_window_events = Some(value);
        self
    }

    /// Sets whether Dex offloads the run's history to object storage.
    ///
    /// Without this builder, the run offloads its history, as if `true` were passed. Leave it unset
    /// for most runs.
    ///
    /// An offloaded run uploads its history to object storage in segment objects. Once a segment
    /// is uploaded and a grace period has passed, Dex deletes its history records and superseded
    /// values from the local disk. History reads then fetch them from object storage. When the
    /// history window passes a whole segment, Dex deletes the segment's object about one hour
    /// later.
    ///
    /// A run that does not offload keeps its history on the local disk. It never writes a history
    /// object. The history window still applies: Dex prunes the oldest events and deletes the
    /// superseded values that the window passed.
    ///
    /// A long-running run that offloads keeps only its newest history and current state on the
    /// local disk. Pass `false` for many short runs that close soon after they start. Each
    /// offloaded run writes at least one history object when it closes. It deletes that object
    /// when its retention ends. Short runs at a high rate therefore add object storage requests,
    /// while their small history costs little local disk. A run that does not offload keeps
    /// every superseded value its history window covers on the local disk, with no byte limit;
    /// for a run that rewrites large values, keep offload or set a smaller history window.
    ///
    /// Set it only at start, through [`crate::StartFlowOptions::config_override`], or for a
    /// SubFlow through [`crate::SubFlowOptions::config_override`]. A SubFlow inherits the value
    /// from its parent unless its own override sets it. The run keeps the value for its whole
    /// life. [`crate::Client::update_flow_config`] with this builder returns
    /// [`crate::SdkError::Service`] whose [`crate::ServiceError::code`] is `InvalidArgument`. Runs
    /// that started before server-zd 0.1.23 keep the history offload behavior that their server
    /// gave them. Servers older than server-zd 0.1.23 ignore the value.
    ///
    /// ```no_run
    /// # fn start<F: dex_sdk::Flow>(
    /// #     client: &dex_sdk::Client,
    /// #     notification_flow: &F,
    /// #     input: F::StartInput,
    /// # ) -> dex_sdk::SdkResult<String> {
    /// client.start_flow_with_options(
    ///     notification_flow,
    ///     "notification-42",
    ///     input,
    ///     dex_sdk::StartFlowOptions::new()
    ///         .config_override(dex_sdk::FlowConfig::new().offload_history(false)),
    /// )
    /// # }
    /// ```
    ///
    /// Since 1.40.0.
    #[must_use]
    pub fn offload_history(mut self, value: bool) -> Self {
        self.offload_history = Some(value);
        self
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
/// Controls which active Step types Dex indexes for Flow search.
pub enum ActiveStepSearchMode {
    /// Indexes every active Step type, including execute-only Steps.
    All,
    /// Indexes a Step type only after its `wait_for` handler runs.
    WithWaitFor,
    /// Disables active-Step indexing for the Flow.
    Disabled,
}
