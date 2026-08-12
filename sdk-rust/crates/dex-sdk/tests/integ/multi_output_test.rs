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

use dex_sdk::{Registry, Step};

use crate::multi_output_workflow::MultiOutputWorkflow;
use crate::support::{DexDevTestEnvironment, flow_id};

#[test]
#[ignore = "requires dexcli dev"]
fn parallel_branches_return_heterogeneous_step_completions() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(MultiOutputWorkflow::new()));
    let workflow = MultiOutputWorkflow::new();
    let flow_id = flow_id("multi-output");
    environment
        .client
        .start_flow(&workflow, &flow_id, ())
        .expect("start multi-output Flow");
    let result = environment
        .client
        .wait_for_flow_with_timeout(&flow_id, Duration::from_secs(30))
        .expect("wait for multi-output Flow");

    assert_eq!(result.completions().len(), 2);
    let string_completion = result
        .completions()
        .iter()
        .find(|completion| completion.step_type == workflow.string_step.step_type())
        .expect("string completion");
    assert_eq!(
        string_completion.decode::<String>().expect("decode string"),
        "branch-one"
    );
    let integer_completion = result
        .completions()
        .iter()
        .find(|completion| completion.step_type == workflow.integer_step.step_type())
        .expect("integer completion");
    assert_eq!(
        integer_completion.decode::<i32>().expect("decode integer"),
        42
    );
    assert!(
        result
            .completions()
            .iter()
            .all(|completion| !completion.step_execution_id.is_empty())
    );
}
