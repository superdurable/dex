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
    Attribute, AttributeIndex, AttributeMap, Context, Flow, FlowConfig, HandlerResult,
    PersistenceSchema, Step, StepDecision, StepList, StepOptions, Wait,
};

pub fn attribute_store_config() -> FlowConfig {
    FlowConfig::new().attribute_store_name("profiles")
}

pub struct AttributeFlow {
    status: Attribute<String>,
    email: Attribute<String>,
    progress: AttributeMap<String>,
    start: AttributeStep,
}

impl Default for AttributeFlow {
    fn default() -> Self {
        let status = Attribute::new("primitive-attribute-status")
            .indexed(AttributeIndex::keyword().with_key("order-status"));
        let email = Attribute::new("primitive-attribute-email").sync_to_attribute_store();
        let progress = AttributeMap::new("primitive-attribute-progress")
            .indexed(AttributeIndex::keyword().with_key("order-progress"));
        Self {
            start: AttributeStep {
                status: status.clone(),
                progress: progress.clone(),
            },
            status,
            email,
            progress,
        }
    }
}

impl Flow for AttributeFlow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&self.status)
            .attribute(&self.email)
            .attribute_map(&self.progress)
    }
}

struct AttributeStep {
    status: Attribute<String>,
    progress: AttributeMap<String>,
}

impl Step for AttributeStep {
    type Input = String;

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_lock(self.status.lock())
    }

    fn wait_for(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        self.status.set(context, "processing".to_owned())?;
        self.progress
            .set(context, "payment", "authorized".to_owned())?;
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        self.status.set(context, "completed".to_owned())?;
        Ok(StepDecision::graceful_complete(input))
    }
}
