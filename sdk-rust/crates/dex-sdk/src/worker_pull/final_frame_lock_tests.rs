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
