// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::atomic::AtomicI32;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use dex_protocol::dex::poll_worker_task_frame::Output;
use dex_protocol::dex::poll_worker_task_request::Frame as RequestFrame;
use dex_protocol::dex::{PollWorkerTaskPong, PollWorkerTaskRequest, WorkerErrorResponse};
use tokio::sync::mpsc;
use tokio::time::timeout;
use tonic::Code;

use super::ActiveTask;
use super::task_frame_sender::TaskFrameSender;
use crate::context::InvocationCancellation;

#[tokio::test]
async fn blocked_task_frame_send_leaves_final_frame_flag_unlocked() {
    let (sender, _receiver) = mpsc::channel(1);
    sender
        .try_send(PollWorkerTaskRequest {
            frame: Some(RequestFrame::Pong(PollWorkerTaskPong {})),
        })
        .expect("fill outbound stream");
    let cancellation = InvocationCancellation::new();
    let final_frame_sent = Arc::new(Mutex::new(false));
    let sending = tokio::spawn({
        let sender = sender.clone();
        let cancellation = cancellation.clone();
        let final_frame_sent = Arc::clone(&final_frame_sent);
        async move {
            TaskFrameSender::new(
                "task-1".to_string(),
                sender,
                cancellation,
                InvocationCancellation::new(),
                Arc::new(AtomicI32::new(0)),
                final_frame_sent,
            )
            .send(Output::WorkerError(WorkerErrorResponse::default()))
            .await
        }
    });
    tokio::task::yield_now().await;
    assert!(!sending.is_finished());
    assert!(
        !*final_frame_sent
            .try_lock()
            .expect("blocked send does not hold the final frame lock")
    );
    cancellation.cancel();
    let result = timeout(Duration::from_secs(1), sending)
        .await
        .expect("send stopped")
        .expect("join send");
    assert_eq!(Code::Cancelled, result.expect_err("cancelled send").code());
    assert!(!*final_frame_sent.lock().expect("final frame lock"));
}

#[tokio::test(flavor = "multi_thread")]
async fn forced_stop_cancels_only_tasks_without_queued_final_output() {
    for _ in 0..200 {
        let (sender, mut receiver) = mpsc::channel(4);
        let transport_cancellation = InvocationCancellation::new();
        let progress_cancellation = InvocationCancellation::new();
        let cancel_code = Arc::new(AtomicI32::new(0));
        let final_frame_sent = Arc::new(Mutex::new(false));
        let frame_sender = TaskFrameSender::new(
            "task-1".to_string(),
            sender,
            transport_cancellation.clone(),
            progress_cancellation.clone(),
            Arc::clone(&cancel_code),
            Arc::clone(&final_frame_sent),
        );
        let handler = tokio::spawn(async {});
        let task = ActiveTask {
            task_id: "task-1".to_string(),
            cancellation: InvocationCancellation::new(),
            transport_cancellation,
            progress_cancellation,
            cancel_code,
            handler,
            final_frame_sent,
            deadline: None,
        };
        let sending = tokio::spawn(async move {
            frame_sender
                .send(Output::WorkerError(WorkerErrorResponse::default()))
                .await
        });
        let is_cancelled = task.cancel_unless_final_frame_sent(Code::Cancelled);
        let sent = sending.await.expect("join send");
        // Exactly one wins: a queued final output is never cancelled, and a cancelled task queues none.
        assert_ne!(is_cancelled, sent.is_ok());
        assert_eq!(sent.is_ok(), receiver.try_recv().is_ok());
        task.handler.await.expect("join handler");
    }
}
