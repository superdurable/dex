// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

struct Sample {
    start: Start,
}

impl Flow for Sample {
    fn steps(&self) -> StepList<'_, ()> {
        StepList::start(&self.start)
    }
}

// dex:group group-id:sample group-label:"Sample"
// dex:explanation text:"Waits and completes the Flow."
struct Start;

impl Step for Start {
    type Input = ();
    fn execute(&self, _context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        Ok(complete())
    }

    fn wait_for(&self, _context: &mut Context, _input: ()) -> HandlerResult<Wait> {
        Ok(wait_condition())
    }
}

fn complete() -> StepDecision {
    StepDecision::graceful_complete(())
}

fn wait_condition() -> Wait {
    Wait::skip_immediately()
}
