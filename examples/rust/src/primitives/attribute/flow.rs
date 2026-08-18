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

use dex_sdk::{
    Attribute, Context, Flow, HandlerResult, PersistenceSchema, Step, StepDecision, StepList, Wait,
};

fn message() -> Attribute<String> {
    Attribute::new("primitive-attribute-message")
}

#[derive(Default)]
pub struct AttributeFlow {
    start: AttributeStep,
}

impl Flow for AttributeFlow {
    type StartInput = String;

    fn flow_type(&self) -> &'static str {
        "AttributeFlow"
    }

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().attribute(&message())
    }
}

#[derive(Default)]
struct AttributeStep;

impl Step for AttributeStep {
    type Input = String;

    fn wait_for(&self, context: &mut Context, input: Self::Input) -> HandlerResult<Wait> {
        message().set(context, input)?;
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(message().get_required(context)?))
    }
}
