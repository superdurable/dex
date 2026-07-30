// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

use std::time::Duration;

use dex_core::{
    CoreError, InvocationFailure, InvocationKind, InvocationResult, WorkerConfig, WorkerCore,
};

#[tokio::test]
async fn routes_successful_invocation() {
    let worker = WorkerCore::new(WorkerConfig::new(4).unwrap());
    let dispatch_worker = worker.clone();
    let dispatch = tokio::spawn(async move {
        dispatch_worker
            .dispatch(InvocationKind::Execute, b"request".to_vec())
            .await
    });

    let invocation = worker.poll_invocation().await.unwrap();
    assert_eq!(invocation.kind(), InvocationKind::Execute);
    assert_eq!(invocation.request(), b"request");

    worker
        .complete_invocation(
            invocation.id(),
            InvocationResult::Success(b"response".to_vec()),
        )
        .unwrap();
    assert_eq!(
        dispatch.await.unwrap().unwrap(),
        InvocationResult::Success(b"response".to_vec())
    );
}

#[tokio::test]
async fn preserves_language_failure() {
    let worker = WorkerCore::new(WorkerConfig::new(4).unwrap());
    let dispatch_worker = worker.clone();
    let dispatch = tokio::spawn(async move {
        dispatch_worker
            .dispatch(InvocationKind::WorkerRpc, b"request".to_vec())
            .await
    });

    let invocation = worker.poll_invocation().await.unwrap();
    let failure = InvocationFailure::new(
        "ValueError",
        "invalid customer",
        b"python traceback".to_vec(),
    );
    worker
        .complete_invocation(invocation.id(), InvocationResult::Failure(failure.clone()))
        .unwrap();

    assert_eq!(
        dispatch.await.unwrap().unwrap(),
        InvocationResult::Failure(failure)
    );
}

#[tokio::test]
async fn rejects_duplicate_completion() {
    let worker = WorkerCore::new(WorkerConfig::new(1).unwrap());
    let dispatch_worker = worker.clone();
    let dispatch = tokio::spawn(async move {
        dispatch_worker
            .dispatch(InvocationKind::WaitFor, Vec::new())
            .await
    });

    let invocation = worker.poll_invocation().await.unwrap();
    worker
        .complete_invocation(invocation.id(), InvocationResult::Success(Vec::new()))
        .unwrap();
    dispatch.await.unwrap().unwrap();

    assert_eq!(
        worker.complete_invocation(invocation.id(), InvocationResult::Success(Vec::new())),
        Err(CoreError::UnknownInvocation(invocation.id()))
    );
}

#[tokio::test]
async fn shutdown_wakes_poller() {
    let worker = WorkerCore::new(WorkerConfig::new(1).unwrap());
    let poll_worker = worker.clone();
    let poll = tokio::spawn(async move { poll_worker.poll_invocation().await });
    tokio::task::yield_now().await;

    worker.initiate_shutdown().unwrap();

    assert_eq!(
        tokio::time::timeout(Duration::from_secs(1), poll)
            .await
            .unwrap()
            .unwrap(),
        Err(CoreError::WorkerShutdown)
    );
}

#[tokio::test]
async fn shutdown_fails_dispatched_request() {
    let worker = WorkerCore::new(WorkerConfig::new(1).unwrap());
    let dispatch_worker = worker.clone();
    let dispatch = tokio::spawn(async move {
        dispatch_worker
            .dispatch(InvocationKind::Execute, Vec::new())
            .await
    });
    let _invocation = worker.poll_invocation().await.unwrap();

    worker.initiate_shutdown().unwrap();

    assert_eq!(dispatch.await.unwrap(), Err(CoreError::WorkerShutdown));
}

#[tokio::test]
async fn queue_capacity_backpressures_dispatch() {
    let worker = WorkerCore::new(WorkerConfig::new(1).unwrap());
    let first_worker = worker.clone();
    let first_dispatch = tokio::spawn(async move {
        first_worker
            .dispatch(InvocationKind::Execute, b"first".to_vec())
            .await
    });
    wait_for_available_capacity(&worker, 0).await;

    let second_worker = worker.clone();
    let second_dispatch = tokio::spawn(async move {
        second_worker
            .dispatch(InvocationKind::Execute, b"second".to_vec())
            .await
    });
    tokio::task::yield_now().await;

    let first_invocation = worker.poll_invocation().await.unwrap();
    assert_eq!(first_invocation.request(), b"first");
    wait_for_available_capacity(&worker, 0).await;
    worker
        .complete_invocation(first_invocation.id(), InvocationResult::Success(Vec::new()))
        .unwrap();

    let second_invocation = worker.poll_invocation().await.unwrap();
    assert_eq!(second_invocation.request(), b"second");
    worker
        .complete_invocation(
            second_invocation.id(),
            InvocationResult::Success(Vec::new()),
        )
        .unwrap();

    first_dispatch.await.unwrap().unwrap();
    second_dispatch.await.unwrap().unwrap();
}

#[test]
fn rejects_zero_queue_capacity() {
    assert_eq!(WorkerConfig::new(0), Err(CoreError::InvalidQueueCapacity));
}

async fn wait_for_available_capacity(worker: &WorkerCore, expected: usize) {
    tokio::time::timeout(Duration::from_secs(1), async {
        while worker.available_queue_capacity() != expected {
            tokio::task::yield_now().await;
        }
    })
    .await
    .unwrap();
}
