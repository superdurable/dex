// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use dex_core::{FlowSpec, PersistenceKind, PersistenceSpec, Registry, RpcSpec, StepSpec};

#[test]
fn assembles_and_resolves_language_neutral_definitions() {
    let registry = Registry::new(vec![FlowSpec::new(
        "Orders",
        vec![
            StepSpec::starting("Validate"),
            StepSpec::non_starting("Ship"),
        ],
        vec![RpcSpec::new("GetOrder")],
        vec![
            PersistenceSpec::new("status", PersistenceKind::Attribute),
            PersistenceSpec::new("items", PersistenceKind::AttributeMap),
            PersistenceSpec::new("commands", PersistenceKind::Channel),
        ],
    )])
    .expect("valid Registry");

    let orders = registry.flow("Orders").expect("Orders Flow");
    assert_eq!(registry.len(), 1);
    assert_eq!(
        orders.starting_step().expect("starting Step").name(),
        "Validate"
    );
    assert!(orders.step("Ship").is_some());
    assert!(orders.rpc("GetOrder").is_some());
    assert!(
        orders
            .persistence("items", PersistenceKind::AttributeMap)
            .is_some()
    );
}

#[test]
fn rejects_duplicate_and_invalid_definitions_atomically() {
    let duplicate_steps = Registry::new(vec![FlowSpec::new(
        "Orders",
        vec![StepSpec::starting("Run"), StepSpec::non_starting("Run")],
        vec![],
        vec![],
    )]);
    assert_eq!(
        duplicate_steps
            .expect_err("duplicate Steps must fail")
            .to_string(),
        "duplicate Step durable name \"Run\""
    );

    let multiple_starts = Registry::new(vec![FlowSpec::new(
        "Orders",
        vec![StepSpec::starting("One"), StepSpec::starting("Two")],
        vec![],
        vec![],
    )]);
    assert_eq!(
        multiple_starts
            .expect_err("multiple starts must fail")
            .to_string(),
        "Flow \"Orders\" has multiple starting Steps"
    );

    let whitespace = Registry::new(vec![FlowSpec::new(" Orders", vec![], vec![], vec![])]);
    assert!(whitespace.is_err());
}
