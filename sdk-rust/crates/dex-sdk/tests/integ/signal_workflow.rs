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

use dex_sdk::{
    Channel, ChannelMap, ConditionCombination, Context, Flow, HandlerError, HandlerResult,
    PersistenceSchema, Step, StepDecision, StepList, Timer, Wait,
};

pub(crate) struct SignalWorkflow {
    pub(crate) first: Channel<i32>,
    pub(crate) second: Channel<i32>,
    pub(crate) third: Channel<()>,
    pub(crate) signal_map: ChannelMap<i32>,
    start: FirstStep,
    pub(crate) combination: CombinationStep,
}

impl SignalWorkflow {
    pub(crate) fn new() -> Self {
        let first = Channel::new("signal-1");
        let second = Channel::new("signal-2");
        let third = Channel::new("signal-3");
        let signal_map = ChannelMap::new("signal-map");
        Self {
            start: FirstStep {
                first: first.clone(),
                second: second.clone(),
            },
            combination: CombinationStep {
                first: first.clone(),
                second: second.clone(),
                third: third.clone(),
                signal_map: signal_map.clone(),
            },
            first,
            second,
            third,
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
            .channel(&self.first)
            .channel(&self.second)
            .channel(&self.third)
            .channel_map(&self.signal_map)
    }
}

struct FirstStep {
    first: Channel<i32>,
    second: Channel<i32>,
}

impl Step for FirstStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, _input: i32) -> HandlerResult<Wait> {
        Ok(Wait::any_of([
            self.first.for_one().with_id("test-signal-id-1"),
            self.second.for_one().with_id("test-signal-id-2"),
        ]))
    }

    fn execute(&self, context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        if !self.second.condition_results(context)?.is_empty() {
            return Err(HandlerError::new(
                "SignalFailure",
                "second signal should still be waiting",
            ));
        }
        let value = self.first.condition_results(context)?[0];
        Ok(StepDecision::go_to(
            &CombinationStep {
                first: self.first.clone(),
                second: Channel::new("signal-2"),
                third: Channel::new("signal-3"),
                signal_map: ChannelMap::new("signal-map"),
            },
            input + value,
        ))
    }
}

pub(crate) struct CombinationStep {
    first: Channel<i32>,
    second: Channel<i32>,
    third: Channel<()>,
    signal_map: ChannelMap<i32>,
}

impl Step for CombinationStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, _input: i32) -> HandlerResult<Wait> {
        Ok(Wait::any_combination_of([ConditionCombination::all_of([
            self.first.for_one().with_id("signal-1"),
            self.third.for_one().with_id("signal-3"),
            self.signal_map.for_one("one").with_id("signal-map"),
            Timer::by_duration(Duration::from_secs(365 * 24 * 60 * 60)).with_id("test-timer-id"),
        ])]))
    }

    fn execute(&self, context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        if !self.second.condition_results(context)?.is_empty() {
            return Err(HandlerError::new(
                "SignalFailure",
                "second signal should still be waiting",
            ));
        }
        if self.third.condition_results(context)?.len() != 1 {
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
            input + self.first.condition_results(context)?[0],
        ))
    }
}
