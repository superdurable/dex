// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use super::*;
use dex_protocol::dex::{
    InvokeExecuteMethodOutput, InvokeExecuteMethodRequest, InvokeExecuteMethodResponse,
    InvokeWaitForMethodOutput, InvokeWaitForMethodResponse, StepMethodHeartbeat, StepStreamWrite,
    invoke_execute_method_output, invoke_wait_for_method_output,
};

#[tokio::test]
async fn cancelled_task_rejects_late_step_output_but_sends_cancellation_error() {
    for is_execute in [false, true] {
        let (sender, mut receiver) = mpsc::channel(8);
        let progress_cancellation = InvocationCancellation::new();
        let frame_sender = frame_sender("cancelled", sender, progress_cancellation.clone());
        progress_cancellation.cancel();
        for output in step_progress(is_execute) {
            assert_eq!(
                Code::Cancelled,
                frame_sender.send(output).await.unwrap_err().code()
            );
        }
        assert!(receiver.try_recv().is_err());
        frame_sender
            .send(Output::WorkerError(WorkerErrorResponse {
                grpc_status: Code::Cancelled as i32,
                ..Default::default()
            }))
            .await
            .expect("send cancellation error");
        let frame = receive_frame(&mut receiver).await;
        assert_eq!(frame.task_id, "cancelled");
        assert!(
            matches!(frame.output, Some(Output::WorkerError(error)) if error.grpc_status == Code::Cancelled as i32)
        );
        assert!(receiver.try_recv().is_err());
    }
}

#[tokio::test]
async fn cancellation_unblocks_step_output_waiting_for_outbound_capacity() {
    for is_execute in [false, true] {
        for output in step_progress(is_execute) {
            let (sender, _receiver) = mpsc::channel(1);
            sender
                .try_send(PollWorkerTaskRequest::default())
                .expect("fill outbound stream");
            let progress_cancellation = InvocationCancellation::new();
            let frame_sender = frame_sender("cancelled", sender, progress_cancellation.clone());
            let mut sending = Box::pin(frame_sender.send(output));
            assert!(
                std::future::poll_fn(|context| {
                    std::task::Poll::Ready(sending.as_mut().poll(context).is_pending())
                })
                .await
            );
            progress_cancellation.cancel();
            assert_eq!(
                Code::Cancelled,
                timeout(Duration::from_secs(1), sending)
                    .await
                    .expect("send stopped")
                    .unwrap_err()
                    .code()
            );
        }
    }
}

#[tokio::test]
async fn completed_task_rejects_late_step_output_even_with_a_full_outbound_stream() {
    for is_execute in [false, true] {
        let (sender, mut receiver) = mpsc::channel(1);
        let frame_sender = frame_sender("completed", sender, InvocationCancellation::new());
        frame_sender
            .send(step_result(is_execute))
            .await
            .expect("send result");
        for output in step_progress(is_execute) {
            assert_eq!(
                Code::Cancelled,
                timeout(Duration::from_secs(1), frame_sender.send(output))
                    .await
                    .expect("late output stopped")
                    .unwrap_err()
                    .code()
            );
        }
        assert_eq!(receive_frame(&mut receiver).await.task_id, "completed");
        assert!(receiver.try_recv().is_err());
    }
}

#[tokio::test]
async fn reused_stream_rejects_previous_task_output_and_keeps_next_task_output() {
    for is_execute in [false, true] {
        for is_cancelled in [false, true] {
            let (sender, mut receiver) = mpsc::channel(8);
            let progress_cancellation = InvocationCancellation::new();
            let previous = frame_sender("previous", sender.clone(), progress_cancellation.clone());
            if is_cancelled {
                progress_cancellation.cancel();
                previous
                    .send(Output::WorkerError(WorkerErrorResponse::default()))
                    .await
                    .expect("send cancellation error");
            } else {
                previous
                    .send(step_result(is_execute))
                    .await
                    .expect("send result");
            }
            assert_eq!(receive_frame(&mut receiver).await.task_id, "previous");
            let next = frame_sender("next", sender, InvocationCancellation::new());
            for output in step_progress(is_execute) {
                assert_eq!(
                    Code::Cancelled,
                    previous.send(output).await.unwrap_err().code()
                );
            }
            for output in step_progress(is_execute) {
                next.send(output.clone()).await.expect("send next progress");
                let frame = receive_frame(&mut receiver).await;
                assert_eq!(frame.task_id, "next");
                assert_eq!(frame.output, Some(output));
            }
            next.send(step_result(is_execute))
                .await
                .expect("send next result");
            assert_eq!(receive_frame(&mut receiver).await.task_id, "next");
            assert!(receiver.try_recv().is_err());
        }
    }
}

struct ExecuteOutputOrderFlow {
    step: ExecuteOutputOrderStep,
}

struct ExecuteOutputOrderStep {
    started: Arc<Notify>,
    should_wait_for_cancellation: bool,
}

impl Flow for ExecuteOutputOrderFlow {
    type StartInput = ();

    fn steps(&self) -> crate::StepList<'_, ()> {
        crate::StepList::start(&self.step)
    }
}

impl crate::Step for ExecuteOutputOrderStep {
    type Input = ();

    fn execute(&self, context: &mut Context, _: ()) -> HandlerResult<crate::StepDecision> {
        context.record_heartbeat()?;
        self.started.notify_one();
        if self.should_wait_for_cancellation {
            context.wait_for_cancellation();
        }
        Ok(crate::StepDecision::graceful_complete(()))
    }
}

#[tokio::test(flavor = "multi_thread")]
async fn execute_output_precedes_result_after_handler_completion() {
    execute_output_after_handler_completion(None).await;
}

#[tokio::test(flavor = "multi_thread")]
async fn cancelled_execute_discards_queued_progress_but_delivers_successful_result() {
    for code in [Code::Cancelled, Code::DeadlineExceeded] {
        execute_output_after_handler_completion(Some(code)).await;
    }
}

async fn execute_output_after_handler_completion(cancel_status: Option<Code>) {
    let channel = outbound_task_channel(Endpoint::from_static("http://127.0.0.1:1"), None);
    let (cache, _directory) = test_cache();
    let started = Arc::new(Notify::new());
    let registry = Registry::new()
        .register(ExecuteOutputOrderFlow {
            step: ExecuteOutputOrderStep {
                started: Arc::clone(&started),
                should_wait_for_cancellation: cancel_status.is_some(),
            },
        })
        .expect("register output order Flow");
    let dispatcher = WorkerDispatcher::new(
        registry,
        ValueHydrator::new(FlowServiceClient::new(channel), cache),
        crate::server_protocol::NegotiatedServerProtocolVersion::default(),
    );
    let (sender, mut receiver) = mpsc::channel(1);
    sender
        .try_send(PollWorkerTaskRequest::default())
        .expect("fill outbound stream");
    let cancellation = InvocationCancellation::new();
    let progress_cancellation = InvocationCancellation::new();
    let cancel_code = Arc::new(AtomicI32::new(0));
    let frame_sender = TaskFrameSender::new(
        "execute".to_string(),
        sender,
        InvocationCancellation::new(),
        progress_cancellation.clone(),
        Arc::clone(&cancel_code),
        Arc::new(Mutex::new(false)),
    );
    let task = PollWorkerTask {
        task_id: "execute".to_string(),
        request: Some(TaskRequest::Execute(InvokeExecuteMethodRequest {
            flow_type: "ExecuteOutputOrderFlow".to_string(),
            step_type: "ExecuteOutputOrderStep".to_string(),
            context: Some(ProtoContext {
                flow_id: uuid::Uuid::new_v4().to_string(),
                ..Default::default()
            }),
            step_input: Some(crate::value_mapper::encode(&()).expect("encode input")),
            ..Default::default()
        })),
        ..Default::default()
    };
    let handler = tokio::spawn(execute_task(
        dispatcher,
        task,
        cancellation.clone(),
        Arc::clone(&cancel_code),
        frame_sender,
    ));
    if let Some(code) = cancel_status {
        timeout(Duration::from_secs(5), started.notified())
            .await
            .expect("handler started");
        cancel_code.store(code as i32, Ordering::Release);
        progress_cancellation.cancel();
        cancellation.cancel();
    } else {
        timeout(Duration::from_secs(5), cancellation.cancelled())
            .await
            .expect("handler completed before output drains");
    }
    receiver.recv().await.expect("remove outbound filler");
    if cancel_status.is_none() {
        let heartbeat = receive_frame(&mut receiver).await;
        assert!(
            matches!(heartbeat.output, Some(Output::Execute(value)) if matches!(value.output, Some(invoke_execute_method_output::Output::Heartbeat(_))))
        );
    }
    let result = receive_frame(&mut receiver).await;
    assert!(
        matches!(result.output, Some(Output::Execute(value)) if matches!(value.output, Some(invoke_execute_method_output::Output::Result(_))))
    );
    timeout(Duration::from_secs(5), handler)
        .await
        .expect("task completed")
        .expect("join task");
    assert!(receiver.try_recv().is_err());
}

fn frame_sender(
    task_id: &str,
    sender: mpsc::Sender<PollWorkerTaskRequest>,
    progress_cancellation: InvocationCancellation,
) -> TaskFrameSender {
    TaskFrameSender::new(
        task_id.to_string(),
        sender,
        InvocationCancellation::new(),
        progress_cancellation,
        Arc::new(AtomicI32::new(Code::Cancelled as i32)),
        Arc::new(Mutex::new(false)),
    )
}

fn step_progress(is_execute: bool) -> Vec<Output> {
    let heartbeat = StepMethodHeartbeat::default();
    let write = StepStreamWrite {
        stream_name: "progress".to_string(),
        ..Default::default()
    };
    if is_execute {
        vec![
            Output::Execute(InvokeExecuteMethodOutput {
                output: Some(invoke_execute_method_output::Output::Heartbeat(heartbeat)),
            }),
            Output::Execute(InvokeExecuteMethodOutput {
                output: Some(invoke_execute_method_output::Output::StreamWrite(write)),
            }),
        ]
    } else {
        vec![
            Output::WaitFor(InvokeWaitForMethodOutput {
                output: Some(invoke_wait_for_method_output::Output::Heartbeat(heartbeat)),
            }),
            Output::WaitFor(InvokeWaitForMethodOutput {
                output: Some(invoke_wait_for_method_output::Output::StreamWrite(write)),
            }),
        ]
    }
}

fn step_result(is_execute: bool) -> Output {
    if is_execute {
        Output::Execute(InvokeExecuteMethodOutput {
            output: Some(invoke_execute_method_output::Output::Result(
                InvokeExecuteMethodResponse::default(),
            )),
        })
    } else {
        Output::WaitFor(InvokeWaitForMethodOutput {
            output: Some(invoke_wait_for_method_output::Output::Result(
                InvokeWaitForMethodResponse::default(),
            )),
        })
    }
}

async fn receive_frame(
    receiver: &mut mpsc::Receiver<PollWorkerTaskRequest>,
) -> PollWorkerTaskFrame {
    let request = timeout(Duration::from_secs(5), receiver.recv())
        .await
        .expect("task frame timeout")
        .expect("task frame channel");
    let Some(RequestFrame::TaskFrame(frame)) = request.frame else {
        panic!("expected task frame");
    };
    frame
}
