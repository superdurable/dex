// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Classifies Worker task frames and acknowledges an attempt-started mark.

use dexzd_protocol::dex::{
    self, PollWorkerTaskAttemptStartedAck, PollWorkerTaskFrame, PollWorkerTaskResponse,
    poll_worker_task_frame, poll_worker_task_response,
};
use tokio::sync::mpsc;
use tonic::Status;

/// Sends `PollWorkerTaskAttemptStartedAck` to the Worker that marked its attempt started.
///
/// The receiver commits the mark first; dropping the value without acknowledging leaves the
/// Worker waiting until its task ends.
#[derive(Debug)]
pub struct AttemptStartedAck {
    output: mpsc::UnboundedSender<Result<PollWorkerTaskResponse, Status>>,
    task_id: String,
}

impl AttemptStartedAck {
    pub(super) fn new(
        output: mpsc::UnboundedSender<Result<PollWorkerTaskResponse, Status>>,
        task_id: String,
    ) -> Self {
        Self { output, task_id }
    }

    /// Sends the acknowledgement on the Worker's stream.
    ///
    /// Returns false when the Worker stream is already closed; the Worker then never starts
    /// the attempt's external effect.
    pub fn acknowledge(self) -> bool {
        self.output
            .send(Ok(PollWorkerTaskResponse {
                frame: Some(poll_worker_task_response::Frame::AttemptStartedAck(
                    PollWorkerTaskAttemptStartedAck {
                        task_id: self.task_id,
                    },
                )),
            }))
            .is_ok()
    }
}

/// Returns true for a WaitFor or Execute output that marks the attempt started.
pub(super) fn is_attempt_started(frame: &PollWorkerTaskFrame) -> bool {
    match &frame.output {
        Some(poll_worker_task_frame::Output::WaitFor(output)) => matches!(
            output.output,
            Some(dex::invoke_wait_for_method_output::Output::AttemptStarted(
                _
            ))
        ),
        Some(poll_worker_task_frame::Output::Execute(output)) => matches!(
            output.output,
            Some(dex::invoke_execute_method_output::Output::AttemptStarted(_))
        ),
        _ => false,
    }
}

/// Returns true for a frame that ends the task: a result, an RPC response, or a Worker error.
pub(super) fn is_final(frame: &PollWorkerTaskFrame) -> bool {
    match &frame.output {
        Some(
            poll_worker_task_frame::Output::WorkerError(_) | poll_worker_task_frame::Output::Rpc(_),
        ) => true,
        Some(poll_worker_task_frame::Output::WaitFor(output)) => matches!(
            output.output,
            None | Some(dex::invoke_wait_for_method_output::Output::Result(_))
        ),
        Some(poll_worker_task_frame::Output::Execute(output)) => matches!(
            output.output,
            None | Some(dex::invoke_execute_method_output::Output::Result(_))
        ),
        None => false,
    }
}
