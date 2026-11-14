// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use dex_sdk::{
    Context, Flow, HandlerError, HandlerResult, Step, StepDecision, StepList, SubFlow,
    SubFlowOptions, Wait,
};

/// Completes with the label of the Worker that ran its Step.
pub(crate) struct TaskQueueLabelFlow {
    start: TaskQueueLabelStep,
}

impl TaskQueueLabelFlow {
    pub(crate) fn new(worker_label: &str) -> Self {
        Self {
            start: TaskQueueLabelStep {
                worker_label: worker_label.to_string(),
            },
        }
    }
}

impl Flow for TaskQueueLabelFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }
}

struct TaskQueueLabelStep {
    worker_label: String,
}

impl Step for TaskQueueLabelStep {
    type Input = ();

    fn execute(&self, _context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(self.worker_label.clone()))
    }
}

/// Runs a [`TaskQueueLabelFlow`] SubFlow on the task queue named by its input.
///
/// Completes with `{parent Worker label}>{SubFlow Worker label}`.
pub(crate) struct TaskQueueParentFlow {
    start: TaskQueueParentStep,
}

impl TaskQueueParentFlow {
    pub(crate) fn new(worker_label: &str) -> Self {
        Self {
            start: TaskQueueParentStep {
                worker_label: worker_label.to_string(),
            },
        }
    }
}

impl Flow for TaskQueueParentFlow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }
}

struct TaskQueueParentStep {
    worker_label: String,
}

impl Step for TaskQueueParentStep {
    type Input = String;

    fn wait_for(&self, _context: &mut Context, sub_flow_task_queue: String) -> HandlerResult<Wait> {
        let child = SubFlow::run_with_options(
            &TaskQueueLabelFlow::new(&self.worker_label),
            (),
            SubFlowOptions::new().task_queue(sub_flow_task_queue),
        )
        .map_err(|error| HandlerError::new("SubFlowError", error.to_string()))?;
        Ok(Wait::until(child))
    }

    fn execute(&self, context: &mut Context, _input: String) -> HandlerResult<StepDecision> {
        let child_label = SubFlow::condition_result(context)?
            .single_output::<String>()
            .map_err(|error| HandlerError::new("SubFlowError", error.to_string()))?;
        Ok(StepDecision::graceful_complete(format!(
            "{}>{child_label}",
            self.worker_label
        )))
    }
}
