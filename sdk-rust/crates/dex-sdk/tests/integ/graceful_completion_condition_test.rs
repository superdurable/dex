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

use dex_sdk::{FlowStatus, Registry, StopFlowOptions};

use crate::graceful_completion_condition_workflow::{
    GracefulConditionProbe, GracefulConditionWorkflow, GracefulReaderObservation,
};
use crate::support::{DexDevTestEnvironment, flow_id};

struct GracefulConditionFixture {
    environment: DexDevTestEnvironment,
    workflow: GracefulConditionWorkflow,
    probe: GracefulConditionProbe,
}

impl GracefulConditionFixture {
    fn start(probe: GracefulConditionProbe) -> Self {
        let environment = DexDevTestEnvironment::start(
            Registry::new().register(GracefulConditionWorkflow::new(probe.clone())),
        );
        Self {
            environment,
            workflow: GracefulConditionWorkflow::new(GracefulConditionProbe::default()),
            probe,
        }
    }

    fn start_flow(&self, prefix: &str) -> String {
        let flow_id = flow_id(prefix);
        self.environment
            .client
            .start_flow(&self.workflow, &flow_id, ())
            .expect("start graceful-condition Flow");
        flow_id
    }

    fn stop_gracefully(&self, flow_id: &str) {
        self.environment
            .client
            .stop_flow(flow_id, StopFlowOptions::graceful_complete())
            .expect("gracefully complete Flow");
    }

    fn wait_for_completed(&self, flow_id: &str) {
        let result = self
            .environment
            .client
            .wait_for_flow_with_timeout(flow_id, Duration::from_secs(20))
            .expect("wait for Flow");
        assert_eq!(FlowStatus::Completed, result.status());
    }
}

#[test]
#[ignore = "requires dexcli dev"]
fn test_graceful_stop_wakes_a_step_waiting_on_the_graceful_completion_condition() {
    let fixture = GracefulConditionFixture::start(GracefulConditionProbe::default());
    let flow_id = fixture.start_flow("rust-graceful-condition");

    fixture.stop_gracefully(&flow_id);

    fixture.wait_for_completed(&flow_id);
    assert_eq!(
        vec![GracefulReaderObservation {
            messages: Vec::new(),
            is_graceful_completed: true,
        }],
        fixture.probe.observations()
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn test_message_published_before_graceful_stop_is_consumed_first() {
    let fixture =
        GracefulConditionFixture::start(GracefulConditionProbe::blocking_first_wait_for());
    let flow_id = fixture.start_flow("rust-graceful-condition-message");
    assert!(fixture.probe.wait_for_started.wait(Duration::from_secs(10)));

    fixture
        .environment
        .client
        .invoke_rpc(
            &flow_id,
            GracefulConditionWorkflow::PUBLISH_MESSAGE,
            "hello".to_string(),
        )
        .expect("publish message");
    fixture.stop_gracefully(&flow_id);
    assert_eq!(
        FlowStatus::Running,
        fixture
            .environment
            .client
            .describe_flow(&flow_id)
            .expect("describe Flow")
            .status
    );
    fixture.probe.release_wait_for.open();

    fixture.wait_for_completed(&flow_id);
    assert_eq!(
        vec![
            GracefulReaderObservation {
                messages: vec!["hello".to_string()],
                is_graceful_completed: true,
            },
            GracefulReaderObservation {
                messages: Vec::new(),
                is_graceful_completed: true,
            },
        ],
        fixture.probe.observations()
    );
}
