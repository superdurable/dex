// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dex_protocol::dex::{StopFlowRequest, StopType as ProtoStopType};

use crate::client::invalid;
use crate::{SdkError, SdkResult, Value, value_mapper};

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum StopType {
    Cancel,
    Terminate,
    Fail,
    GracefulComplete,
    ForceComplete,
}

/// Holds an output encoded when [`StopFlowOptions::output`] runs, or its encoding failure.
#[derive(Clone, Debug, Eq, PartialEq)]
pub(crate) enum StopFlowOutput {
    Encoded(serde_json::Value),
    EncodingFailed(String),
}

#[derive(Clone, Debug, Eq, PartialEq)]
/// Selects how [`crate::Client::stop_flow`] ends an active Flow.
///
/// Construct a value with [`Self::cancel`], [`Self::terminate`], [`Self::fail`],
/// [`Self::graceful_complete`], or [`Self::force_complete`], then optionally attach an
/// operator-facing reason or, for the complete stop types, a Flow result output.
pub struct StopFlowOptions {
    pub(crate) stop_type: StopType,
    pub(crate) reason: Option<String>,
    pub(crate) output: Option<StopFlowOutput>,
}

impl StopFlowOptions {
    fn new(stop_type: StopType) -> Self {
        Self {
            stop_type,
            reason: None,
            output: None,
        }
    }

    /// Requests cooperative cancellation so application cleanup can run.
    pub fn cancel() -> Self {
        Self::new(StopType::Cancel)
    }

    /// Terminates the Flow immediately without cooperative cleanup.
    pub fn terminate() -> Self {
        Self::new(StopType::Terminate)
    }

    /// Marks the Flow failed with the reason set by [`Self::reason`].
    pub fn fail() -> Self {
        Self::new(StopType::Fail)
    }

    /// Completes the Flow once no Step is active, like a Step that returns
    /// [`crate::StepDecision::graceful_complete`].
    ///
    /// Use it to end a Flow successfully from outside its Steps, for example from a Step that ends
    /// its own Flow through a [`crate::Client`]. Active Steps keep running. Their decisions still
    /// apply, so a Step that moves to another Step keeps the Flow running. The Flow closes as
    /// completed when the last active Step finishes, or at once when no Step is active. The value
    /// set by [`Self::output`] becomes one of the Flow results. A Step waiting for Channel messages
    /// or a Timer stays active until its wait ends.
    ///
    /// A later stop of any type still applies while the Flow waits. Another graceful completion
    /// adds its output to the results. The server ignores the reason.
    ///
    /// It requires Dex Server 0.1.34 or newer. Older servers reject it with an invalid argument
    /// error.
    ///
    /// ```no_run
    /// # use dex_sdk::{Client, SdkResult, StopFlowOptions};
    /// # fn end_session(client: &Client) -> SdkResult<()> {
    /// client.stop_flow(
    ///     "session-42",
    ///     StopFlowOptions::graceful_complete().output("user ended the session".to_string()),
    /// )?;
    /// # Ok(())
    /// # }
    /// ```
    ///
    /// Since 1.45.3.
    pub fn graceful_complete() -> Self {
        Self::new(StopType::GracefulComplete)
    }

    /// Completes the Flow at once, like a Step that returns
    /// [`crate::StepDecision::force_complete`].
    ///
    /// Use it to end a Flow successfully without waiting for its Steps. The server cancels every
    /// active Step and discards their results. Results that Steps already returned through graceful
    /// completion stay, and the value set by [`Self::output`] is added after them. The Flow closes
    /// as completed. Like the Step decision, the close waits until pending Attribute Sync writes and
    /// transactional RPCs finish. The server ignores the reason.
    ///
    /// It requires Dex Server 0.1.34 or newer. Older servers reject it with an invalid argument
    /// error.
    ///
    /// ```no_run
    /// # use dex_sdk::{Client, SdkResult, StopFlowOptions};
    /// # fn close_session(client: &Client) -> SdkResult<()> {
    /// client.stop_flow(
    ///     "session-42",
    ///     StopFlowOptions::force_complete().output("closed by operator".to_string()),
    /// )?;
    /// # Ok(())
    /// # }
    /// ```
    ///
    /// Since 1.45.3.
    pub fn force_complete() -> Self {
        Self::new(StopType::ForceComplete)
    }

    /// Attaches a reason to the stop operation.
    ///
    /// [`Self::terminate`] keeps it in the Flow closed history event, and [`Self::fail`] uses it as
    /// the Flow error message. The other stop types ignore it.
    pub fn reason(mut self, value: impl Into<String>) -> Self {
        self.reason = Some(value.into());
        self
    }

    /// Attaches a Flow result output for [`Self::graceful_complete`] or [`Self::force_complete`].
    ///
    /// The SDK encodes it like a Step decision output, so [`crate::Client::wait_for_flow`] returns
    /// it as a Step completion whose Step type and Step execution ID are empty. The server rejects
    /// an output that exceeds its maximum value size.
    ///
    /// The SDK encodes the value when this method runs. [`crate::Client::stop_flow`] returns
    /// [`crate::SdkError::ValueMapping`] when encoding failed, and
    /// [`crate::SdkError::InvalidArgument`] when the stop type does not complete the Flow. In both
    /// cases it sends no request.
    ///
    /// ```no_run
    /// # use dex_sdk::{Client, SdkResult, StopFlowOptions};
    /// # use serde::{Deserialize, Serialize};
    /// #[derive(Deserialize, Serialize)]
    /// struct SessionSummary {
    ///     summary: String,
    /// }
    ///
    /// # fn end_session(client: &Client) -> SdkResult<()> {
    /// let options = StopFlowOptions::graceful_complete().output(SessionSummary {
    ///     summary: "user ended the session".to_string(),
    /// });
    /// client.stop_flow("session-42", options)?;
    /// # Ok(())
    /// # }
    /// ```
    ///
    /// Since 1.45.3.
    pub fn output<Output: Value>(mut self, value: Output) -> Self {
        self.output = Some(match serde_json::to_value(&value) {
            Ok(json) => StopFlowOutput::Encoded(json),
            Err(error) => StopFlowOutput::EncodingFailed(error.to_string()),
        });
        self
    }

    pub(crate) fn into_request(self, flow_id: &str) -> SdkResult<StopFlowRequest> {
        let stop_type = match self.stop_type {
            StopType::Cancel => ProtoStopType::Cancel,
            StopType::Terminate => ProtoStopType::Terminate,
            StopType::Fail => ProtoStopType::Fail,
            StopType::GracefulComplete => ProtoStopType::GracefulComplete,
            StopType::ForceComplete => ProtoStopType::ForceComplete,
        };
        let output = match self.output {
            None => None,
            Some(_) if !self.stop_type.is_complete() => {
                return Err(invalid(
                    "StopFlowOptions::output requires graceful_complete or force_complete",
                ));
            }
            Some(StopFlowOutput::Encoded(json)) => Some(value_mapper::encode_json(json)?),
            Some(StopFlowOutput::EncodingFailed(message)) => {
                return Err(SdkError::ValueMapping { message });
            }
        };
        Ok(StopFlowRequest {
            flow_id: flow_id.to_string(),
            run_id: String::new(),
            reason: self.reason.unwrap_or_default(),
            stop_type: stop_type as i32,
            output,
        })
    }
}

impl StopType {
    fn is_complete(self) -> bool {
        matches!(self, Self::GracefulComplete | Self::ForceComplete)
    }
}
