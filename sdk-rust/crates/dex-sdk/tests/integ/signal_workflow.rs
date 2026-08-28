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

use std::sync::LazyLock;

use dex_sdk::{
    Channel, ChannelMap, ConditionCombination, Context, Flow, HandlerError, HandlerResult,
    PersistenceSchema, Step, StepDecision, StepList, Timer, Wait,
};

pub(crate) static SECOND: LazyLock<Channel<i32>> = LazyLock::new(|| Channel::new("signal-2"));
pub(crate) static THIRD: LazyLock<Channel<()>> = LazyLock::new(|| Channel::new("signal-3"));
pub(crate) static FIRST: LazyLock<Channel<i32>> = LazyLock::new(|| Channel::new("signal-1"));

pub(crate) struct SignalWorkflow {
    pub(crate) signal_map: ChannelMap<i32>,
    start: FirstStep,
    pub(crate) combination: CombinationStep,
}

impl SignalWorkflow {
    pub(crate) fn new() -> Self {
        let signal_map = ChannelMap::new("signal-map");
        Self {
            start: FirstStep,
            combination: CombinationStep {
                signal_map: signal_map.clone(),
            },
            signal_map,
        }
    }
}

impl Flow for SignalWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start).and(&self.combination)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .channel(&FIRST)
            .channel(&SECOND)
            .channel(&THIRD)
            .channel_map(&self.signal_map)
    }
}

struct FirstStep;

impl Step for FirstStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, _input: i32) -> HandlerResult<Wait> {
        Ok(Wait::any_of([
            FIRST.for_one().with_id("test-signal-id-1"),
            SECOND.for_one().with_id("test-signal-id-2"),
        ]))
    }

    fn execute(&self, context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        if !SECOND.condition_results(context)?.is_empty() {
            return Err(HandlerError::new(
                "SignalFailure",
                "second signal should still be waiting",
            ));
        }
        let value = FIRST.condition_results(context)?[0];
        Ok(StepDecision::go_to(
            &CombinationStep {
                signal_map: ChannelMap::new("signal-map"),
            },
            input + value,
        ))
    }
}

pub(crate) struct CombinationStep {
    signal_map: ChannelMap<i32>,
}

impl Step for CombinationStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, _input: i32) -> HandlerResult<Wait> {
        Ok(Wait::any_combination_of([ConditionCombination::all_of([
            FIRST.for_one().with_id("signal-1"),
            THIRD.for_one().with_id("signal-3"),
            self.signal_map.for_one("one").with_id("signal-map"),
            Timer::by_duration(Duration::from_secs(365 * 24 * 60 * 60)).with_id("test-timer-id"),
        ])]))
    }

    fn execute(&self, context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        if !SECOND.condition_results(context)?.is_empty() {
            return Err(HandlerError::new(
                "SignalFailure",
                "second signal should still be waiting",
            ));
        }
        if THIRD.condition_results(context)?.len() != 1 {
            return Err(HandlerError::new(
                "SignalFailure",
                "null signal was not received",
            ));
        }
        if self.signal_map.condition_results(context, "one")?.len() != 1 {
            return Err(HandlerError::new(
                "SignalFailure",
                "mapped signal was not received",
            ));
        }
        if !context.has_any_timer_fired() {
            return Err(HandlerError::new("SignalFailure", "timer was not fired"));
        }
        Ok(StepDecision::graceful_complete(
            input + FIRST.condition_results(context)?[0],
        ))
    }
}
