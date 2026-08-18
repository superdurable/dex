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

use dex_sdk::{Context, Flow, HandlerResult, StartFlowOptions, Step, StepDecision, StepList};

#[derive(Default)]
pub struct CronScheduleFlow {
    run: Run,
}

impl CronScheduleFlow {
    pub fn start_options() -> StartFlowOptions {
        StartFlowOptions::new().cron_schedule("0 0 * * * *")
    }
}

impl Flow for CronScheduleFlow {
    type StartInput = String;

    fn flow_type(&self) -> &'static str {
        "CronScheduleFlow"
    }

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.run)
    }
}

#[derive(Default)]
struct Run;

impl Step for Run {
    type Input = String;

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        context.record_event("cron-run", input)?;
        Ok(StepDecision::graceful_complete(()))
    }
}
