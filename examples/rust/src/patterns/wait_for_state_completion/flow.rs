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

/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

use dex_sdk::{
    Attribute, Context, Flow, HandlerResult, PersistenceSchema, Step, StepDecision,
    StepExecutionId, StepList, StepMovement,
};
use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct PersistRequest {
    pub record_id: String,
    pub payload: String,
}

#[derive(Default)]
pub struct WaitForStateCompletionFlow {
    persist_data: PersistData,
    background_work: BackgroundWork,
}

impl WaitForStateCompletionFlow {
    pub fn persisted_step() -> StepExecutionId {
        StepExecutionId::of(&PersistData)
    }
}

impl Flow for WaitForStateCompletionFlow {
    type StartInput = PersistRequest;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.persist_data).and(&self.background_work)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().attribute(&record())
    }
}

#[derive(Default)]
struct PersistData;

impl Step for PersistData {
    type Input = PersistRequest;

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        record().set(context, input.clone())?;
        context.record_event("client-visible-persisted", input.record_id)?;
        Ok(StepDecision::go_to_many([StepMovement::to(
            &BackgroundWork,
            input.payload,
        )]))
    }
}

#[derive(Default)]
struct BackgroundWork;

impl Step for BackgroundWork {
    type Input = String;

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        context.record_event("background-operation", input)?;
        Ok(StepDecision::graceful_complete(()))
    }
}

fn record() -> Attribute<PersistRequest> {
    Attribute::new("wait-for-state-record")
}
