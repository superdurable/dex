// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::atomic::AtomicI32;
use std::sync::{Arc, Mutex};

use dex_protocol::dex::poll_worker_task_frame::Output;
use dex_protocol::dex::poll_worker_task_request::Frame as RequestFrame;
use dex_protocol::dex::{PollWorkerTaskFrame, PollWorkerTaskRequest};
use tokio::sync::mpsc;
use tonic::Status;

use crate::context::InvocationCancellation;

pub(crate) struct TaskFrameSender {
    task_id: String,
    sender: mpsc::Sender<PollWorkerTaskRequest>,
    transport_cancellation: InvocationCancellation,
    progress_cancellation: InvocationCancellation,
    cancel_code: Arc<AtomicI32>,
    final_frame_sent: Arc<Mutex<bool>>,
}

impl TaskFrameSender {
    pub(crate) fn new(
        task_id: String,
        sender: mpsc::Sender<PollWorkerTaskRequest>,
        transport_cancellation: InvocationCancellation,
        progress_cancellation: InvocationCancellation,
        cancel_code: Arc<AtomicI32>,
        final_frame_sent: Arc<Mutex<bool>>,
    ) -> Self {
        Self {
            task_id,
            sender,
            transport_cancellation,
            progress_cancellation,
            cancel_code,
            final_frame_sent,
        }
    }

    pub(crate) async fn send(&self, output: Output) -> Result<(), Status> {
        let is_final_frame = matches!(&output, Output::Rpc(_) | Output::WorkerError(_))
            || matches!(&output, Output::WaitFor(value) if matches!(value.output.as_ref(), Some(dex_protocol::dex::invoke_wait_for_method_output::Output::Result(_))))
            || matches!(&output, Output::Execute(value) if matches!(value.output.as_ref(), Some(dex_protocol::dex::invoke_execute_method_output::Output::Result(_))));
        if *self.final_frame_sent.lock().expect("final frame lock") {
            return Err(Status::cancelled("Worker task is no longer active"));
        }
        let permit = tokio::select! {
            biased;
            () = self.transport_cancellation.cancelled() => return Err(Status::cancelled("Worker task transport was cancelled")),
            () = self.progress_cancellation.cancelled(), if !is_final_frame => return Err(Status::cancelled("Worker task was cancelled")),
            result = self.sender.reserve() => result.map_err(|error| Status::unavailable(format!("Worker task stream cannot send task frame: {error}")))?,
        };
        let mut is_final_frame_sent = self.final_frame_sent.lock().expect("final frame lock");
        if *is_final_frame_sent
            || self.transport_cancellation.is_cancelled()
            || (!is_final_frame && self.progress_cancellation.is_cancelled())
        {
            return Err(Status::cancelled("Worker task is no longer active"));
        }
        let output = match output {
            Output::WorkerError(error) => {
                Output::WorkerError(super::with_cancel_status(error, &self.cancel_code))
            }
            _ => output,
        };
        // Set the flag before enqueueing, so a later keepalive Pong never follows the final frame.
        *is_final_frame_sent = is_final_frame;
        permit.send(PollWorkerTaskRequest {
            frame: Some(RequestFrame::TaskFrame(PollWorkerTaskFrame {
                task_id: self.task_id.clone(),
                output: Some(output),
            })),
        });
        Ok(())
    }
}
