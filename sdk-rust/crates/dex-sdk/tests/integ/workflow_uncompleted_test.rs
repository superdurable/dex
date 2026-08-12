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

use dex_sdk::{
    Client, FlowErrorType, FlowStatus, Registry, SdkError, SdkResult, StartFlowOptions,
    StopFlowOptions,
};

use crate::signal_workflow::SignalWorkflow;
use crate::support::{DexDevTestEnvironment, flow_id};
use crate::workflow_uncompleted_empty_decision_workflow::WorkflowUncompletedEmptyDecisionWorkflow;
use crate::workflow_uncompleted_force_fail_workflow::WorkflowUncompletedForceFailWorkflow;
use crate::workflow_uncompleted_state_failure_workflow::WorkflowUncompletedStateFailureWorkflow;
use crate::workflow_uncompleted_state_timeout_workflow::WorkflowUncompletedStateTimeoutWorkflow;

#[test]
#[ignore = "requires dexcli dev"]
fn test_flow_wait_timeout() {
    let environment = DexDevTestEnvironment::start(Registry::new().register(SignalWorkflow::new()));
    let workflow = SignalWorkflow::new();
    let flow_id = flow_id("wait-timeout");
    environment
        .client
        .start_flow(&workflow, &flow_id, 1)
        .expect("start waiting Flow");
    match environment
        .client
        .wait_for_flow_with_timeout(&flow_id, Duration::from_secs(1))
        .and_then(|result| result.single_output::<i32>())
        .expect_err("waitForFlow must time out")
    {
        SdkError::LongPollTimeout { service } => {
            assert_eq!(Some(flow_id.as_str()), service.flow_id())
        }
        error => panic!("expected LongPollTimeout, got {error:?}"),
    }
}

#[test]
#[ignore = "requires dexcli dev"]
fn test_flow_timeout() {
    let environment = DexDevTestEnvironment::start(Registry::new().register(SignalWorkflow::new()));
    let workflow = SignalWorkflow::new();
    let flow_id = flow_id("flow-timeout");
    let run_id = environment
        .client
        .start_flow_with_options(
            &workflow,
            &flow_id,
            1,
            StartFlowOptions::new().timeout(Duration::from_secs(1)),
        )
        .expect("start timed Flow");
    assert_failure(
        wait_for_failure(&environment, &flow_id),
        &run_id,
        FlowStatus::TimedOut,
        None,
        None,
        0,
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn test_flow_canceled() {
    assert_stopped_flow(StopFlowOptions::cancel(), FlowStatus::Canceled, None, None);
}

#[test]
#[ignore = "requires dexcli dev"]
fn test_flow_terminated() {
    assert_stopped_flow(
        StopFlowOptions::terminate().reason("terminated"),
        FlowStatus::Terminated,
        None,
        None,
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn test_flow_failed_by_api() {
    assert_stopped_flow(
        StopFlowOptions::fail().reason("fail by API"),
        FlowStatus::Failed,
        Some(FlowErrorType::ClientApiFailed),
        Some("fail by API"),
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn test_force_fail_flow() {
    let environment = DexDevTestEnvironment::start(
        Registry::new().register(WorkflowUncompletedForceFailWorkflow::new()),
    );
    let workflow = WorkflowUncompletedForceFailWorkflow::new();
    let flow_id = flow_id("force-fail");
    let run_id = environment
        .client
        .start_flow(&workflow, &flow_id, 5)
        .expect("start force-fail Flow");
    assert_failure(
        wait_for_failure(&environment, &flow_id),
        &run_id,
        FlowStatus::Failed,
        Some(FlowErrorType::StepDecisionFailed),
        Some("a failing message"),
        0,
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn test_worker_api_failure() {
    let environment = DexDevTestEnvironment::start(
        Registry::new().register(WorkflowUncompletedStateFailureWorkflow::new()),
    );
    let workflow = WorkflowUncompletedStateFailureWorkflow::new();
    let flow_id = flow_id("worker-api-failure");
    let run_id = environment
        .client
        .start_flow(&workflow, &flow_id, 5)
        .expect("start worker-failure Flow");
    match wait_for_failure(&environment, &flow_id) {
        SdkError::FlowUncompleted {
            run_id: failed_run_id,
            status,
            error_type,
            message,
            completions,
        } => {
            assert_eq!(run_id, failed_run_id);
            assert_eq!(FlowStatus::Failed, status);
            assert_eq!(Some(FlowErrorType::WorkerApiFailed), error_type);
            assert!(
                message
                    .as_deref()
                    .is_some_and(|value| value.contains("test api failing")),
                "{message:?}"
            );
            assert_eq!(0, completions.len());
        }
        error => panic!("expected FlowUncompleted, got {error:?}"),
    }
}

#[test]
#[ignore = "requires dexcli dev"]
fn test_worker_api_timeout() {
    let environment = DexDevTestEnvironment::start(
        Registry::new().register(WorkflowUncompletedStateTimeoutWorkflow::new()),
    );
    let workflow = WorkflowUncompletedStateTimeoutWorkflow::new();
    let flow_id = flow_id("worker-api-timeout");
    let run_id = environment
        .client
        .start_flow(&workflow, &flow_id, 5)
        .expect("start worker-timeout Flow");
    match wait_for_failure(&environment, &flow_id) {
        SdkError::FlowUncompleted {
            run_id: failed_run_id,
            status,
            error_type,
            message,
            completions,
        } => {
            assert_eq!(run_id, failed_run_id);
            assert_eq!(FlowStatus::Failed, status);
            assert_eq!(Some(FlowErrorType::WorkerApiFailed), error_type);
            assert!(
                message
                    .as_deref()
                    .is_some_and(|value| value.to_lowercase().contains("timeout")),
                "{message:?}"
            );
            assert_eq!(0, completions.len());
        }
        error => panic!("expected FlowUncompleted, got {error:?}"),
    }
}

#[test]
#[ignore = "requires dexcli dev"]
fn test_empty_decision_fails_flow() {
    let environment = DexDevTestEnvironment::start(
        Registry::new().register(WorkflowUncompletedEmptyDecisionWorkflow::new()),
    );
    let workflow = WorkflowUncompletedEmptyDecisionWorkflow::new();
    let flow_id = flow_id("empty-decision");
    let run_id = environment
        .client
        .start_flow(&workflow, &flow_id, 5)
        .expect("start empty-decision Flow");
    match wait_for_failure(&environment, &flow_id) {
        SdkError::FlowUncompleted {
            run_id: failed_run_id,
            status,
            error_type,
            message,
            completions,
        } => {
            assert_eq!(run_id, failed_run_id);
            assert_eq!(FlowStatus::Failed, status);
            assert_eq!(Some(FlowErrorType::WorkerApiFailed), error_type);
            assert!(
                message
                    .as_deref()
                    .is_some_and(|value| value.contains("go_to_many requires a movement")),
                "{message:?}"
            );
            assert_eq!(0, completions.len());
        }
        error => panic!("expected FlowUncompleted, got {error:?}"),
    }
}

fn assert_stopped_flow(
    options: StopFlowOptions,
    expected_status: FlowStatus,
    expected_error_type: Option<FlowErrorType>,
    expected_message: Option<&str>,
) {
    let environment = DexDevTestEnvironment::start(Registry::new().register(SignalWorkflow::new()));
    let workflow = SignalWorkflow::new();
    let flow_id = flow_id("stopped");
    let run_id = environment
        .client
        .start_flow(&workflow, &flow_id, 1)
        .expect("start stoppable Flow");
    environment
        .client
        .stop_flow(&flow_id, options)
        .expect("stop Flow");
    assert_failure(
        wait_for_failure(&environment, &flow_id),
        &run_id,
        expected_status,
        expected_error_type,
        expected_message,
        0,
    );
}

fn wait_for_failure(environment: &DexDevTestEnvironment, flow_id: &str) -> SdkError {
    environment
        .client
        .wait_for_flow_with_timeout(flow_id, Duration::from_secs(15))
        .and_then(|result| result.single_output::<i32>())
        .expect_err("Flow must not complete")
}

fn assert_failure(
    failure: SdkError,
    run_id: &str,
    expected_status: FlowStatus,
    expected_error_type: Option<FlowErrorType>,
    expected_message: Option<&str>,
    expected_result_count: usize,
) {
    match failure {
        SdkError::FlowUncompleted {
            run_id: failed_run_id,
            status,
            error_type,
            message,
            completions,
        } => {
            assert_eq!(run_id, failed_run_id);
            assert_eq!(expected_status, status);
            assert_eq!(expected_error_type, error_type);
            assert_eq!(expected_message, message.as_deref());
            assert_eq!(expected_result_count, completions.len());
        }
        error => panic!("expected FlowUncompleted, got {error:?}"),
    }
}

#[allow(dead_code)]
fn compile_workflow_uncompleted(client: &Client) -> SdkResult<()> {
    client.start_flow(
        &WorkflowUncompletedForceFailWorkflow::new(),
        "force-fail",
        5,
    )?;
    client.start_flow(
        &WorkflowUncompletedStateFailureWorkflow::new(),
        "worker-failure",
        5,
    )?;
    client.start_flow(
        &WorkflowUncompletedStateTimeoutWorkflow::new(),
        "worker-timeout",
        5,
    )?;
    client.start_flow(
        &WorkflowUncompletedEmptyDecisionWorkflow::new(),
        "empty-decision",
        5,
    )?;
    Ok(())
}
