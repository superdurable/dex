// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use dex_sdk::{Flow, Registry, SdkError, StepList};

struct EmptyFlow;

impl Flow for EmptyFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::empty()
    }
}

#[test]
fn fallible_registration_returns_flow_definition_error() {
    let registry = Registry::new()
        .try_register(EmptyFlow)
        .expect("register first Flow");
    let duplicate = registry.try_register(EmptyFlow);
    assert!(matches!(duplicate, Err(SdkError::FlowDefinition { .. })));
}
