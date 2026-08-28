// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

use std::sync::LazyLock;

use dex_sdk::{
    Attribute, AttributeIndex, Context, Flow, HandlerResult, PersistenceSchema, Step, StepDecision,
    StepList,
};

const KEYWORD_KEY: &str = "CustomKeywordField";

static KEYWORD: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new(KEYWORD_KEY).indexed(AttributeIndex::keyword()));

#[derive(Default)]
pub struct ClientApisFlow {
    index: ClientApisStep,
}

impl Flow for ClientApisFlow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.index)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().attribute(&KEYWORD)
    }
}

#[derive(Default)]
struct ClientApisStep;

impl Step for ClientApisStep {
    type Input = String;

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        KEYWORD.set(context, input.clone())?;
        Ok(StepDecision::graceful_complete(input))
    }
}
