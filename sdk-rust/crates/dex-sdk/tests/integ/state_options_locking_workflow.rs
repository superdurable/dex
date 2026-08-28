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

pub(crate) static WAIT_FOR_COUNT: LazyLock<Attribute<i32>> =
    LazyLock::new(|| Attribute::new("step-lock-wait-for-count"));
pub(crate) static EXECUTE_COUNT: LazyLock<Attribute<i32>> =
    LazyLock::new(|| Attribute::new("step-lock-execute-count"));
static COMPLETED: LazyLock<Channel<()>> = LazyLock::new(|| Channel::new("step-lock-completed"));

pub(crate) struct StateOptionsLockingWorkflow {
    start: StartStep,
    locked: LockedStep,
    complete: CompleteStep,
}

impl StateOptionsLockingWorkflow {
    pub(crate) fn new() -> Self {
        Self {
            start: StartStep,
            locked: LockedStep,
            complete: CompleteStep,
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
            .attribute(&WAIT_FOR_COUNT)
            .attribute(&EXECUTE_COUNT)
            .channel(&COMPLETED)
    }
}

struct StartStep;

impl Step for StartStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, parallelism: i32) -> HandlerResult<StepDecision> {
        let mut movements = (0..parallelism)
            .map(|index| StepMovement::to(&LockedStep, index))
            .collect::<Vec<_>>();
        movements.push(StepMovement::to(&CompleteStep, parallelism));
        Ok(StepDecision::go_to_many(movements))
    }
}

struct LockedStep;

impl Step for LockedStep {
    type Input = i32;

    fn wait_for(&self, context: &mut Context, _input: i32) -> HandlerResult<Wait> {
        let next = WAIT_FOR_COUNT.get(context)?.unwrap_or_default() + 1;
        WAIT_FOR_COUNT.set(context, next)?;
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, context: &mut Context, _input: i32) -> HandlerResult<StepDecision> {
        let next = EXECUTE_COUNT.get(context)?.unwrap_or_default() + 1;
        EXECUTE_COUNT.set(context, next)?;
        COMPLETED.publish(context, ())?;
        Ok(StepDecision::dead_end())
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .wait_for_lock(WAIT_FOR_COUNT.lock())
            .execute_lock(EXECUTE_COUNT.lock())
    }
}

struct CompleteStep;

impl Step for CompleteStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, parallelism: i32) -> HandlerResult<Wait> {
        Ok(Wait::until(COMPLETED.for_n(parallelism as usize)))
    }

    fn execute(&self, context: &mut Context, parallelism: i32) -> HandlerResult<StepDecision> {
        if COMPLETED.condition_results(context)?.len() != parallelism as usize {
            return Err(HandlerError::new(
                "StateOptionsLockingFailure",
                "not all locked Steps completed",
            ));
        }
        Ok(StepDecision::graceful_complete(format!(
            "{}:{}",
            WAIT_FOR_COUNT.get_required(context)?,
            EXECUTE_COUNT.get_required(context)?
        )))
    }
}
