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

use std::time::Duration;

use dex_sdk::{Context, Flow, HandlerResult, Step, StepDecision, StepList, StepOptions};

#[derive(Default)]
pub struct InterruptibleExecutionFlow {
    run_until_cancelled: RunUntilCancelled,
}

impl Flow for InterruptibleExecutionFlow {
    type StartInput = ();

    fn flow_type(&self) -> &'static str {
        "InterruptibleExecutionFlow"
    }

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.run_until_cancelled)
    }
}

#[derive(Default)]
struct RunUntilCancelled;

impl Step for RunUntilCancelled {
    type Input = ();

    fn execute(&self, context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        context.wait_for_cancellation();
        Ok(StepDecision::graceful_complete("cancelled".to_string()))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_method_timeout(Duration::from_secs(30))
    }
}
