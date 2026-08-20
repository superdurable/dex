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
    Channel, Context, Flow, HandlerResult, PersistenceSchema, Step, StepDecision, StepList, Wait,
};

fn approval() -> Channel<String> {
    Channel::new("Approval")
}

#[derive(Default)]
pub struct StepExecutionLocalFlow {
    note_wait: NoteWaitStep,
}

impl Flow for StepExecutionLocalFlow {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.note_wait)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().channel(&approval())
    }
}

#[derive(Default)]
struct NoteWaitStep;

impl Step for NoteWaitStep {
    type Input = i32;

    fn wait_for(&self, context: &mut Context, input: Self::Input) -> HandlerResult<Wait> {
        context.set_step_execution_local("note", format!("approval:{input}"))?;
        Ok(Wait::until(approval().for_one()))
    }

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        let note: String = context.step_execution_local("note")?;
        Ok(StepDecision::graceful_complete(note))
    }
}
