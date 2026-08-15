// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use dex_protocol::dex::{FlowResult as ProtoFlowResult, Value as ProtoValue};

use crate::{FlowErrorType, FlowStatus, SdkError, SdkResult, Value, value_mapper};

#[derive(Clone, Debug)]
/// Contains one output-bearing Step completion returned by [`crate::Client::wait_for_flow`].
pub struct StepCompletion {
    /// Registered Step type that produced this output.
    pub step_type: String,
    /// Exact server Step execution identity that produced this output.
    pub step_execution_id: String,
    output: ProtoValue,
}

impl StepCompletion {
    pub(crate) fn new(step_type: String, step_execution_id: String, output: ProtoValue) -> Self {
        Self {
            step_type,
            step_execution_id,
            output,
        }
    }

    /// Decodes this already hydrated Step output as `Output`.
    ///
    /// # Errors
    ///
    /// Returns [`SdkError::ValueMapping`] when the wire value is incompatible with `Output`.
    pub fn decode<Output: Value>(&self) -> SdkResult<Output> {
        value_mapper::decode(&self.output)
    }
}

#[derive(Clone, Debug)]
/// Describes an observed Flow status and its output-bearing completions.
///
/// Client waits return terminal results. A SubFlow AnyOf loser can return a Running snapshot,
/// which does not guarantee that the independently running Flow remains active at read time.
pub struct FlowResult {
    status: FlowStatus,
    error_type: Option<FlowErrorType>,
    error_message: Option<String>,
    completions: Vec<StepCompletion>,
}

impl FlowResult {
    pub(crate) fn new(
        status: FlowStatus,
        error_type: Option<FlowErrorType>,
        error_message: Option<String>,
        completions: Vec<StepCompletion>,
    ) -> Self {
        Self {
            status,
            error_type,
            error_message,
            completions,
        }
    }

    pub(crate) fn from_proto(result: &ProtoFlowResult) -> SdkResult<Self> {
        let status = crate::client::map_flow_status(result.flow_status)?;
        let completions = result
            .results
            .iter()
            .map(|completion| {
                Ok(StepCompletion::new(
                    completion.completed_step_type.clone(),
                    completion.completed_step_execution_id.clone(),
                    completion.completed_step_output.clone().ok_or_else(|| {
                        SdkError::ValueMapping {
                            message: "Step completion output is required".to_string(),
                        }
                    })?,
                ))
            })
            .collect::<SdkResult<Vec<_>>>()?;
        Ok(Self::new(
            status,
            crate::client::map_flow_error_type(result.error_type),
            (!result.error_message.is_empty()).then(|| result.error_message.clone()),
            completions,
        ))
    }

    /// Returns the observed Flow lifecycle state.
    pub fn status(&self) -> FlowStatus {
        self.status
    }

    /// Returns the Dex failure category when one was reported.
    pub fn error_type(&self) -> Option<FlowErrorType> {
        self.error_type
    }

    /// Returns the server failure detail when one was reported.
    pub fn error_message(&self) -> Option<&str> {
        self.error_message.as_deref()
    }

    /// Returns whether the observed run can no longer execute.
    pub fn is_terminal(&self) -> bool {
        !matches!(
            self.status,
            FlowStatus::Running | FlowStatus::ContinuedAsNew
        )
    }

    /// Returns output-bearing completions in server collection order.
    ///
    /// Parallel Step order is not deterministic. Select by Step type or Step execution ID.
    pub fn completions(&self) -> &[StepCompletion] {
        &self.completions
    }

    /// Decodes the output when exactly one completion exists.
    ///
    /// # Errors
    ///
    /// Returns [`SdkError::InvalidArgument`] for a nonterminal result or zero or multiple outputs and
    /// [`SdkError::ValueMapping`] when the output is incompatible with `Output`.
    pub fn single_output<Output: Value>(&self) -> SdkResult<Output> {
        if !self.is_terminal() {
            return Err(SdkError::InvalidArgument {
                message: "Flow result is not terminal".to_string(),
            });
        }
        if self.completions().len() != 1 {
            return Err(SdkError::InvalidArgument {
                message: format!(
                    "expected exactly one Step output, found {}",
                    self.completions().len()
                ),
            });
        }
        self.completions[0].decode()
    }
}

#[cfg(test)]
mod tests {
    use dex_protocol::dex::value;

    use super::*;

    #[test]
    fn preserves_heterogeneous_completions_and_rejects_ambiguous_single_output() {
        let result = FlowResult::new(
            crate::FlowStatus::Completed,
            None,
            None,
            vec![
                completion(
                    "First",
                    "First-1",
                    value::Kind::StringValue("one".to_string()),
                ),
                completion("Second", "Second-2", value::Kind::BoolValue(true)),
            ],
        );

        assert_eq!(result.completions()[0].step_type, "First");
        assert_eq!(result.completions()[1].step_execution_id, "Second-2");
        assert_eq!(result.completions()[0].decode::<String>().unwrap(), "one");
        assert!(result.completions()[1].decode::<bool>().unwrap());
        assert!(matches!(
            result.single_output::<String>(),
            Err(SdkError::InvalidArgument { .. })
        ));
    }

    #[test]
    fn single_output_requires_exactly_one_completion() {
        let single = FlowResult::new(
            crate::FlowStatus::Completed,
            None,
            None,
            vec![completion("Only", "Only-1", value::Kind::IntValue(7))],
        );
        assert_eq!(single.single_output::<i64>().unwrap(), 7);

        let empty = FlowResult::new(crate::FlowStatus::Completed, None, None, Vec::new());
        assert!(matches!(
            empty.single_output::<String>(),
            Err(SdkError::InvalidArgument { .. })
        ));
    }

    fn completion(step_type: &str, step_execution_id: &str, kind: value::Kind) -> StepCompletion {
        StepCompletion::new(
            step_type.to_string(),
            step_execution_id.to_string(),
            ProtoValue { kind: Some(kind) },
        )
    }
}
