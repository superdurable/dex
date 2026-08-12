// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use dex_protocol::dex::Value as ProtoValue;

use crate::{SdkError, SdkResult, Value, value_mapper};

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
/// Contains every output-bearing completion from a successfully completed Flow.
pub struct WaitForFlowResult {
    completions: Vec<StepCompletion>,
}

impl WaitForFlowResult {
    pub(crate) fn new(completions: Vec<StepCompletion>) -> Self {
        Self { completions }
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
    /// Returns [`SdkError::InvalidArgument`] for zero or multiple outputs and
    /// [`SdkError::ValueMapping`] when the output is incompatible with `Output`.
    pub fn single_output<Output: Value>(&self) -> SdkResult<Output> {
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
        let result = WaitForFlowResult::new(vec![
            completion(
                "First",
                "First-1",
                value::Kind::StringValue("one".to_string()),
            ),
            completion("Second", "Second-2", value::Kind::BoolValue(true)),
        ]);

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
        let single =
            WaitForFlowResult::new(vec![completion("Only", "Only-1", value::Kind::IntValue(7))]);
        assert_eq!(single.single_output::<i64>().unwrap(), 7);

        let empty = WaitForFlowResult::new(Vec::new());
        assert!(matches!(
            empty.single_output::<String>(),
            Err(SdkError::InvalidArgument { .. })
        ));
    }

    #[test]
    fn uncompleted_error_retains_completion_identity_and_output() {
        let error = SdkError::FlowUncompleted {
            run_id: "run-failed".to_string(),
            status: crate::FlowStatus::Failed,
            error_type: None,
            message: Some("failed by test".to_string()),
            completions: vec![completion(
                "Partial",
                "Partial-3",
                value::Kind::StringValue("partial".to_string()),
            )],
        };

        let SdkError::FlowUncompleted { completions, .. } = error else {
            panic!("expected FlowUncompleted");
        };
        assert_eq!(completions[0].step_type, "Partial");
        assert_eq!(completions[0].step_execution_id, "Partial-3");
        assert_eq!(completions[0].decode::<String>().unwrap(), "partial");
    }

    fn completion(step_type: &str, step_execution_id: &str, kind: value::Kind) -> StepCompletion {
        StepCompletion::new(
            step_type.to_string(),
            step_execution_id.to_string(),
            ProtoValue { kind: Some(kind) },
        )
    }
}
