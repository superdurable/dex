// Copyright (c) 2026 Super Durable, Inc.
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

use std::sync::OnceLock;

use dex_sdk::{
    Context, Flow, HandlerResult, PersistenceSchema, Step, StepDecision, StepList, Stream,
};

pub fn progress() -> Stream<String> {
    static PROGRESS: OnceLock<Stream<String>> = OnceLock::new();
    PROGRESS
        .get_or_init(|| Stream::new("Progress", 10 * 1024 * 1024))
        .clone()
}

#[derive(Default)]
pub struct StreamFlow {
    render_preview: RenderPreview,
}

impl Flow for StreamFlow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.render_preview)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().stream(&progress())
    }
}

#[derive(Default)]
struct RenderPreview;

impl Step for RenderPreview {
    type Input = String;

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        progress().write(context, format!("Rendering preview for {input}"))?;
        Ok(StepDecision::graceful_complete(format!("Rendered {input}")))
    }
}
