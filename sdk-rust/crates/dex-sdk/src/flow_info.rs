// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeMap;
use std::time::SystemTime;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
/// Describes the current or terminal state of a Flow run.
pub enum FlowStatus {
    /// The Flow can still execute Steps or receive RPCs and Channel messages.
    Running,
    /// The Flow completed successfully.
    Completed,
    /// The Flow ended because a Step, RPC, or user-code failure was not recovered.
    Failed,
    /// Reserved for backend hard-timeout reporting. Applications must not depend on this status.
    ServerSideTimeoutInternalOnly,
    /// An operator terminated the Flow immediately.
    Terminated,
    /// The Flow completed cooperative cancellation.
    Canceled,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
#[non_exhaustive]
/// Categorizes the failure recorded for an uncompleted Flow.
///
/// Dex may add categories in later releases, so a `match` needs a wildcard arm. A category that
/// this SDK version does not recognize reads as `None` from [`crate::FlowResult::error_type`].
///
/// Changed in 1.45.2: the enum is `#[non_exhaustive]`.
pub enum FlowErrorType {
    /// A Step returned an invalid or failing decision.
    StepDecisionFailed,
    /// A client API operation failed the Flow.
    ClientApiFailed,
    /// A Worker Step or RPC handler failed.
    WorkerApiFailed,
    /// The registered Flow or Step definition violated an SDK contract.
    InvalidUserFlowCode,
    /// Dex encountered an internal failure.
    Internal,
    /// A Dex soft Flow timeout expired under the fail policy.
    FlowTimeout,
    /// The parent Flow stopped waiting for this SubFlow child.
    ///
    /// A Time Travel restored a parent Step that waited on a SubFlow condition, and its SubFlow
    /// reset policy chose to fail that condition. Dex terminates the child run if it still runs.
    /// The condition then completes with a [`FlowStatus::Failed`] result that carries this error
    /// type. Only a SubFlow result read in Execute carries it; the child run itself never closes
    /// with it.
    ///
    /// Handle it like any other failed child: retry, compensate, or fail the parent. The child's
    /// output is not available.
    ///
    /// ```ignore
    /// let result = SubFlow::condition_result(context)?;
    /// if result.error_type() == Some(FlowErrorType::SubFlowAbandoned) {
    ///     return Err(HandlerError::new("ChildAbandoned", "child abandoned by a Time Travel"));
    /// }
    /// ```
    ///
    /// Since 1.45.2.
    SubFlowAbandoned,
}

#[derive(Clone, Debug, Eq, PartialEq)]
/// Summarizes one Flow run returned by [`crate::Client::describe_flow`].
pub struct FlowInfo {
    /// Application-assigned Flow ID.
    pub flow_id: String,
    /// Server-assigned run ID for the described execution.
    pub run_id: String,
    /// Registered Flow type name.
    pub flow_type: String,
    /// Current or terminal status.
    pub status: FlowStatus,
    /// Server-recorded start time.
    pub started_at: SystemTime,
}

#[derive(Clone, Debug, PartialEq)]
/// Contains one Flow row returned by a search query.
pub struct SearchFlowEntry {
    /// Application-assigned Flow ID.
    pub flow_id: String,
    /// Server-assigned run ID.
    pub run_id: String,
    /// Registered Flow type name.
    pub flow_type: String,
    /// Status captured by the search index.
    pub status: FlowStatus,
    /// Indexed start time, or `None` when unavailable.
    pub started_at: Option<SystemTime>,
    /// Indexed close time, or `None` while open or unavailable.
    pub closed_at: Option<SystemTime>,
    /// Values keyed by their physical Indexed Attribute names.
    pub indexed_attributes: BTreeMap<String, serde_json::Value>,
}

#[derive(Clone, Debug, PartialEq)]
/// Contains one page of [`crate::Client::search_flows`] results.
pub struct SearchFlowsPage {
    /// Flow rows in server-defined query order.
    pub flows: Vec<SearchFlowEntry>,
    /// Opaque token for [`crate::Client::search_flows_page`], or empty on the last page.
    pub next_page_token: String,
}
