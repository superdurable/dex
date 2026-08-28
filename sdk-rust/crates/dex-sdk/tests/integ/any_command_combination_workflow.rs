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
    Channel, Context, Flow, HandlerError, HandlerResult, PersistenceSchema, RetryPolicy, Step,
    StepDecision, StepList, StepOptions, Wait,
};

static FIRST: LazyLock<Channel<i32>> = LazyLock::new(|| Channel::new("test-signal-1"));
static SECOND: LazyLock<Channel<i32>> = LazyLock::new(|| Channel::new("test-signal-2"));
static THIRD: LazyLock<Channel<i32>> = LazyLock::new(|| Channel::new("test-signal-3"));

pub(crate) struct AnyCommandCombinationWorkflow {
    first: Channel<i32>,
    second: Channel<i32>,
    third: Channel<i32>,
    start: AnyCommandCombinationStep,
}

impl AnyCommandCombinationWorkflow {
    pub(crate) fn new() -> Self {
        Self {
            first: FIRST.clone(),
            second: SECOND.clone(),
            third: THIRD.clone(),
            start: AnyCommandCombinationStep,
        }
    }
}

impl Flow for AnyCommandCombinationWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .channel(&self.first)
            .channel(&self.second)
            .channel(&self.third)
    }
}

struct AnyCommandCombinationStep;

impl Step for AnyCommandCombinationStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, _input: i32) -> HandlerResult<Wait> {
        Err(HandlerError::new(
            "UnknownConditionId",
            "Found unknown condition ID in the combination list",
        ))
    }

    fn execute(&self, _context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(input))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().wait_for_retry(RetryPolicy::new().maximum_attempts(1))
    }
}
