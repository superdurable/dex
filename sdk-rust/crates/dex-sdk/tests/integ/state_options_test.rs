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

use crate::state_options_locking_workflow::{
    EXECUTE_COUNT, StateOptionsLockingWorkflow, WAIT_FOR_COUNT,
};
use crate::state_options_workflow::StateOptionsWorkflow;
use crate::support::{DexDevTestEnvironment, flow_id};

#[test]
#[ignore = "requires dexcli dev"]
fn test_state_options_workflow() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(StateOptionsWorkflow::new()));
    let workflow = StateOptionsWorkflow::new();
    let flow_id = flow_id("state-options");
    environment
        .client
        .start_flow(&workflow, &flow_id, ())
        .expect("start state-options Flow");
    assert_eq!(
        "success",
        environment
            .client
            .wait_for_flow_with_timeout(&flow_id, Duration::from_secs(30))
            .and_then(|result| result.single_output::<String>())
            .expect("complete state-options Flow")
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn test_wait_for_and_execute_locks_serialize_parallel_steps() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(StateOptionsLockingWorkflow::new()));
    let workflow = StateOptionsLockingWorkflow::new();
    let flow_id = flow_id("state-options-locks");
    let parallelism = 20;
    environment
        .client
        .start_flow(&workflow, &flow_id, parallelism)
        .expect("start step-locking Flow");
    assert_eq!(
        "20:20",
        environment
            .client
            .wait_for_flow_with_timeout(&flow_id, Duration::from_secs(30))
            .and_then(|result| result.single_output::<String>())
            .expect("complete step-locking Flow")
    );
    assert_eq!(
        Some(parallelism),
        environment
            .client
            .get_attribute(&flow_id, &WAIT_FOR_COUNT)
            .expect("read waitFor count")
    );
    assert_eq!(
        Some(parallelism),
        environment
            .client
            .get_attribute(&flow_id, &EXECUTE_COUNT)
            .expect("read execute count")
    );
}

#[allow(dead_code)]
fn compile_step_locks(client: &Client) -> SdkResult<()> {
    let workflow = StateOptionsLockingWorkflow::new();
    client.start_flow(&workflow, "state-options-locks", 10)?;
    let output: String = client
        .wait_for_flow("state-options-locks")?
        .single_output()?;
    drop(output);
    Ok(())
}
