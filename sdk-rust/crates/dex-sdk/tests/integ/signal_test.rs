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

use dex_sdk::{Client, GrpcCode, Registry, SdkError, SdkResult, StepExecutionId, TimerId};

use crate::signal_workflow::{FIRST, SignalWorkflow, THIRD};
use crate::support::{DexDevTestEnvironment, flow_id, skip_timer_when_pending};

#[test]
#[ignore = "requires dexcli dev"]
fn test_basic_signal_workflow() {
    let environment = DexDevTestEnvironment::start(Registry::new().register(SignalWorkflow::new()));
    let workflow = SignalWorkflow::new();
    let flow_id = flow_id("basic-signal");
    environment
        .client
        .start_flow(&workflow, &flow_id, 1)
        .expect("start signal Flow");
    environment
        .client
        .publish_many(&flow_id, &FIRST, [2, 3, 5])
        .expect("publish first signals");
    environment
        .client
        .publish(&flow_id, &THIRD, ())
        .expect("publish null signal");
    environment
        .client
        .publish_map(&flow_id, &workflow.signal_map, "one", [4])
        .expect("publish mapped signal");
    skip_timer_when_pending(
        &environment.client,
        &flow_id,
        StepExecutionId::of(&workflow.combination),
        TimerId::by_condition_id("test-timer-id"),
    );
    assert_eq!(
        6,
        environment
            .client
            .wait_for_flow_with_timeout(&flow_id, Duration::from_secs(30))
            .and_then(|result| result.single_output::<i32>())
            .expect("complete signal Flow")
    );
    match environment
        .client
        .publish(&flow_id, &FIRST, 8)
        .expect_err("publishing to a closed Flow must fail")
    {
        SdkError::FlowNotActive { service } => {
            assert_eq!(GrpcCode::NotFound, service.code())
        }
        error => panic!("expected FlowNotActive, got {error:?}"),
    }
}

#[allow(dead_code)]
fn compile_signals_and_timer_skip(client: &Client) -> SdkResult<()> {
    let workflow = SignalWorkflow::new();
    client.start_flow(&workflow, "signal", 1)?;
    client.publish_many("signal", &FIRST, [2, 3, 5])?;
    client.publish("signal", &THIRD, ())?;
    client.publish_map("signal", &workflow.signal_map, "one", [4])?;
    client.skip_timer(
        "signal",
        StepExecutionId::of(&workflow.combination),
        TimerId::by_condition_id("test-timer-id"),
    )?;
    let _: i32 = client.wait_for_flow("signal")?.single_output()?;
    Ok(())
}
