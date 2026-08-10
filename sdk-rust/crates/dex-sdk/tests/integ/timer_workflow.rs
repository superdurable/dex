// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use std::time::Duration;

use dex_sdk::{Context, Flow, HandlerResult, Step, StepDecision, StepList, Timer, Wait};

pub(crate) struct TimerWorkflow {
    pub(crate) start: TimerStep,
}

impl TimerWorkflow {
    pub(crate) fn new() -> Self {
        Self { start: TimerStep }
    }
}

impl Flow for TimerWorkflow {
    type StartInput = u64;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }
}

pub(crate) struct TimerStep;

impl Step for TimerStep {
    type Input = u64;

    fn wait_for(&self, _context: &mut Context, input: u64) -> HandlerResult<Wait> {
        Ok(Wait::until(
            Timer::by_duration(Duration::from_secs(input)).with_id("test-timer-id"),
        ))
    }

    fn execute(&self, _context: &mut Context, _input: u64) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(()))
    }
}
