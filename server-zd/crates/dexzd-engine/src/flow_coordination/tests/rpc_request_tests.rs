// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::atomic::Ordering;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use dexzd_apply::{FlowStateReader, StateWrite};
use dexzd_keyspace::row_keys::{RequestKind, request_dedup_key};
use dexzd_keyspace::{ColumnFamily, RunId};
use dexzd_protocol::dex::{
    AttributeWrite, GetAttributesRequest, InvokeRpcRequest, InvokeWorkerRpcResponse,
};
use futures::{future::join_all, poll};
use tokio::sync::Notify;

use super::fixtures::{TENANT, attributes_set_command, flow, start_flow, string_value};
use super::test_support::FlowCoordinationHarness;
use crate::flow_coordination::client_operations::request_fingerprint::request_fingerprint;
use crate::flow_coordination::client_operations::rpc_requests::RpcRequestGuard;
use crate::flow_coordination::{
    CommitUrgency, FlowCoordinationConfig, FlowCoordinationError, WorkerRpcResult, grpc_code,
};

#[tokio::test(start_paused = true)]
async fn rpc_joiners_share_dispatch_and_replay_during_commit_handoff() {
    let harness = FlowCoordinationHarness::new();
    harness
        .applier
        .uses_real_apply
        .store(true, Ordering::SeqCst);
    start_flow(&harness, "shared-rpc").await.unwrap();
    let mut request = rpc_request("shared-rpc");
    request.lock_attribute_keys.clear();
    let gate = hold_worker(&harness);
    script_result(&harness);
    let mut first = Box::pin(harness.registry.invoke_rpc(TENANT, request.clone()));
    tokio::select! {
        result = &mut first => panic!("Worker is gated: {result:?}"),
        () = harness.rpc_invoker.entered.notified() => {}
    }
    let mut joiners = Box::pin(join_all(
        (0..8).map(|_| harness.registry.invoke_rpc(TENANT, request.clone())),
    ));
    assert!(poll!(joiners.as_mut()).is_pending());
    assert_eq!(harness.rpc_invoker.invocations.lock().unwrap().len(), 1);
    gate.notify_one();
    harness.committer.submitted.notified().await;
    assert!(harness.commit_next());
    assert!(
        poll!(first.as_mut()).is_pending(),
        "the original task has not finished yet"
    );
    let replay = harness.registry.invoke_rpc(TENANT, request).await.unwrap();
    let (first, joiners) = tokio::join!(first, joiners);
    assert_eq!(first.unwrap(), replay);
    for result in joiners {
        assert_eq!(result.unwrap(), replay);
    }
    assert_eq!(harness.rpc_invoker.invocations.lock().unwrap().len(), 1);
}

#[tokio::test(start_paused = true)]
async fn rpc_committed_replay_precedes_a_conflicting_pending_entry() {
    let harness = Arc::new(FlowCoordinationHarness::new());
    harness
        .applier
        .uses_real_apply
        .store(true, Ordering::SeqCst);
    let run_id = start_flow(&harness, "replayed-rpc").await.unwrap();
    let request = rpc_request("replayed-rpc");
    script_result(&harness);
    let response = harness
        .drive(harness.registry.invoke_rpc(TENANT, request.clone()))
        .await
        .unwrap();
    let conflicting = InvokeRpcRequest {
        input: Some(string_value("different")),
        ..request.clone()
    };
    for should_commit_after_snapshot in [false, true] {
        let pending = Arc::new(Mutex::new(None));
        let record_key = request_dedup_key(
            harness
                .registry
                .flow_context(&flow("replayed-rpc"))
                .flow_hash,
            run_id,
            RequestKind::InvokeRpc,
            &request.request_id,
        );
        let record = harness
            .store
            .view()
            .get(ColumnFamily::Flow, &record_key)
            .unwrap()
            .unwrap();
        let transition = CommittedReplayRace {
            harness: Arc::clone(&harness),
            run_id,
            request: conflicting.clone(),
            record_key,
            record,
            pending: Arc::clone(&pending),
        };
        if should_commit_after_snapshot {
            harness.store.apply_writes(&[StateWrite::Delete {
                column_family: ColumnFamily::Flow,
                key: transition.record_key.clone(),
            }]);
            *harness.committer.view_hook.lock().unwrap() =
                Some(Box::new(move || transition.commit_and_insert_conflict()));
        } else {
            transition.commit_and_insert_conflict();
        }
        let replay = harness
            .registry
            .invoke_rpc(TENANT, request.clone())
            .await
            .unwrap();
        assert_eq!(replay, response);
        let error = harness
            .registry
            .invoke_rpc(TENANT, conflicting.clone())
            .await
            .unwrap_err();
        assert_eq!(error.grpc_code(), grpc_code::INVALID_ARGUMENT);
        assert_eq!(harness.rpc_invoker.invocations.lock().unwrap().len(), 1);
        drop(pending.lock().unwrap().take());
    }
}

#[tokio::test(start_paused = true)]
async fn rpc_panic_resolves_joiners_and_allows_the_request_id_to_retry() {
    let harness = FlowCoordinationHarness::new();
    harness
        .applier
        .uses_real_apply
        .store(true, Ordering::SeqCst);
    start_flow(&harness, "panicked-rpc").await.unwrap();
    let request = rpc_request("panicked-rpc");
    let gate = hold_worker(&harness);
    harness
        .rpc_invoker
        .should_panic
        .store(true, Ordering::SeqCst);
    let mut first = Box::pin(harness.registry.invoke_rpc(TENANT, request.clone()));
    tokio::select! {
        result = &mut first => panic!("Worker is gated: {result:?}"),
        () = harness.rpc_invoker.entered.notified() => {}
    }
    let mut joiner = Box::pin(harness.registry.invoke_rpc(TENANT, request.clone()));
    assert!(poll!(joiner.as_mut()).is_pending());
    gate.notify_one();
    let (first, joiner) = tokio::join!(first, joiner);
    for result in [first, joiner] {
        let error = result.unwrap_err();
        assert!(
            matches!(error, FlowCoordinationError::Internal { .. }),
            "{error:?}"
        );
    }
    harness
        .rpc_invoker
        .should_panic
        .store(false, Ordering::SeqCst);
    *harness.rpc_invoker.gate.lock().unwrap() = None;
    script_result(&harness);
    harness
        .drive(harness.registry.invoke_rpc(TENANT, request))
        .await
        .unwrap();
    assert_eq!(harness.rpc_invoker.invocations.lock().unwrap().len(), 2);
    harness.registry.close_rpc_admission();
    harness.registry.wait_for_rpc_tasks().await;
}

#[tokio::test(start_paused = true)]
async fn rpc_registry_halt_resolves_all_joiners_and_stops_tasks() {
    let harness = FlowCoordinationHarness::new();
    harness
        .applier
        .uses_real_apply
        .store(true, Ordering::SeqCst);
    start_flow(&harness, "halted-rpc").await.unwrap();
    hold_worker(&harness);
    let request = rpc_request("halted-rpc");
    let mut first = Box::pin(harness.registry.invoke_rpc(TENANT, request.clone()));
    tokio::select! {
        result = &mut first => panic!("Worker is gated: {result:?}"),
        () = harness.rpc_invoker.entered.notified() => {}
    }
    let mut joiner = Box::pin(harness.registry.invoke_rpc(TENANT, request));
    assert!(poll!(joiner.as_mut()).is_pending());
    harness
        .registry
        .halt("test commit pipeline stopped".to_owned());
    let (first, joiner) = tokio::join!(first, joiner);
    for result in [first, joiner] {
        assert!(matches!(
            result,
            Err(FlowCoordinationError::NodeStopping { .. })
        ));
    }
    harness.registry.cancel_rpc_tasks();
    harness.registry.wait_for_rpc_tasks().await;
    assert_eq!(harness.rpc_invoker.invocations.lock().unwrap().len(), 1);
}

#[tokio::test(start_paused = true)]
async fn rpc_capacity_allows_joiners_and_releases_disconnected_calls_at_the_deadline() {
    let config = FlowCoordinationConfig {
        max_in_flight_rpcs_per_node: 1,
        rpc_default_timeout: Duration::from_secs(2),
        ..FlowCoordinationConfig::default()
    };
    let harness = FlowCoordinationHarness::with_config(&config);
    harness
        .applier
        .uses_real_apply
        .store(true, Ordering::SeqCst);
    let run_id = start_flow(&harness, "bounded-rpc").await.unwrap();
    hold_worker(&harness);
    let request = rpc_request("bounded-rpc");
    let mut first = Box::pin(harness.registry.invoke_rpc(TENANT, request.clone()));
    tokio::select! {
        result = &mut first => panic!("Worker is gated: {result:?}"),
        () = harness.rpc_invoker.entered.notified() => {}
    }
    drop(first);
    let mut joiner = Box::pin(harness.registry.invoke_rpc(TENANT, request.clone()));
    assert!(poll!(joiner.as_mut()).is_pending());
    let error = harness
        .registry
        .invoke_rpc(
            TENANT,
            InvokeRpcRequest {
                request_id: "another".to_owned(),
                ..request.clone()
            },
        )
        .await
        .unwrap_err();
    assert_eq!(error.grpc_code(), grpc_code::RESOURCE_EXHAUSTED);
    let address = flow("bounded-rpc");
    let mut closing = Box::pin(harness.registry.wait_for_transactional_rpcs(&address));
    assert!(poll!(closing.as_mut()).is_pending());
    tokio::time::advance(Duration::from_secs(2)).await;
    assert_eq!(
        joiner.await.unwrap_err().grpc_code(),
        grpc_code::DEADLINE_EXCEEDED
    );
    closing.await.unwrap();
    let lock = harness
        .registry
        .acquire_rpc_locks(
            harness.registry.lease(&flow("bounded-rpc")),
            run_id,
            &["balance".to_owned()],
        )
        .unwrap();
    drop(lock);
    *harness.rpc_invoker.gate.lock().unwrap() = None;
    script_result(&harness);
    harness
        .drive(harness.registry.invoke_rpc(TENANT, request))
        .await
        .unwrap();
}

#[tokio::test(start_paused = true)]
async fn rpc_admission_wait_preserves_the_full_worker_timeout() {
    for timeout_seconds in [0, 2] {
        let config = FlowCoordinationConfig {
            rpc_default_timeout: Duration::from_secs(2),
            ..FlowCoordinationConfig::default()
        };
        let harness = FlowCoordinationHarness::with_config(&config);
        harness
            .applier
            .uses_real_apply
            .store(true, Ordering::SeqCst);
        let run_id = start_flow(&harness, "admission-wait").await.unwrap();
        harness
            .registry
            .propose(
                flow("admission-wait"),
                attributes_set_command("admission-wait", run_id, "balance", string_value("old")),
                CommitUrgency::Batched,
            )
            .await
            .unwrap();
        let gate = hold_worker(&harness);
        script_result(&harness);
        let mut request = rpc_request("admission-wait");
        request.timeout_seconds = timeout_seconds;
        let mut caller = Box::pin(harness.registry.invoke_rpc(TENANT, request));
        assert!(poll!(caller.as_mut()).is_pending());
        tokio::task::yield_now().await;
        assert!(harness.rpc_invoker.invocations.lock().unwrap().is_empty());
        tokio::time::advance(Duration::from_secs(3)).await;
        assert!(poll!(caller.as_mut()).is_pending());
        assert!(harness.commit_next());
        tokio::select! {
            result = &mut caller => panic!("Worker is gated: {result:?}"),
            () = harness.rpc_invoker.entered.notified() => {}
        }
        assert_eq!(
            harness.rpc_invoker.invocations.lock().unwrap()[0].deadline,
            tokio::time::Instant::now() + Duration::from_secs(2)
        );
        tokio::time::advance(Duration::from_millis(1900)).await;
        assert!(poll!(caller.as_mut()).is_pending());
        gate.notify_one();
        assert_eq!(
            harness.drive(caller).await.unwrap().output,
            Some(string_value("result"))
        );
    }
}

#[tokio::test(start_paused = true)]
async fn rpc_deadline_does_not_bound_commit_and_disconnected_waiters_replay_the_result() {
    let harness = FlowCoordinationHarness::new();
    harness
        .applier
        .uses_real_apply
        .store(true, Ordering::SeqCst);
    start_flow(&harness, "commit-deadline").await.unwrap();
    script_result(&harness);
    let mut request = rpc_request("commit-deadline");
    request.timeout_seconds = 2;
    let mut caller = Box::pin(harness.registry.invoke_rpc(TENANT, request.clone()));
    tokio::select! {
        result = &mut caller => panic!("commit is pending: {result:?}"),
        () = harness.committer.submitted.notified() => {}
    }
    let mut joiner = Box::pin(harness.registry.invoke_rpc(TENANT, request.clone()));
    assert!(poll!(joiner.as_mut()).is_pending());
    let address = flow("commit-deadline");
    let mut closing = Box::pin(harness.registry.wait_for_transactional_rpcs(&address));
    assert!(
        tokio::time::timeout(Duration::from_secs(1), caller)
            .await
            .is_err()
    );
    tokio::time::advance(Duration::from_secs(2)).await;
    assert!(poll!(joiner.as_mut()).is_pending());
    assert!(poll!(closing.as_mut()).is_pending());
    assert!(harness.commit_next());
    let response = joiner.await.unwrap();
    assert_eq!(response.output, Some(string_value("result")));
    closing.await.unwrap();
    let replay = harness.registry.invoke_rpc(TENANT, request).await.unwrap();
    assert_eq!(replay, response);
    assert_eq!(harness.rpc_invoker.invocations.lock().unwrap().len(), 1);
}

#[tokio::test(start_paused = true)]
async fn rpc_handler_deadline_resolves_waiters_without_proposing_a_commit() {
    let harness = FlowCoordinationHarness::new();
    harness
        .applier
        .uses_real_apply
        .store(true, Ordering::SeqCst);
    let run_id = start_flow(&harness, "handler-deadline").await.unwrap();
    hold_worker(&harness);
    script_result(&harness);
    let mut request = rpc_request("handler-deadline");
    request.timeout_seconds = 2;
    let submitted_before_rpc = harness.submitted().len();
    let mut caller = Box::pin(harness.registry.invoke_rpc(TENANT, request.clone()));
    tokio::select! {
        result = &mut caller => panic!("Worker is gated: {result:?}"),
        () = harness.rpc_invoker.entered.notified() => {}
    }
    let mut joiner = Box::pin(harness.registry.invoke_rpc(TENANT, request.clone()));
    assert!(poll!(joiner.as_mut()).is_pending());
    tokio::time::advance(Duration::from_secs(2)).await;
    for result in [caller.await, joiner.await] {
        assert_eq!(
            result.unwrap_err().grpc_code(),
            grpc_code::DEADLINE_EXCEEDED
        );
    }
    assert_eq!(harness.submitted().len(), submitted_before_rpc);
    assert!(!harness.commit_next());
    let record_key = request_dedup_key(
        harness
            .registry
            .flow_context(&flow("handler-deadline"))
            .flow_hash,
        run_id,
        RequestKind::InvokeRpc,
        &request.request_id,
    );
    assert!(
        harness
            .store
            .view()
            .get(ColumnFamily::Flow, &record_key)
            .unwrap()
            .is_none()
    );
    harness
        .registry
        .wait_for_transactional_rpcs(&flow("handler-deadline"))
        .await
        .unwrap();
    *harness.rpc_invoker.gate.lock().unwrap() = None;
    let response = harness
        .drive(harness.registry.invoke_rpc(TENANT, request))
        .await
        .unwrap();
    assert_eq!(response.output, Some(string_value("result")));
    assert_eq!(harness.rpc_invoker.invocations.lock().unwrap().len(), 2);
}

#[tokio::test(start_paused = true)]
async fn rpc_commit_lifetime_budget_releases_disconnected_tasks_with_an_unknown_outcome() {
    let config = FlowCoordinationConfig {
        max_in_flight_rpcs_per_node: 1,
        ..FlowCoordinationConfig::default()
    };
    let harness = FlowCoordinationHarness::with_config(&config);
    harness
        .applier
        .uses_real_apply
        .store(true, Ordering::SeqCst);
    let run_id = start_flow(&harness, "commit-budget").await.unwrap();
    script_result(&harness);
    let mut request = rpc_request("commit-budget");
    request.timeout_seconds = 2;
    let mut caller = Box::pin(harness.registry.invoke_rpc(TENANT, request.clone()));
    tokio::select! {
        result = &mut caller => panic!("commit is pending: {result:?}"),
        () = harness.committer.submitted.notified() => {}
    }
    drop(caller);
    let mut joiner = Box::pin(harness.registry.invoke_rpc(TENANT, request.clone()));
    assert!(poll!(joiner.as_mut()).is_pending());
    tokio::time::advance(Duration::from_secs(60)).await;
    assert_eq!(
        joiner.await.unwrap_err().grpc_code(),
        grpc_code::UNAVAILABLE
    );
    harness
        .registry
        .wait_for_transactional_rpcs(&flow("commit-budget"))
        .await
        .unwrap();
    let lock = harness
        .registry
        .acquire_rpc_locks(
            harness.registry.lease(&flow("commit-budget")),
            run_id,
            &["balance".to_owned()],
        )
        .unwrap();
    drop(lock);
    assert!(
        harness.commit_next(),
        "the submitted command can still commit"
    );
    let replay = harness
        .registry
        .invoke_rpc(TENANT, request.clone())
        .await
        .unwrap();
    assert_eq!(replay.output, Some(string_value("result")));
    script_result(&harness);
    harness
        .drive(harness.registry.invoke_rpc(
            TENANT,
            InvokeRpcRequest {
                request_id: "after-budget".to_owned(),
                ..request
            },
        ))
        .await
        .unwrap();
    assert_eq!(harness.rpc_invoker.invocations.lock().unwrap().len(), 2);
}

#[tokio::test(start_paused = true)]
async fn rpc_commit_budget_bounds_upload_stalls_before_proposal_and_allows_retry() {
    let mut config = FlowCoordinationConfig {
        max_in_flight_rpcs_per_node: 1,
        ..FlowCoordinationConfig::default()
    };
    config.validation.has_blob_store = true;
    let harness = FlowCoordinationHarness::with_config(&config);
    harness
        .applier
        .uses_real_apply
        .store(true, Ordering::SeqCst);
    let run_id = start_flow(&harness, "upload-budget").await.unwrap();
    let gate = Arc::new(Notify::new());
    *harness.hard_delete_uploader.gate.lock().unwrap() = Some(Arc::clone(&gate));
    script_worker_response(
        &harness,
        InvokeWorkerRpcResponse {
            upsert_attributes: vec![AttributeWrite {
                key: "secret".to_owned(),
                value: Some(string_value("secret-value")),
                is_hard_delete_on_null: true,
                ..AttributeWrite::default()
            }],
            ..InvokeWorkerRpcResponse::default()
        },
    );
    let mut request = rpc_request("upload-budget");
    request.timeout_seconds = 2;
    let submitted_before_rpc = harness.submitted().len();
    let mut caller = Box::pin(harness.registry.invoke_rpc(TENANT, request.clone()));
    tokio::select! {
        result = &mut caller => panic!("upload is gated: {result:?}"),
        () = harness.hard_delete_uploader.entered.notified() => {}
    }
    drop(caller);
    let mut joiner = Box::pin(harness.registry.invoke_rpc(TENANT, request.clone()));
    assert!(poll!(joiner.as_mut()).is_pending());
    tokio::time::advance(Duration::from_secs(59)).await;
    assert!(poll!(joiner.as_mut()).is_pending());
    tokio::time::advance(Duration::from_secs(1)).await;
    let error = joiner.await.unwrap_err();
    assert_eq!(error.grpc_code(), grpc_code::UNAVAILABLE);
    assert!(error.to_string().contains("outcome is unknown"));
    assert_eq!(harness.submitted().len(), submitted_before_rpc);
    assert!(!harness.commit_next());
    harness
        .registry
        .wait_for_transactional_rpcs(&flow("upload-budget"))
        .await
        .unwrap();
    let lock = harness
        .registry
        .acquire_rpc_locks(
            harness.registry.lease(&flow("upload-budget")),
            run_id,
            &["balance".to_owned()],
        )
        .unwrap();
    drop(lock);
    gate.notify_one();
    *harness.hard_delete_uploader.gate.lock().unwrap() = None;
    script_result(&harness);
    assert_eq!(
        harness
            .drive(harness.registry.invoke_rpc(TENANT, request))
            .await
            .unwrap()
            .output,
        Some(string_value("result"))
    );
    assert_eq!(harness.rpc_invoker.invocations.lock().unwrap().len(), 2);
}

#[tokio::test(start_paused = true)]
async fn rpc_retry_before_an_orphaned_commit_can_invoke_the_worker_again() {
    let harness = FlowCoordinationHarness::new();
    harness
        .applier
        .uses_real_apply
        .store(true, Ordering::SeqCst);
    start_flow(&harness, "orphaned-commit").await.unwrap();
    let mut request = rpc_request("orphaned-commit");
    request.lock_attribute_keys.clear();
    script_worker_response(
        &harness,
        InvokeWorkerRpcResponse {
            output: Some(string_value("result")),
            upsert_attributes: vec![AttributeWrite {
                key: "balance".to_owned(),
                value: Some(string_value("first-write")),
                ..AttributeWrite::default()
            }],
            ..InvokeWorkerRpcResponse::default()
        },
    );
    let mut caller = Box::pin(harness.registry.invoke_rpc(TENANT, request.clone()));
    tokio::select! {
        result = &mut caller => panic!("commit is pending: {result:?}"),
        () = harness.committer.submitted.notified() => {}
    }
    tokio::time::advance(Duration::from_secs(60)).await;
    assert_eq!(
        caller.await.unwrap_err().grpc_code(),
        grpc_code::UNAVAILABLE
    );
    script_worker_response(
        &harness,
        InvokeWorkerRpcResponse {
            output: Some(string_value("retried-result")),
            upsert_attributes: vec![AttributeWrite {
                key: "balance".to_owned(),
                value: Some(string_value("retried-write")),
                ..AttributeWrite::default()
            }],
            ..InvokeWorkerRpcResponse::default()
        },
    );
    let mut retry = Box::pin(harness.registry.invoke_rpc(TENANT, request.clone()));
    tokio::select! {
        result = &mut retry => panic!("both commits are pending: {result:?}"),
        () = harness.committer.submitted.notified() => {}
    }
    assert_eq!(harness.rpc_invoker.invocations.lock().unwrap().len(), 2);
    assert_eq!(harness.committer.pending_count(), 2);
    assert!(harness.commit_next());
    assert_eq!(
        harness
            .registry
            .invoke_rpc(TENANT, request)
            .await
            .unwrap()
            .output,
        Some(string_value("result"))
    );
    assert!(harness.commit_next());
    assert_eq!(retry.await.unwrap().output, Some(string_value("result")));
    let attributes = harness
        .registry
        .get_attributes(
            TENANT,
            GetAttributesRequest {
                flow_id: "orphaned-commit".to_owned(),
                all_keys: true,
                ..GetAttributesRequest::default()
            },
        )
        .await
        .unwrap()
        .attributes;
    assert_eq!(attributes.len(), 1);
    assert_eq!(attributes[0].key, "balance");
    assert_eq!(attributes[0].value, Some(string_value("first-write")));
}

#[tokio::test(start_paused = true)]
async fn rpc_tasks_finish_during_drain_after_long_polls_close() {
    let harness = FlowCoordinationHarness::new();
    harness
        .applier
        .uses_real_apply
        .store(true, Ordering::SeqCst);
    start_flow(&harness, "drained-rpc").await.unwrap();
    let request = rpc_request("drained-rpc");
    let gate = hold_worker(&harness);
    script_result(&harness);
    let mut first = Box::pin(harness.registry.invoke_rpc(TENANT, request.clone()));
    tokio::select! {
        result = &mut first => panic!("Worker is gated: {result:?}"),
        () = harness.rpc_invoker.entered.notified() => {}
    }
    drop(first);
    harness.registry.close_long_polls();
    harness.registry.close_rpc_admission();
    let mut joiner = Box::pin(harness.registry.invoke_rpc(TENANT, request.clone()));
    assert!(poll!(joiner.as_mut()).is_pending());
    let error = harness
        .registry
        .invoke_rpc(
            TENANT,
            InvokeRpcRequest {
                request_id: "after-drain-started".to_owned(),
                ..request.clone()
            },
        )
        .await
        .unwrap_err();
    assert!(matches!(error, FlowCoordinationError::NodeStopping { .. }));
    let mut drain = Box::pin(harness.registry.wait_for_rpc_tasks());
    assert!(
        poll!(drain.as_mut()).is_pending(),
        "disconnected calls belong to the drain"
    );
    gate.notify_one();
    harness.drive(drain).await;
    assert_eq!(joiner.await.unwrap().output, Some(string_value("result")));
    let replay = harness.registry.invoke_rpc(TENANT, request).await.unwrap();
    assert_eq!(replay.output, Some(string_value("result")));
    assert_eq!(harness.rpc_invoker.invocations.lock().unwrap().len(), 1);
}

fn rpc_request(flow_id: &str) -> InvokeRpcRequest {
    InvokeRpcRequest {
        flow_id: flow_id.to_owned(),
        rpc_name: "updateBalance".to_owned(),
        request_id: "same-request".to_owned(),
        is_transactional: true,
        lock_attribute_keys: vec!["balance".to_owned()],
        ..InvokeRpcRequest::default()
    }
}

fn hold_worker(harness: &FlowCoordinationHarness) -> Arc<Notify> {
    let gate = Arc::new(Notify::new());
    *harness.rpc_invoker.gate.lock().unwrap() = Some(Arc::clone(&gate));
    gate
}

fn script_result(harness: &FlowCoordinationHarness) {
    script_worker_response(
        harness,
        InvokeWorkerRpcResponse {
            output: Some(string_value("result")),
            ..InvokeWorkerRpcResponse::default()
        },
    );
}

fn script_worker_response(harness: &FlowCoordinationHarness, response: InvokeWorkerRpcResponse) {
    harness
        .rpc_invoker
        .results
        .lock()
        .unwrap()
        .push_back(Ok(WorkerRpcResult {
            response,
            app_worker_id: "worker-1".to_owned(),
            pull_routing: None,
        }));
}

struct CommittedReplayRace {
    harness: Arc<FlowCoordinationHarness>,
    run_id: RunId,
    request: InvokeRpcRequest,
    record_key: Vec<u8>,
    record: Vec<u8>,
    pending: Arc<Mutex<Option<RpcRequestGuard>>>,
}

impl CommittedReplayRace {
    fn commit_and_insert_conflict(self) {
        self.harness
            .store
            .put(ColumnFamily::Flow, self.record_key, self.record);
        let (_, guard) = self
            .harness
            .registry
            .inner
            .rpc_requests
            .join_or_insert(
                &flow(&self.request.flow_id),
                self.run_id,
                &self.request.request_id,
                request_fingerprint(&self.request),
            )
            .unwrap();
        *self.pending.lock().unwrap() =
            Some(guard.expect("conflicting task inserted after the successful task finished"));
    }
}
