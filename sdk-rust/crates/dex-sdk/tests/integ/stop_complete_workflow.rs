// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use std::sync::{Arc, Condvar, Mutex};
use std::time::Duration;

use dex_sdk::{Context, Flow, HandlerResult, Step, StepDecision, StepList};

/// A one-shot latch shared by the test and the blocking Step.
#[derive(Clone, Default)]
pub(crate) struct StopCompleteLatch {
    state: Arc<(Mutex<bool>, Condvar)>,
}

impl StopCompleteLatch {
    pub(crate) fn open(&self) {
        let (is_open, changed) = &*self.state;
        *is_open.lock().expect("latch lock") = true;
        changed.notify_all();
    }

    pub(crate) fn wait(&self, timeout: Duration) -> bool {
        let (is_open, changed) = &*self.state;
        let guard = is_open.lock().expect("latch lock");
        let (guard, _) = changed
            .wait_timeout_while(guard, timeout, |is_open| !*is_open)
            .expect("latch wait");
        *guard
    }
}

pub(crate) struct StopCompleteWorkflow {
    start: StopCompleteBlockingStep,
}

impl StopCompleteWorkflow {
    pub(crate) fn new(
        execute_started: StopCompleteLatch,
        release_execute: StopCompleteLatch,
    ) -> Self {
        Self {
            start: StopCompleteBlockingStep {
                execute_started,
                release_execute,
            },
        }
    }
}

impl Flow for StopCompleteWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }
}

struct StopCompleteBlockingStep {
    execute_started: StopCompleteLatch,
    release_execute: StopCompleteLatch,
}

impl Step for StopCompleteBlockingStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, _input: i32) -> HandlerResult<StepDecision> {
        self.execute_started.open();
        self.release_execute.wait(Duration::from_secs(20));
        Ok(StepDecision::dead_end())
    }
}
