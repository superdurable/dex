// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use std::time::Duration;

use dex_sdk::{FlowResult, FlowStatus, Registry, SdkError, StopFlowOptions};

use crate::stop_complete_workflow::{StopCompleteLatch, StopCompleteWorkflow};
use crate::support::{DexDevTestEnvironment, flow_id};

const STOP_OUTPUT: &str = "completed by StopFlow";

struct StopCompleteFixture {
    environment: DexDevTestEnvironment,
    workflow: StopCompleteWorkflow,
    execute_started: StopCompleteLatch,
    release_execute: StopCompleteLatch,
}

impl StopCompleteFixture {
    fn start() -> Self {
        let execute_started = StopCompleteLatch::default();
        let release_execute = StopCompleteLatch::default();
        let environment = DexDevTestEnvironment::start(Registry::new().register(
            StopCompleteWorkflow::new(execute_started.clone(), release_execute.clone()),
        ));
        Self {
            environment,
            workflow: StopCompleteWorkflow::new(execute_started.clone(), release_execute.clone()),
            execute_started,
            release_execute,
        }
    }

    fn start_blocked_flow(&self, prefix: &str) -> String {
        let flow_id = flow_id(prefix);
        self.environment
            .client
            .start_flow(&self.workflow, &flow_id, 1)
            .expect("start blocking Flow");
        assert!(self.execute_started.wait(Duration::from_secs(10)));
        flow_id
    }

    fn describe_status(&self, flow_id: &str) -> FlowStatus {
        self.environment
            .client
            .describe_flow(flow_id)
            .expect("describe Flow")
            .status
    }
}

#[test]
#[ignore = "requires dexcli dev"]
fn test_stop_flow_force_complete_cancels_active_step() {
    let fixture = StopCompleteFixture::start();
    let flow_id = fixture.start_blocked_flow("rust-force-complete");

    fixture
        .environment
        .client
        .stop_flow(
            &flow_id,
            StopFlowOptions::force_complete().output(STOP_OUTPUT.to_string()),
        )
        .expect("force complete Flow");

    let result = fixture
        .environment
        .client
        .wait_for_flow_with_timeout(&flow_id, Duration::from_secs(15))
        .expect("wait for Flow");
    fixture.release_execute.open();
    assert_only_stop_output(&result);
}

#[test]
#[ignore = "requires dexcli dev"]
fn test_stop_flow_graceful_complete_waits_for_active_step() {
    let fixture = StopCompleteFixture::start();
    let flow_id = fixture.start_blocked_flow("rust-graceful-complete");

    fixture
        .environment
        .client
        .stop_flow(
            &flow_id,
            StopFlowOptions::graceful_complete().output(STOP_OUTPUT.to_string()),
        )
        .expect("gracefully complete Flow");

    assert_eq!(FlowStatus::Running, fixture.describe_status(&flow_id));
    fixture.release_execute.open();
    let result = fixture
        .environment
        .client
        .wait_for_flow_with_timeout(&flow_id, Duration::from_secs(15))
        .expect("wait for Flow");
    assert_only_stop_output(&result);
}

#[test]
#[ignore = "requires dexcli dev"]
fn test_stop_flow_rejects_output_with_cancel_before_sending() {
    let fixture = StopCompleteFixture::start();
    let flow_id = fixture.start_blocked_flow("rust-cancel-output");

    match fixture
        .environment
        .client
        .stop_flow(
            &flow_id,
            StopFlowOptions::cancel().output(STOP_OUTPUT.to_string()),
        )
        .expect_err("cancel with an output must fail")
    {
        SdkError::InvalidArgument { message } => {
            assert!(message.contains("StopFlowOptions::output requires"));
        }
        error => panic!("expected InvalidArgument, got {error:?}"),
    }
    assert_eq!(FlowStatus::Running, fixture.describe_status(&flow_id));
    fixture.release_execute.open();
}

fn assert_only_stop_output(result: &FlowResult) {
    assert_eq!(FlowStatus::Completed, result.status());
    assert_eq!(1, result.completions().len());
    let completion = &result.completions()[0];
    assert_eq!("", completion.step_type);
    assert_eq!("", completion.step_execution_id);
    assert_eq!(
        STOP_OUTPUT,
        completion.decode::<String>().expect("decode stop output")
    );
}
