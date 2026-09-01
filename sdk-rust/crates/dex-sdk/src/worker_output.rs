// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::pin::Pin;
use std::task::{Context, Poll};

use dex_protocol::dex::{
    InvokeExecuteMethodOutput, InvokeWaitForMethodOutput, StepMethodHeartbeat, StepStreamWrite,
    Value as ProtoValue, invoke_execute_method_output, invoke_wait_for_method_output,
};
use tokio::sync::mpsc;
use tokio::task::JoinHandle;
use tokio_stream::Stream;
use tonic::Status;

use crate::context::InvocationCancellation;
use crate::{HandlerError, HandlerResult};

const STEP_OUTPUT_CHANNEL_CAPACITY: usize = 1;

#[derive(Clone)]
pub(crate) enum StepOutputEmitter {
    WaitFor(mpsc::Sender<HandlerResult<InvokeWaitForMethodOutput>>),
    Execute(mpsc::Sender<HandlerResult<InvokeExecuteMethodOutput>>),
}

impl StepOutputEmitter {
    pub(crate) fn wait_for() -> (
        Self,
        mpsc::Receiver<HandlerResult<InvokeWaitForMethodOutput>>,
    ) {
        let (sender, receiver) = mpsc::channel(STEP_OUTPUT_CHANNEL_CAPACITY);
        (Self::WaitFor(sender), receiver)
    }

    pub(crate) fn execute() -> (
        Self,
        mpsc::Receiver<HandlerResult<InvokeExecuteMethodOutput>>,
    ) {
        let (sender, receiver) = mpsc::channel(STEP_OUTPUT_CHANNEL_CAPACITY);
        (Self::Execute(sender), receiver)
    }

    pub(crate) fn send_heartbeat(&self, value: Option<ProtoValue>) -> HandlerResult<()> {
        match self {
            Self::WaitFor(sender) => sender
                .blocking_send(Ok(InvokeWaitForMethodOutput {
                    output: Some(invoke_wait_for_method_output::Output::Heartbeat(
                        StepMethodHeartbeat { value },
                    )),
                }))
                .map_err(output_closed),
            Self::Execute(sender) => sender
                .blocking_send(Ok(InvokeExecuteMethodOutput {
                    output: Some(invoke_execute_method_output::Output::Heartbeat(
                        StepMethodHeartbeat { value },
                    )),
                }))
                .map_err(output_closed),
        }
    }

    pub(crate) fn send_stream_write(&self, write: StepStreamWrite) -> HandlerResult<()> {
        match self {
            Self::WaitFor(sender) => sender
                .blocking_send(Ok(InvokeWaitForMethodOutput {
                    output: Some(invoke_wait_for_method_output::Output::StreamWrite(write)),
                }))
                .map_err(output_closed),
            Self::Execute(sender) => sender
                .blocking_send(Ok(InvokeExecuteMethodOutput {
                    output: Some(invoke_execute_method_output::Output::StreamWrite(write)),
                }))
                .map_err(output_closed),
        }
    }

    pub(crate) async fn send_stream_write_async(
        &self,
        write: StepStreamWrite,
    ) -> HandlerResult<()> {
        match self {
            Self::WaitFor(sender) => sender
                .send(Ok(InvokeWaitForMethodOutput {
                    output: Some(invoke_wait_for_method_output::Output::StreamWrite(write)),
                }))
                .await
                .map_err(output_closed),
            Self::Execute(sender) => sender
                .send(Ok(InvokeExecuteMethodOutput {
                    output: Some(invoke_execute_method_output::Output::StreamWrite(write)),
                }))
                .await
                .map_err(output_closed),
        }
    }

    pub(crate) fn wait_for_sender(&self) -> mpsc::Sender<HandlerResult<InvokeWaitForMethodOutput>> {
        match self {
            Self::WaitFor(sender) => sender.clone(),
            Self::Execute(_) => panic!("WaitFor output emitter has the wrong method"),
        }
    }

    pub(crate) fn execute_sender(&self) -> mpsc::Sender<HandlerResult<InvokeExecuteMethodOutput>> {
        match self {
            Self::Execute(sender) => sender.clone(),
            Self::WaitFor(_) => panic!("Execute output emitter has the wrong method"),
        }
    }
}

pub(crate) struct WorkerInvocation<Output> {
    receiver: mpsc::Receiver<HandlerResult<Output>>,
    cancellation: InvocationCancellation,
    producer: JoinHandle<()>,
}

impl<Output> WorkerInvocation<Output> {
    pub(crate) fn new(
        receiver: mpsc::Receiver<HandlerResult<Output>>,
        cancellation: InvocationCancellation,
        producer: JoinHandle<()>,
    ) -> Self {
        Self {
            receiver,
            cancellation,
            producer,
        }
    }

    pub(crate) fn into_stream(
        self,
        map_error: fn(HandlerError) -> Status,
    ) -> WorkerResponseStream<Output> {
        WorkerResponseStream {
            receiver: self.receiver,
            cancellation: self.cancellation,
            producer: self.producer,
            map_error,
        }
    }
}

pub(crate) struct WorkerResponseStream<Output> {
    receiver: mpsc::Receiver<HandlerResult<Output>>,
    cancellation: InvocationCancellation,
    producer: JoinHandle<()>,
    map_error: fn(HandlerError) -> Status,
}

impl<Output> Stream for WorkerResponseStream<Output> {
    type Item = Result<Output, Status>;

    fn poll_next(mut self: Pin<&mut Self>, context: &mut Context<'_>) -> Poll<Option<Self::Item>> {
        let map_error = self.map_error;
        Pin::new(&mut self.receiver)
            .poll_recv(context)
            .map(|output| output.map(|result| result.map_err(map_error)))
    }
}

impl<Output> Drop for WorkerResponseStream<Output> {
    fn drop(&mut self) {
        self.cancellation.cancel();
        self.producer.abort();
    }
}

fn output_closed<T>(_: mpsc::error::SendError<T>) -> HandlerError {
    HandlerError::new("dex_sdk::HandlerError", "Step output stream is closed")
}

#[cfg(test)]
mod tests {
    use std::sync::mpsc as std_mpsc;
    use std::time::Duration;

    use super::*;

    #[tokio::test(flavor = "multi_thread")]
    async fn single_slot_channel_applies_backpressure() {
        let (emitter, mut receiver) = StepOutputEmitter::wait_for();
        let (sent, observed) = std_mpsc::channel();
        let producer = std::thread::spawn(move || {
            emitter.send_heartbeat(None).expect("send first frame");
            sent.send(1).expect("report first frame");
            emitter.send_heartbeat(None).expect("send second frame");
            sent.send(2).expect("report second frame");
        });

        assert_eq!(1, observed.recv().expect("observe first frame"));
        assert!(observed.recv_timeout(Duration::from_millis(50)).is_err());
        receiver
            .recv()
            .await
            .expect("receive first frame")
            .expect("first frame succeeds");
        assert_eq!(2, observed.recv().expect("observe second frame"));
        receiver
            .recv()
            .await
            .expect("receive second frame")
            .expect("second frame succeeds");
        producer.join().expect("join producer");
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn dropped_stream_cancels_and_unblocks_sender() {
        let (emitter, receiver) = StepOutputEmitter::wait_for();
        let (sent, observed) = std_mpsc::channel();
        let sender = std::thread::spawn(move || {
            emitter.send_heartbeat(None).expect("send first frame");
            sent.send(()).expect("report first frame");
            emitter.send_heartbeat(None)
        });
        observed.recv().expect("observe first frame");

        let cancellation = InvocationCancellation::new();
        let producer = tokio::spawn(std::future::pending());
        let stream = WorkerInvocation::new(receiver, cancellation.clone(), producer)
            .into_stream(|error| Status::unknown(error.to_string()));
        drop(stream);

        assert!(cancellation.is_cancelled());
        assert!(sender.join().expect("join sender").is_err());
    }
}
