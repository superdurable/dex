// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dex_sdk::*;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::sync::LazyLock;

// dex:indexed-attribute attribute-key:status index-key:keyword1 index-type:keyword value-type:string description:"Case status"
static STATUS: LazyLock<Attribute<String>> = LazyLock::new(|| {
    Attribute::new("status").indexed(AttributeIndex::keyword().with_key("keyword1"))
});
static TITLE: LazyLock<Attribute<String>> = LazyLock::new(|| Attribute::new("title"));

const SUMMARY: Rpc<(), HashMap<String, String>> = Rpc::new("GetDexSummary");
const DISPLAY: Rpc<(), HashMap<String, String>> = Rpc::new("GetDexDisplay");

#[derive(Deserialize, Serialize)]
#[serde(rename_all = "lowercase")]
enum Priority {
    Standard,
    Urgent,
}

#[derive(Deserialize, Serialize)]
struct IntakeInput {
    priority: Priority,
    #[serde(rename = "note")]
    intake_note: Option<String>,
    labels: Option<Vec<String>>,
}

struct IntakeFlow {
    receive: Receive,
}

impl Flow for IntakeFlow {
    type StartInput = IntakeInput;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.receive)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&STATUS)
            .attribute(&TITLE)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function_without_input(SUMMARY, Self::summary)
            .function_without_input(DISPLAY, Self::display)
    }
}

impl IntakeFlow {
    // dex:field attribute-key:title value-type:string editable:false description:"Case title"
    fn summary(&self, _: &mut Context) -> HandlerResult<RpcResult<HashMap<String, String>>> {
        Ok(RpcResult::new(HashMap::from([(
            "title".to_owned(),
            "Intake".to_owned(),
        )])))
    }

    // dex:field attribute-key:status value-type:string editable:false description:"Case status"
    fn display(&self, _: &mut Context) -> HandlerResult<RpcResult<HashMap<String, String>>> {
        Ok(RpcResult::new(HashMap::from([(
            "status".to_owned(),
            "New".to_owned(),
        )])))
    }
}

// dex:group group-id:intake group-label:"Intake"
// dex:explanation text:"Receives a case."
struct Receive;

impl Step for Receive {
    type Input = IntakeInput;

    fn execute(&self, _: &mut Context, input: IntakeInput) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(input))
    }
}
