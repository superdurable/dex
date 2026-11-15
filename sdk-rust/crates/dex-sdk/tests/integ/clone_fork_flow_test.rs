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

use dex_sdk::{
    CloneForkFlowOptions, FlowStatus, Registry, StopFlowOptions, SubFlowResetPolicy,
    TimeTravelOptions,
};

use crate::clone_fork_flow_workflow::{CLONE_FORK_LABEL, RustCloneForkSessionWorkflow};
use crate::flow_service_client::await_wait_for_completed_event_id;
use crate::support::{DexDevTestEnvironment, flow_id};

#[test]
#[ignore = "requires dexcli dev"]
fn test_clone_fork_flow_copies_running_source_at_current_and_event_id_points() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(RustCloneForkSessionWorkflow::new()));
    let workflow = RustCloneForkSessionWorkflow::new();
    let source_id = flow_id("clone-fork-source");
    let source_run_id = environment
        .client
        .start_flow(&workflow, &source_id, "source".to_string())
        .expect("start source Flow");
    let wait_event_id = await_wait_for_completed_event_id(&source_id, &source_run_id);

    let current_id = flow_id("clone-fork-current");
    let current = environment
        .client
        .clone_fork_flow(
            &workflow,
            &source_id,
            &current_id,
            CloneForkFlowOptions::new()
                .initial_attribute(&CLONE_FORK_LABEL, "current".to_string())
                .reason("branch at the current point"),
        )
        .expect("clone fork at the current point");
    assert!(!current.run_id.is_empty());
    assert_eq!(current.source_run_id, source_run_id);
    assert_eq!(current.source_run_attempt, 1);
    assert!(current.source_event_id >= wait_event_id);

    let event_id = flow_id("clone-fork-event");
    let event = environment
        .client
        .clone_fork_flow(
            &workflow,
            &source_id,
            &event_id,
            CloneForkFlowOptions::from_event_id(wait_event_id).source_run_id(&source_run_id),
        )
        .expect("clone fork at an event ID");
    assert_eq!(event.source_run_id, source_run_id);
    assert_eq!(event.source_event_id, wait_event_id);

    environment
        .client
        .invoke_rpc::<String, ()>(
            &current_id,
            RustCloneForkSessionWorkflow::RELEASE_SESSION,
            "a".to_string(),
        )
        .expect("release current clone");
    environment
        .client
        .invoke_rpc::<String, ()>(
            &event_id,
            RustCloneForkSessionWorkflow::RELEASE_SESSION,
            "b".to_string(),
        )
        .expect("release event clone");
    assert_eq!(completed_output(&environment, &current_id), "current|a");
    assert_eq!(completed_output(&environment, &event_id), "source|b");

    let source = environment
        .client
        .describe_flow(&source_id)
        .expect("describe source Flow");
    assert_eq!(source.status, FlowStatus::Running);
    assert_eq!(source.run_id, source_run_id);
    environment
        .client
        .stop_flow(&source_id, StopFlowOptions::terminate())
        .expect("terminate source Flow");
}

#[test]
#[ignore = "requires dexcli dev"]
fn test_time_travel_by_event_id_returns_to_the_waiting_point() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(RustCloneForkSessionWorkflow::new()));
    let workflow = RustCloneForkSessionWorkflow::new();
    let flow_id = flow_id("time-travel-event-id");
    let run_id = environment
        .client
        .start_flow(&workflow, &flow_id, "first".to_string())
        .expect("start Flow");
    let wait_event_id = await_wait_for_completed_event_id(&flow_id, &run_id);
    environment
        .client
        .invoke_rpc::<String, ()>(
            &flow_id,
            RustCloneForkSessionWorkflow::RELEASE_SESSION,
            "a".to_string(),
        )
        .expect("release Flow");
    assert_eq!(completed_output(&environment, &flow_id), "first|a");

    environment
        .client
        .time_travel(
            &flow_id,
            TimeTravelOptions::from_event_id(wait_event_id)
                .skip_writes_reapply(true)
                .sub_flow_reset_policy(SubFlowResetPolicy::AttachElseReject)
                .reason("answer again"),
        )
        .expect("time travel to the event ID");
    environment
        .client
        .invoke_rpc::<String, ()>(
            &flow_id,
            RustCloneForkSessionWorkflow::RELEASE_SESSION,
            "b".to_string(),
        )
        .expect("release Flow again");
    assert_eq!(completed_output(&environment, &flow_id), "first|b");
}

fn completed_output(environment: &DexDevTestEnvironment, flow_id: &str) -> String {
    let result = environment
        .client
        .wait_for_flow_with_timeout(flow_id, Duration::from_secs(30))
        .expect("wait for Flow");
    assert_eq!(result.status(), FlowStatus::Completed);
    result.single_output().expect("decode Flow output")
}
