// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use std::time::Duration;

use dex_sdk::{FlowStatus, Registry};

use crate::flow_service_client::{assert_worker_failure_stack_trace, await_live_worker_failure};
use crate::support::{DexDevTestEnvironment, flow_id};
use crate::worker_origin_stack_workflow::{ORIGIN_STACK_DETAIL, WorkerOriginStackWaitForWorkflow};

#[test]
#[ignore = "requires dexcli dev"]
fn test_wait_for_origin_stack_trace() {
    let environment = DexDevTestEnvironment::start(
        Registry::new().register(WorkerOriginStackWaitForWorkflow::new()),
    );
    let workflow = WorkerOriginStackWaitForWorkflow::new();
    let flow_id = flow_id("wait-origin-stack");
    let run_id = environment
        .client
        .start_flow(&workflow, &flow_id, ())
        .expect("start waitFor origin-stack Flow");
    let failure = await_live_worker_failure(&flow_id, &run_id);
    if let Some(details) = &failure.details {
        assert_worker_failure_stack_trace(&failure, ORIGIN_STACK_DETAIL);
        let stack_trace = &details.original_worker_error_stack_trace;
        assert!(
            stack_trace.contains("origin_stack_failure"),
            "expected construction-site frame, got {stack_trace}"
        );
        assert!(
            !stack_trace.contains("worker_status"),
            "expected construction-site stack, not wrap-site: {stack_trace}"
        );
    }
    let result = environment
        .client
        .wait_for_flow_with_timeout(&flow_id, Duration::from_secs(30))
        .expect("complete waitFor origin-stack Flow");
    assert_eq!(FlowStatus::Completed, result.status());
}
