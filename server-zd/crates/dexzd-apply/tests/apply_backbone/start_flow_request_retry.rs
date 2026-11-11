// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! A StartFlow retry with the request ID that started the latest run is the same request under
//! every ID reuse policy.

use super::*;
use dexzd_keyspace::flow_directory_key;
use dexzd_protocol::dexzd::v1::FlowDirectoryRecord;

const ORIGINAL_REQUEST_ID: &str = "start-request-1";

fn start_request_with_policy(
    request_id: &str,
    id_reuse_policy: IdReusePolicy,
    should_ignore_already_started_error: bool,
) -> StartFlowRequest {
    let mut request = start_request(request_id);
    request.flow_start_options = Some(FlowStartOptions {
        id_reuse_policy: id_reuse_policy as i32,
        flow_already_started_options: Some(FlowAlreadyStartedOptions {
            ignore_already_started_error: should_ignore_already_started_error,
        }),
        ..FlowStartOptions::default()
    });
    request
}

fn stop_flow(harness: &mut Harness, stop_type: StopType) {
    harness.apply(Command::FlowStop(FlowStopCommand {
        request: Some(StopFlowRequest {
            stop_type: stop_type as i32,
            ..StopFlowRequest::default()
        }),
    }));
}

fn directory_run_ids(harness: &Harness) -> Vec<Vec<u8>> {
    let key = flow_directory_key(
        harness.context.flow_hash,
        TenantId::DEFAULT,
        &harness.flow_id,
    );
    let bytes = harness
        .state
        .get(ColumnFamily::Flow, &key)
        .expect("memory reads never fail")
        .expect("the Flow directory exists");
    FlowDirectoryRecord::decode(bytes.as_slice())
        .expect("the Flow directory decodes")
        .runs
        .into_iter()
        .map(|run| run.run_id)
        .collect()
}

/// Starts run r1 with the original request ID, then moves it to `first_run_status`.
fn start_first_run(
    id_reuse_policy: IdReusePolicy,
    first_run_status: FlowStatus,
) -> (Harness, Vec<u8>) {
    let mut harness = Harness::new("order-1");
    harness.start(start_request_with_policy(
        ORIGINAL_REQUEST_ID,
        id_reuse_policy,
        true,
    ));
    let first_run_id = harness.run_id.clone();
    match first_run_status {
        FlowStatus::Running => {}
        FlowStatus::Canceled => stop_flow(&mut harness, StopType::Cancel),
        FlowStatus::Failed => stop_flow(&mut harness, StopType::Fail),
        FlowStatus::Terminated => stop_flow(&mut harness, StopType::Terminate),
        other => panic!("the test cannot drive a run to {other:?}"),
    }
    assert_eq!(harness.run_record().status, first_run_status as i32);
    (harness, first_run_id)
}

/// Retries the original request under `id_reuse_policy` and checks that nothing changed.
fn assert_retry_is_the_same_request(id_reuse_policy: IdReusePolicy, first_run_status: FlowStatus) {
    let (mut harness, first_run_id) = start_first_run(id_reuse_policy, first_run_status);

    let silent_retry = harness.start(start_request_with_policy(
        ORIGINAL_REQUEST_ID,
        id_reuse_policy,
        true,
    ));
    assert!(
        silent_retry.writes.is_empty(),
        "{id_reuse_policy:?}: the retry writes nothing"
    );
    match silent_retry.reply.reply {
        Some(command_reply::Reply::StartFlow(reply)) => {
            assert!(
                reply.is_existing_run,
                "{id_reuse_policy:?}: the retry returns the existing run"
            );
            assert_eq!(reply.run_id, first_run_id, "{id_reuse_policy:?}");
        }
        other => panic!("{id_reuse_policy:?}: unexpected reply {other:?}"),
    }

    let explicit_retry = harness.start(start_request_with_policy(
        ORIGINAL_REQUEST_ID,
        id_reuse_policy,
        false,
    ));
    assert!(
        explicit_retry.writes.is_empty(),
        "{id_reuse_policy:?}: the retry writes nothing"
    );
    match &explicit_retry.reply.reply {
        Some(command_reply::Reply::Error(error)) => {
            assert_eq!(error.grpc_code, 6, "{id_reuse_policy:?}: ALREADY_EXISTS");
            assert_eq!(
                error.sub_status,
                ErrorSubStatus::FlowAlreadyStarted as i32,
                "{id_reuse_policy:?}"
            );
        }
        other => panic!("{id_reuse_policy:?}: unexpected reply {other:?}"),
    }

    assert_eq!(directory_run_ids(&harness), vec![first_run_id.clone()]);
    assert_eq!(harness.run_id, first_run_id);
    assert_eq!(harness.run_record().status, first_run_status as i32);
}

#[test]
fn allow_if_no_running_retry_with_the_same_request_id_returns_the_closed_run() {
    assert_retry_is_the_same_request(IdReusePolicy::AllowIfNoRunning, FlowStatus::Canceled);
    assert_retry_is_the_same_request(IdReusePolicy::AllowIfNoRunning, FlowStatus::Terminated);
}

#[test]
fn allow_if_previous_exists_abnormally_retry_with_the_same_request_id_returns_the_closed_run() {
    assert_retry_is_the_same_request(
        IdReusePolicy::AllowIfPreviousExistsAbnormally,
        FlowStatus::Failed,
    );
    assert_retry_is_the_same_request(
        IdReusePolicy::AllowIfPreviousExistsAbnormally,
        FlowStatus::Canceled,
    );
}

#[test]
fn allow_terminate_if_running_retry_with_the_same_request_id_keeps_the_run_running() {
    assert_retry_is_the_same_request(IdReusePolicy::AllowTerminateIfRunning, FlowStatus::Running);
}

#[test]
fn a_different_request_id_still_follows_the_reuse_policy() {
    let (mut harness, first_run_id) =
        start_first_run(IdReusePolicy::AllowTerminateIfRunning, FlowStatus::Running);
    let new_start = harness.start(start_request_with_policy(
        "start-request-2",
        IdReusePolicy::AllowTerminateIfRunning,
        true,
    ));
    match new_start.reply.reply {
        Some(command_reply::Reply::StartFlow(reply)) => assert!(!reply.is_existing_run),
        other => panic!("unexpected reply {other:?}"),
    }
    let second_run_id = harness.run_id.clone();
    assert_ne!(second_run_id, first_run_id);
    assert_eq!(
        directory_run_ids(&harness),
        vec![first_run_id.clone(), second_run_id]
    );
    assert_eq!(harness.run_record().status, FlowStatus::Running as i32);
    harness.run_id = first_run_id;
    assert_eq!(harness.run_record().status, FlowStatus::Terminated as i32);

    let (mut closed_harness, closed_run_id) =
        start_first_run(IdReusePolicy::AllowIfNoRunning, FlowStatus::Canceled);
    closed_harness.start(start_request_with_policy(
        "start-request-2",
        IdReusePolicy::AllowIfNoRunning,
        false,
    ));
    assert_ne!(closed_harness.run_id, closed_run_id);
    assert_eq!(directory_run_ids(&closed_harness).len(), 2);
}
