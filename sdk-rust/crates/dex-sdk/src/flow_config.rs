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
    pub(crate) delete_expired_history_objects: Option<bool>,
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

    /// Controls deletion of object storage objects for expired history segments.
    ///
    /// `true` deletes each expired segment's object about one hour after expiry. `false` keeps
    /// objects until the run is deleted after retention. This only matters when history
    /// offload is enabled. Omission uses `history.deleteExpiredObjects` on the server: true
    /// for dev/local and false for serve. The run records that default at start.
    ///
    /// Set it through [`crate::StartFlowOptions::config_override`],
    /// update it with [`crate::Client::update_flow_config`], or override it for a SubFlow. Children otherwise inherit the parent's value.
    /// An omitted update preserves the current value; it cannot be cleared to the server
    /// default. Enabling deletion includes already expired objects. Disabling it stops
    /// deletions that have not started; requests already sent may complete, and deleted
    /// objects never return. History reads and Time Travel exclude expired history even
    /// when its objects are kept.
    ///
    /// ```no_run
    /// # fn update(client: &dex_sdk::Client) -> dex_sdk::SdkResult<()> {
    /// client.update_flow_config("order-42",
    ///     dex_sdk::FlowConfig::new().delete_expired_history_objects(false))?;
    /// # Ok(())
    /// # }
    /// ```
    ///
    /// Since 1.39.0.
    #[must_use]
    pub fn delete_expired_history_objects(mut self, value: bool) -> Self {
        self.delete_expired_history_objects = Some(value);
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
