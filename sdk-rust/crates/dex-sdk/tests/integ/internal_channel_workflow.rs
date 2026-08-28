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
    Channel, ChannelMap, ConditionCombination, Context, Flow, HandlerError, HandlerResult,
    PersistenceSchema, Step, StepDecision, StepList, StepMovement, Wait,
};

static FIRST_CHANNEL: LazyLock<Channel<i32>> =
    LazyLock::new(|| Channel::new("test-inter-state-channel-1"));
static SECOND_CHANNEL: LazyLock<Channel<i32>> =
    LazyLock::new(|| Channel::new("test-inter-state-channel-2"));

pub(crate) struct InternalChannelWorkflow {
    first_channel: Channel<i32>,
    second_channel: Channel<i32>,
    channel_map: ChannelMap<i32>,
    start: ForkStep,
    consumer: ConsumeStep,
    publisher: PublishStep,
}

impl InternalChannelWorkflow {
    pub(crate) fn new() -> Self {
        let first_channel = FIRST_CHANNEL.clone();
        let second_channel = SECOND_CHANNEL.clone();
        let channel_map = ChannelMap::new("test-inter-state-channel-map");
        Self {
            start: ForkStep,
            consumer: ConsumeStep {
                first_channel: first_channel.clone(),
                second_channel: second_channel.clone(),
                channel_map: channel_map.clone(),
            },
            publisher: PublishStep {
                first_channel: first_channel.clone(),
                channel_map: channel_map.clone(),
            },
            first_channel,
            second_channel,
            channel_map,
        }
    }
}

impl Flow for InternalChannelWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
            .and(&self.consumer)
            .and(&self.publisher)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .channel(&self.first_channel)
            .channel(&self.second_channel)
            .channel_map(&self.channel_map)
    }
}

struct ForkStep;

impl Step for ForkStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to_many([
            StepMovement::to(
                &ConsumeStep {
                    first_channel: FIRST_CHANNEL.clone(),
                    second_channel: SECOND_CHANNEL.clone(),
                    channel_map: ChannelMap::new("test-inter-state-channel-map"),
                },
                input,
            ),
            StepMovement::to(
                &PublishStep {
                    first_channel: FIRST_CHANNEL.clone(),
                    channel_map: ChannelMap::new("test-inter-state-channel-map"),
                },
                input,
            ),
        ]))
    }
}

struct ConsumeStep {
    first_channel: Channel<i32>,
    second_channel: Channel<i32>,
    channel_map: ChannelMap<i32>,
}

impl Step for ConsumeStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, _input: i32) -> HandlerResult<Wait> {
        Ok(Wait::any_combination_of([
            ConditionCombination::all_of([
                self.first_channel.for_one().with_id("first"),
                self.channel_map.for_one("one").with_id("mapped"),
            ]),
            ConditionCombination::all_of([self.second_channel.for_one().with_id("second")]),
        ]))
    }

    fn execute(&self, context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        if !self.second_channel.condition_results(context)?.is_empty() {
            return Err(HandlerError::new(
                "InternalChannelFailure",
                "second channel should still be waiting",
            ));
        }
        let first = self.first_channel.condition_results(context)?[0];
        let mapped = self.channel_map.condition_results(context, "one")?[0];
        if mapped != 3 {
            return Err(HandlerError::new(
                "InternalChannelFailure",
                format!("mapped channel returned {mapped}"),
            ));
        }
        Ok(StepDecision::graceful_complete(input + first))
    }
}

struct PublishStep {
    first_channel: Channel<i32>,
    channel_map: ChannelMap<i32>,
}

impl Step for PublishStep {
    type Input = i32;

    fn execute(&self, context: &mut Context, _input: i32) -> HandlerResult<StepDecision> {
        self.first_channel.publish(context, 2)?;
        self.channel_map.publish(context, "one", 3)?;
        Ok(StepDecision::dead_end())
    }
}
