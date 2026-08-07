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
    Attribute, Channel, ChannelMap, Client, ConditionCombination, Context, Flow, HandlerError,
    HandlerResult, PersistenceSchema, RetryPolicy, Rpc, RpcList, SdkResult, Step, StepDecision,
    StepExecutionId, StepList, StepMovement, StepOptions, Timer, TimerId, Wait,
};

struct AnyCommandCombinationWorkflow {
    first: Channel<i32>,
    second: Channel<i32>,
    third: Channel<i32>,
    start: AnyCommandCombinationStep,
}

impl AnyCommandCombinationWorkflow {
    fn new() -> Self {
        let first = Channel::new("test-signal-1");
        let second = Channel::new("test-signal-2");
        let third = Channel::new("test-signal-3");
        Self {
            start: AnyCommandCombinationStep,
            first,
            second,
            third,
        }
    }
}

impl Flow for AnyCommandCombinationWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<Self::StartInput> {
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

struct ConditionalCompleteWorkflow {
    signal: Channel<()>,
    internal: Channel<()>,
    counter: Attribute<i32>,
    start: ConditionalCompleteStep,
}

impl ConditionalCompleteWorkflow {
    const PUBLISH_TO_INTERNAL: Rpc<(), ()> = Rpc::new("publish_to_internal_channel");

    fn new() -> Self {
        let signal = Channel::new("test-signal-channel");
        let internal = Channel::new("test-internal-channel");
        let counter = Attribute::new("counter");
        Self {
            start: ConditionalCompleteStep {
                signal: signal.clone(),
                internal: internal.clone(),
                counter: counter.clone(),
            },
            signal,
            internal,
            counter,
        }
    }

    fn publish_to_internal_channel(&self, context: &mut Context) -> HandlerResult<()> {
        self.internal.publish(context, ())
    }
}

impl Flow for ConditionalCompleteWorkflow {
    type StartInput = bool;

    fn steps(&self) -> StepList<Self::StartInput> {
        StepList::start(&self.start)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&self.counter)
            .channel(&self.signal)
            .channel(&self.internal)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .procedure_without_input(Self::PUBLISH_TO_INTERNAL, Self::publish_to_internal_channel)
    }
}

struct ConditionalCompleteStep {
    signal: Channel<()>,
    internal: Channel<()>,
    counter: Attribute<i32>,
}

impl Step for ConditionalCompleteStep {
    type Input = bool;

    fn wait_for(&self, _context: &mut Context, use_signal: bool) -> HandlerResult<Wait> {
        let condition = if use_signal {
            self.signal.for_one()
        } else {
            self.internal.for_one()
        };
        Ok(Wait::any_of([condition]))
    }

    fn execute(&self, context: &mut Context, use_signal: bool) -> HandlerResult<StepDecision> {
        let next = self.counter.get(context)?.unwrap_or_default() + 1;
        self.counter.set(context, next)?;
        let selected = if use_signal {
            &self.signal
        } else {
            &self.internal
        };
        Ok(StepDecision::force_complete_when_channels_empty(
            next,
            StepMovement::to(self, use_signal),
            [selected.when_empty()],
        ))
    }
}

struct InternalChannelWorkflow {
    channel: Channel<i32>,
    channel_map: ChannelMap<i32>,
    start: InternalForkStep,
    consumer: InternalConsumeStep,
    publisher: InternalPublishStep,
}

impl InternalChannelWorkflow {
    fn new() -> Self {
        let channel = Channel::new("test-inter-state-channel-1");
        let channel_map = ChannelMap::new("test-inter-state-channel-map");
        Self {
            start: InternalForkStep,
            consumer: InternalConsumeStep {
                channel: channel.clone(),
                channel_map: channel_map.clone(),
            },
            publisher: InternalPublishStep {
                channel: channel.clone(),
                channel_map: channel_map.clone(),
            },
            channel,
            channel_map,
        }
    }
}

impl Flow for InternalChannelWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<Self::StartInput> {
        StepList::start(&self.start)
            .and(&self.consumer)
            .and(&self.publisher)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .channel(&self.channel)
            .channel_map(&self.channel_map)
    }
}

struct InternalForkStep;

impl Step for InternalForkStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to_many([
            StepMovement::to(
                &InternalConsumeStep {
                    channel: Channel::new("test-inter-state-channel-1"),
                    channel_map: ChannelMap::new("test-inter-state-channel-map"),
                },
                input,
            ),
            StepMovement::to(
                &InternalPublishStep {
                    channel: Channel::new("test-inter-state-channel-1"),
                    channel_map: ChannelMap::new("test-inter-state-channel-map"),
                },
                input,
            ),
        ]))
    }
}

struct InternalConsumeStep {
    channel: Channel<i32>,
    channel_map: ChannelMap<i32>,
}

impl Step for InternalConsumeStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, _input: i32) -> HandlerResult<Wait> {
        Ok(Wait::any_combination_of([
            ConditionCombination::all_of([self.channel.for_one().with_id("first")]),
            ConditionCombination::all_of([self.channel_map.for_one("one")]),
        ]))
    }

    fn execute(&self, context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(
            input + self.channel.condition_results(context)?.len() as i32,
        ))
    }
}

struct InternalPublishStep {
    channel: Channel<i32>,
    channel_map: ChannelMap<i32>,
}

impl Step for InternalPublishStep {
    type Input = i32;

    fn execute(&self, context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        self.channel.publish(context, input)?;
        self.channel_map.publish(context, "one", input)?;
        Ok(StepDecision::dead_end())
    }
}

struct InternalChannelWaitingWorkflow {
    channel: Channel<i32>,
    start: InternalChannelWaitingStep,
}

impl InternalChannelWaitingWorkflow {
    fn new() -> Self {
        let channel = Channel::new("waiting-channel");
        Self {
            start: InternalChannelWaitingStep {
                channel: channel.clone(),
            },
            channel,
        }
    }
}

impl Flow for InternalChannelWaitingWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<Self::StartInput> {
        StepList::start(&self.start)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().channel(&self.channel)
    }
}

struct InternalChannelWaitingStep {
    channel: Channel<i32>,
}

impl Step for InternalChannelWaitingStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, _input: i32) -> HandlerResult<Wait> {
        Ok(Wait::all_of([self.channel.for_n(2)]))
    }

    fn execute(&self, context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        let output = self
            .channel
            .condition_results(context)?
            .into_iter()
            .fold(input, i32::saturating_add);
        Ok(StepDecision::graceful_complete(output))
    }
}

struct SignalWorkflow {
    first: Channel<i32>,
    second: Channel<i32>,
    third: Channel<()>,
    signal_map: ChannelMap<i32>,
    start: SignalFirstStep,
    combination: SignalCombinationStep,
}

impl SignalWorkflow {
    fn new() -> Self {
        let first = Channel::new("signal-1");
        let second = Channel::new("signal-2");
        let third = Channel::new("signal-3");
        let signal_map = ChannelMap::new("signal-map");
        Self {
            start: SignalFirstStep {
                first: first.clone(),
            },
            combination: SignalCombinationStep {
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

    fn steps(&self) -> StepList<Self::StartInput> {
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

struct SignalFirstStep {
    first: Channel<i32>,
}

impl Step for SignalFirstStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, _input: i32) -> HandlerResult<Wait> {
        Ok(Wait::any_of([self
            .first
            .for_one()
            .with_id("test-signal-id")]))
    }

    fn execute(&self, context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        let value = self.first.condition_results(context)?[0];
        Ok(StepDecision::go_to(
            &SignalCombinationStep {
                second: Channel::new("signal-2"),
                third: Channel::new("signal-3"),
                signal_map: ChannelMap::new("signal-map"),
            },
            input + value,
        ))
    }
}

struct SignalCombinationStep {
    second: Channel<i32>,
    third: Channel<()>,
    signal_map: ChannelMap<i32>,
}

impl Step for SignalCombinationStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, _input: i32) -> HandlerResult<Wait> {
        Ok(Wait::any_combination_of([
            ConditionCombination::all_of([
                self.second.for_one().with_id("signal-2"),
                Timer::by_duration(Duration::from_secs(10)).with_id("test-timer-id"),
            ]),
            ConditionCombination::all_of([self.third.for_n(2), self.signal_map.for_one("one")]),
        ]))
    }

    fn execute(&self, context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(
            input + self.third.size(context)? as i32,
        ))
    }
}

struct TimerWorkflow {
    start: TimerStep,
}

impl Flow for TimerWorkflow {
    type StartInput = u64;

    fn steps(&self) -> StepList<Self::StartInput> {
        StepList::start(&self.start)
    }
}

struct TimerStep;

impl Step for TimerStep {
    type Input = u64;

    fn wait_for(&self, _context: &mut Context, input: u64) -> HandlerResult<Wait> {
        Ok(Wait::all_of([Timer::by_duration(Duration::from_secs(
            input,
        ))
        .with_id("test-timer-id")]))
    }

    fn execute(&self, _context: &mut Context, _input: u64) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(()))
    }
}

fn compile_any_command_combination_test(client: &Client) -> SdkResult<()> {
    let workflow = AnyCommandCombinationWorkflow::new();
    client.start_flow(&workflow, "any-combination", 0)?;
    let result: i32 = client.wait_for_flow("any-combination")?;
    assert_eq!(0, result);
    Ok(())
}

fn compile_conditional_complete_test(client: &Client) -> SdkResult<()> {
    let workflow = ConditionalCompleteWorkflow::new();
    client.start_flow(&workflow, "conditional-signal", true)?;
    client.publish("conditional-signal", &workflow.signal, ())?;
    let output: i32 = client.wait_for_flow("conditional-signal")?;
    assert_eq!(1, output);

    client.start_flow(&workflow, "conditional-internal", false)?;
    client.invoke_rpc_without_input(
        "conditional-internal",
        ConditionalCompleteWorkflow::PUBLISH_TO_INTERNAL,
    )?;
    Ok(())
}

fn compile_internal_channel_test(client: &Client) -> SdkResult<()> {
    let workflow = InternalChannelWorkflow::new();
    client.start_flow(&workflow, "internal", 1)?;
    let output: i32 = client.wait_for_flow("internal")?;
    assert_eq!(2, output);

    let waiting = InternalChannelWaitingWorkflow::new();
    client.start_flow(&waiting, "waiting", 1)?;
    client.publish_many("waiting", &waiting.channel, [2, 3])?;
    Ok(())
}

fn compile_signal_test(client: &Client) -> SdkResult<()> {
    let workflow = SignalWorkflow::new();
    client.start_flow(&workflow, "signal", 0)?;
    client.publish("signal", &workflow.first, 1)?;
    client.publish("signal", &workflow.second, 2)?;
    client.publish("signal", &workflow.third, ())?;
    client.publish_map("signal", &workflow.signal_map, "one", [5])?;
    client.skip_timer(
        "signal",
        StepExecutionId::of(&workflow.combination),
        TimerId::by_condition_id("test-timer-id"),
    )?;
    Ok(())
}

fn compile_timer_test(client: &Client) -> SdkResult<()> {
    let workflow = TimerWorkflow { start: TimerStep };
    client.start_flow(&workflow, "timer", 1)?;
    client.wait_for_step_completion(
        "timer",
        StepExecutionId::of(&workflow.start),
        Duration::from_secs(10),
    )?;
    let _: () = client.wait_for_flow("timer")?;
    Ok(())
}
