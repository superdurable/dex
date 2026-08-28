// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use std::sync::LazyLock;

use dex_sdk::{
    Attribute, Channel, Context, Flow, HandlerError, HandlerResult, PersistenceSchema, Step,
    StepDecision, StepList, StepMovement, StepOptions, Wait,
};

static WAIT_FOR_COUNT: LazyLock<Attribute<i32>> =
    LazyLock::new(|| Attribute::new("step-lock-wait-for-count"));
static EXECUTE_COUNT: LazyLock<Attribute<i32>> =
    LazyLock::new(|| Attribute::new("step-lock-execute-count"));
static COMPLETED: LazyLock<Channel<()>> = LazyLock::new(|| Channel::new("step-lock-completed"));

pub(crate) struct StateOptionsLockingWorkflow {
    pub(crate) wait_for_count: Attribute<i32>,
    pub(crate) execute_count: Attribute<i32>,
    completed: Channel<()>,
    start: StartStep,
    locked: LockedStep,
    complete: CompleteStep,
}

impl StateOptionsLockingWorkflow {
    pub(crate) fn new() -> Self {
        let wait_for_count = WAIT_FOR_COUNT.clone();
        let execute_count = EXECUTE_COUNT.clone();
        let completed = COMPLETED.clone();
        Self {
            start: StartStep,
            locked: LockedStep {
                wait_for_count: wait_for_count.clone(),
                execute_count: execute_count.clone(),
                completed: completed.clone(),
            },
            complete: CompleteStep {
                wait_for_count: wait_for_count.clone(),
                execute_count: execute_count.clone(),
                completed: completed.clone(),
            },
            wait_for_count,
            execute_count,
            completed,
        }
    }
}

impl Flow for StateOptionsLockingWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
            .and(&self.locked)
            .and(&self.complete)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&self.wait_for_count)
            .attribute(&self.execute_count)
            .channel(&self.completed)
    }
}

struct StartStep;

impl Step for StartStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, parallelism: i32) -> HandlerResult<StepDecision> {
        let mut movements = (0..parallelism)
            .map(|index| {
                StepMovement::to(
                    &LockedStep {
                        wait_for_count: WAIT_FOR_COUNT.clone(),
                        execute_count: EXECUTE_COUNT.clone(),
                        completed: COMPLETED.clone(),
                    },
                    index,
                )
            })
            .collect::<Vec<_>>();
        movements.push(StepMovement::to(
            &CompleteStep {
                wait_for_count: WAIT_FOR_COUNT.clone(),
                execute_count: EXECUTE_COUNT.clone(),
                completed: COMPLETED.clone(),
            },
            parallelism,
        ));
        Ok(StepDecision::go_to_many(movements))
    }
}

struct LockedStep {
    wait_for_count: Attribute<i32>,
    execute_count: Attribute<i32>,
    completed: Channel<()>,
}

impl Step for LockedStep {
    type Input = i32;

    fn wait_for(&self, context: &mut Context, _input: i32) -> HandlerResult<Wait> {
        let next = self.wait_for_count.get(context)?.unwrap_or_default() + 1;
        self.wait_for_count.set(context, next)?;
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, context: &mut Context, _input: i32) -> HandlerResult<StepDecision> {
        let next = self.execute_count.get(context)?.unwrap_or_default() + 1;
        self.execute_count.set(context, next)?;
        self.completed.publish(context, ())?;
        Ok(StepDecision::dead_end())
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .wait_for_lock(self.wait_for_count.lock())
            .execute_lock(self.execute_count.lock())
    }
}

struct CompleteStep {
    wait_for_count: Attribute<i32>,
    execute_count: Attribute<i32>,
    completed: Channel<()>,
}

impl Step for CompleteStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, parallelism: i32) -> HandlerResult<Wait> {
        Ok(Wait::until(self.completed.for_n(parallelism as usize)))
    }

    fn execute(&self, context: &mut Context, parallelism: i32) -> HandlerResult<StepDecision> {
        if self.completed.condition_results(context)?.len() != parallelism as usize {
            return Err(HandlerError::new(
                "StateOptionsLockingFailure",
                "not all locked Steps completed",
            ));
        }
        Ok(StepDecision::graceful_complete(format!(
            "{}:{}",
            self.wait_for_count.get_required(context)?,
            self.execute_count.get_required(context)?
        )))
    }
}
