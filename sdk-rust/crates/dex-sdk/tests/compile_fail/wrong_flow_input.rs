// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use dex_sdk::{Client, Flow, StepList};

#[derive(serde::Deserialize, serde::Serialize)]
struct ModelInput {
    value: i32,
}

struct ModelInputFlow;

impl Flow for ModelInputFlow {
    type StartInput = ModelInput;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::empty()
    }
}

fn start_with_wrong_input(client: &Client, flow: &ModelInputFlow) {
    client.start_flow(flow, "wrong-input", "wrong").unwrap();
}

fn main() {}
