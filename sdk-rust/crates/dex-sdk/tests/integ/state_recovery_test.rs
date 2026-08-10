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

use dex_sdk::{Client, Registry, SdkResult};

use crate::state_recovery_no_wait_workflow::StateRecoveryNoWaitWorkflow;
use crate::state_recovery_workflow::StateRecoveryWorkflow;
use crate::support::{DexDevTestEnvironment, flow_id};

#[test]
#[ignore = "requires dexcli dev"]
fn test_state_api_fail_and_recovery_workflow() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(StateRecoveryWorkflow::new()));
    let workflow = StateRecoveryWorkflow::new();
    let flow_id = flow_id("state-recovery");
    environment
        .client
        .start_flow(&workflow, &flow_id, 5)
        .expect("start state-recovery Flow");
    assert_eq!(
        10,
        environment
            .client
            .wait_for_flow_with_timeout::<i32>(&flow_id, Duration::from_secs(30))
            .expect("complete state-recovery Flow")
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn test_state_api_fail_and_recovery_no_wait_until_workflow() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(StateRecoveryNoWaitWorkflow::new()));
    let workflow = StateRecoveryNoWaitWorkflow::new();
    let flow_id = flow_id("state-recovery-no-wait");
    environment
        .client
        .start_flow(&workflow, &flow_id, 5)
        .expect("start execute-only recovery Flow");
    assert_eq!(
        10,
        environment
            .client
            .wait_for_flow_with_timeout::<i32>(&flow_id, Duration::from_secs(30))
            .expect("complete execute-only recovery Flow")
    );
}

#[allow(dead_code)]
fn compile_wait_and_execute_recovery(client: &Client) -> SdkResult<()> {
    let workflow = StateRecoveryWorkflow::new();
    client.start_flow(&workflow, "state-recovery", 1)?;
    let output: i32 = client.wait_for_flow("state-recovery")?;
    let _ = output;
    Ok(())
}

#[allow(dead_code)]
fn compile_execute_only_recovery(client: &Client) -> SdkResult<()> {
    let workflow = StateRecoveryNoWaitWorkflow::new();
    client.start_flow(&workflow, "state-recovery-no-wait", 1)?;
    let output: i32 = client.wait_for_flow("state-recovery-no-wait")?;
    let _ = output;
    Ok(())
}
