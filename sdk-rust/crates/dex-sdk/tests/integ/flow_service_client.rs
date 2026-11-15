// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use std::time::{Duration, Instant};

use dex_protocol::dex::flow_service_client::FlowServiceClient;
use dex_protocol::dex::{GetFlowStateRequest, StepMethodFailure};
use tokio::runtime::Runtime;

/// Returns the event ID of the run's first StepWaitForCompleted history event.
pub(crate) fn await_wait_for_completed_event_id(flow_id: &str, run_id: &str) -> i64 {
    use dex_protocol::dex::GetHistoryEventsRequest;
    use dex_protocol::dex::flow_history_event::Payload;

    let server_address =
        std::env::var("DEX_SERVER_ADDRESS").unwrap_or_else(|_| "127.0.0.1:8801".to_string());
    let runtime = Runtime::new().expect("create FlowService runtime");
    runtime.block_on(async {
        let mut client = FlowServiceClient::connect(format!("http://{server_address}"))
            .await
            .expect("connect FlowService");
        let deadline = Instant::now() + Duration::from_secs(30);
        while Instant::now() < deadline {
            let response = client
                .get_history_events(GetHistoryEventsRequest {
                    flow_id: flow_id.to_string(),
                    run_id: run_id.to_string(),
                    estimate_page_size: 100,
                    ..Default::default()
                })
                .await
                .expect("GetHistoryEvents")
                .into_inner();
            let wait_completed = response
                .events
                .iter()
                .find(|event| matches!(event.payload, Some(Payload::StepWaitForCompleted(_))));
            if let Some(event) = wait_completed {
                return event.event_id;
            }
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
        panic!("Flow history has no StepWaitForCompleted event");
    })
}

pub(crate) fn await_live_worker_failure(flow_id: &str, run_id: &str) -> StepMethodFailure {
    let server_address =
        std::env::var("DEX_SERVER_ADDRESS").unwrap_or_else(|_| "127.0.0.1:8801".to_string());
    let runtime = Runtime::new().expect("create FlowService runtime");
    runtime.block_on(async {
        let mut client = FlowServiceClient::connect(format!("http://{server_address}"))
            .await
            .expect("connect FlowService");
        let deadline = Instant::now() + Duration::from_secs(6);
        while Instant::now() < deadline {
            let response = client
                .get_flow_state(GetFlowStateRequest {
                    flow_id: flow_id.to_string(),
                    run_id: run_id.to_string(),
                })
                .await
                .expect("GetFlowState")
                .into_inner();
            for step in response.active_step_executions {
                if let Some(failure) = step.last_failure_info {
                    return failure;
                }
            }
            tokio::time::sleep(Duration::from_millis(50)).await;
        }
        panic!("active Step did not expose retry failure");
    })
}

pub(crate) fn assert_worker_failure_stack_trace(
    failure: &StepMethodFailure,
    expected_detail: &str,
) {
    assert_eq!(1, failure.attempt);
    let details = failure.details.as_ref().expect("Step failure details");
    assert_eq!(expected_detail, details.original_worker_error_detail);
    let stack_trace = &details.original_worker_error_stack_trace;
    assert!(!stack_trace.is_empty());
    assert!(stack_trace.contains(expected_detail));
}
