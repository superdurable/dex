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
// dex:explanation text:"Selects the next Step."
struct Start;

impl Step for Start {
    type Input = ();
    fn execute(&self, _context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(&select_step(), ()))
    }
}
