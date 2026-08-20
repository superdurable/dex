// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::time::{Duration, Instant};

use dex_sdk::{FlowStatus, Registry};

use crate::flow_service_client::{assert_worker_failure_stack_trace, await_live_worker_failure};
use crate::support::{DexDevTestEnvironment, flow_id};
use crate::worker_retry_after_workflow::{
    EXECUTE_RETRY_AFTER_DETAIL, RETRY_AFTER_SECONDS, RETRY_POLICY_INTERVAL_SECONDS,
    WAIT_FOR_RETRY_AFTER_DETAIL, WorkerRetryAfterExecuteWorkflow, WorkerRetryAfterWaitForWorkflow,
};

#[test]
#[ignore = "requires dexcli dev"]
fn test_wait_for_retry_after_stack_trace_and_delay() {
    let environment = DexDevTestEnvironment::start(
        Registry::new().register(WorkerRetryAfterWaitForWorkflow::new()),
    );
    let workflow = WorkerRetryAfterWaitForWorkflow::new();
    let flow_id = flow_id("wait-retry-after");
    let started_at = Instant::now();
    let run_id = environment
        .client
        .start_flow(&workflow, &flow_id, ())
        .expect("start waitFor retry-after Flow");
    let failure = await_live_worker_failure(&flow_id, &run_id);
    if failure.details.is_some() {
        assert_worker_failure_stack_trace(&failure, WAIT_FOR_RETRY_AFTER_DETAIL);
    }
    let result = environment
        .client
        .wait_for_flow_with_timeout(&flow_id, Duration::from_secs(30))
        .expect("complete waitFor retry-after Flow");
    assert_eq!(FlowStatus::Completed, result.status());
    let elapsed = started_at.elapsed();
    assert!(elapsed >= Duration::from_secs(RETRY_AFTER_SECONDS as u64));
    assert!(elapsed < Duration::from_secs(RETRY_POLICY_INTERVAL_SECONDS));
}

#[test]
#[ignore = "requires dexcli dev"]
fn test_execute_retry_after_stack_trace_and_delay() {
    let environment = DexDevTestEnvironment::start(
        Registry::new().register(WorkerRetryAfterExecuteWorkflow::new()),
    );
    let workflow = WorkerRetryAfterExecuteWorkflow::new();
    let flow_id = flow_id("execute-retry-after");
    let started_at = Instant::now();
    let run_id = environment
        .client
        .start_flow(&workflow, &flow_id, ())
        .expect("start execute retry-after Flow");
    let failure = await_live_worker_failure(&flow_id, &run_id);
    if failure.details.is_some() {
        assert_worker_failure_stack_trace(&failure, EXECUTE_RETRY_AFTER_DETAIL);
    }
    let result = environment
        .client
        .wait_for_flow_with_timeout(&flow_id, Duration::from_secs(30))
        .expect("complete execute retry-after Flow");
    assert_eq!(FlowStatus::Completed, result.status());
    let elapsed = started_at.elapsed();
    assert!(elapsed >= Duration::from_secs(RETRY_AFTER_SECONDS as u64));
    assert!(elapsed < Duration::from_secs(RETRY_POLICY_INTERVAL_SECONDS));
}
