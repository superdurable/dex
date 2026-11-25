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

use dex_sdk::{Flow, Registry};

use crate::non_retryable_workflow::{
    ATTEMPTS_BY_FLOW_ID, NON_RETRYABLE_DETAIL, NON_RETRYABLE_ERROR_TYPE,
    NonRetryableExecuteWorkflow, NonRetryableWaitForWorkflow,
};
use crate::support::{DexDevTestEnvironment, flow_id};

/// Far below the hourly retry interval: completing in time proves no retry ran.
const WAIT_TIMEOUT: Duration = Duration::from_secs(30);

#[test]
#[ignore = "requires dexcli dev"]
fn test_non_retryable_execute_failure_skips_retries_and_proceeds() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(NonRetryableExecuteWorkflow::new()));
    assert_recovered_after_one_attempt(
        &environment,
        &NonRetryableExecuteWorkflow::new(),
        "non-retryable-execute",
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn test_non_retryable_wait_for_failure_skips_retries_and_proceeds() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(NonRetryableWaitForWorkflow::new()));
    assert_recovered_after_one_attempt(
        &environment,
        &NonRetryableWaitForWorkflow::new(),
        "non-retryable-wait-for",
    );
}

fn assert_recovered_after_one_attempt<W: Flow<StartInput = String>>(
    environment: &DexDevTestEnvironment,
    workflow: &W,
    prefix: &str,
) {
    let flow_id = flow_id(prefix);
    environment
        .client
        .start_flow(workflow, &flow_id, "order".to_string())
        .expect("start non-retryable Flow");
    let result = environment
        .client
        .wait_for_flow_with_timeout(&flow_id, WAIT_TIMEOUT)
        .expect("complete non-retryable Flow");
    assert_eq!(
        format!("{NON_RETRYABLE_ERROR_TYPE}: {NON_RETRYABLE_DETAIL}"),
        result.single_output::<String>().expect("recovery output")
    );
    assert_eq!(
        Some(&1),
        ATTEMPTS_BY_FLOW_ID
            .lock()
            .expect("attempt counter lock")
            .get(&flow_id)
    );
}
