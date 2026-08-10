// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use std::time::{Duration, Instant};

use dex_sdk::{
    Attribute, Channel, ChannelMap, Client, ConditionCombination, Context, ErrorSubStatus, Flow,
    FlowErrorType, FlowStatus, HandlerError, HandlerResult, PersistenceSchema, Registry,
    RetryPolicy, Rpc, RpcList, SdkError, SdkResult, Step, StepDecision, StepExecutionId, StepList,
    StepMovement, StepOptions, Timer, TimerId, Wait,
};

use crate::support::{DexDevTestEnvironment, flow_id};

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
    const PUBLISH_TO_INTERNAL: Rpc<i32, ()> = Rpc::new("publish_to_internal_channel");

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

    fn publish_to_internal_channel(&self, context: &mut Context, count: i32) -> HandlerResult<()> {
        for _ in 0..count {
            self.internal.publish(context, ())?;
        }
        Ok(())
    }
}

impl Flow for ConditionalCompleteWorkflow {
    type StartInput = bool;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&self.counter)
            .channel(&self.signal)
            .channel(&self.internal)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().procedure(Self::PUBLISH_TO_INTERNAL, Self::publish_to_internal_channel)
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
        Ok(Wait::until(condition))
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
    first_channel: Channel<i32>,
    second_channel: Channel<i32>,
    channel_map: ChannelMap<i32>,
    start: InternalForkStep,
    consumer: InternalConsumeStep,
    publisher: InternalPublishStep,
}

impl InternalChannelWorkflow {
    fn new() -> Self {
        let first_channel = Channel::new("test-inter-state-channel-1");
        let second_channel = Channel::new("test-inter-state-channel-2");
        let channel_map = ChannelMap::new("test-inter-state-channel-map");
        Self {
            start: InternalForkStep,
            consumer: InternalConsumeStep {
                first_channel: first_channel.clone(),
                second_channel: second_channel.clone(),
                channel_map: channel_map.clone(),
            },
            publisher: InternalPublishStep {
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

struct InternalForkStep;

impl Step for InternalForkStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to_many([
            StepMovement::to(
                &InternalConsumeStep {
                    first_channel: Channel::new("test-inter-state-channel-1"),
                    second_channel: Channel::new("test-inter-state-channel-2"),
                    channel_map: ChannelMap::new("test-inter-state-channel-map"),
                },
                input,
            ),
            StepMovement::to(
                &InternalPublishStep {
                    first_channel: Channel::new("test-inter-state-channel-1"),
                    channel_map: ChannelMap::new("test-inter-state-channel-map"),
                },
                input,
            ),
        ]))
    }
}

struct InternalConsumeStep {
    first_channel: Channel<i32>,
    second_channel: Channel<i32>,
    channel_map: ChannelMap<i32>,
}

impl Step for InternalConsumeStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, _input: i32) -> HandlerResult<Wait> {
        Ok(Wait::any_combination_of([
            ConditionCombination::all_of([
                self.first_channel.for_one().with_id("first"),
                self.channel_map.for_one("one"),
            ]),
            ConditionCombination::all_of([self.second_channel.for_one().with_id("second")]),
        ]))
    }

    fn execute(&self, context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        if !self.second_channel.condition_results(context)?.is_empty() {
            return Err(HandlerError::new("second channel should still be waiting"));
        }
        let first = self.first_channel.condition_results(context)?[0];
        let mapped = self.channel_map.condition_results(context, "one")?[0];
        if mapped != 3 {
            return Err(HandlerError::new(format!(
                "mapped channel returned {mapped}"
            )));
        }
        Ok(StepDecision::graceful_complete(input + first))
    }
}

struct InternalPublishStep {
    first_channel: Channel<i32>,
    channel_map: ChannelMap<i32>,
}

impl Step for InternalPublishStep {
    type Input = i32;

    fn execute(&self, context: &mut Context, _input: i32) -> HandlerResult<StepDecision> {
        self.first_channel.publish(context, 2)?;
        self.channel_map.publish(context, "one", 3)?;
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

    fn steps(&self) -> StepList<'_, Self::StartInput> {
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
        Ok(Wait::until(self.channel.for_n(2)))
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

pub(crate) struct SignalWorkflow {
    pub(crate) first: Channel<i32>,
    pub(crate) second: Channel<i32>,
    pub(crate) third: Channel<()>,
    pub(crate) signal_map: ChannelMap<i32>,
    start: SignalFirstStep,
    combination: SignalCombinationStep,
}

impl SignalWorkflow {
    pub(crate) fn new() -> Self {
        let first = Channel::new("signal-1");
        let second = Channel::new("signal-2");
        let third = Channel::new("signal-3");
        let signal_map = ChannelMap::new("signal-map");
        Self {
            start: SignalFirstStep {
                first: first.clone(),
                second: second.clone(),
            },
            combination: SignalCombinationStep {
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

struct SignalFirstStep {
    first: Channel<i32>,
    second: Channel<i32>,
}

impl Step for SignalFirstStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, _input: i32) -> HandlerResult<Wait> {
        Ok(Wait::any_of([
            self.first.for_one().with_id("test-signal-id-1"),
            self.second.for_one().with_id("test-signal-id-2"),
        ]))
    }

    fn execute(&self, context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        if !self.second.condition_results(context)?.is_empty() {
            return Err(HandlerError::new("second signal should still be waiting"));
        }
        let value = self.first.condition_results(context)?[0];
        Ok(StepDecision::go_to(
            &SignalCombinationStep {
                first: self.first.clone(),
                second: Channel::new("signal-2"),
                third: Channel::new("signal-3"),
                signal_map: ChannelMap::new("signal-map"),
            },
            input + value,
        ))
    }
}

struct SignalCombinationStep {
    first: Channel<i32>,
    second: Channel<i32>,
    third: Channel<()>,
    signal_map: ChannelMap<i32>,
}

impl Step for SignalCombinationStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, _input: i32) -> HandlerResult<Wait> {
        Ok(Wait::any_combination_of([ConditionCombination::all_of([
            self.first.for_one().with_id("signal-1"),
            self.third.for_one().with_id("signal-3"),
            self.signal_map.for_one("one"),
            Timer::by_duration(Duration::from_secs(365 * 24 * 60 * 60)).with_id("test-timer-id"),
        ])]))
    }

    fn execute(&self, context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        if !self.second.condition_results(context)?.is_empty() {
            return Err(HandlerError::new("second signal should still be waiting"));
        }
        if self.third.condition_results(context)?.len() != 1 {
            return Err(HandlerError::new("null signal was not received"));
        }
        if self.signal_map.condition_results(context, "one")?.len() != 1 {
            return Err(HandlerError::new("mapped signal was not received"));
        }
        if !context.has_any_timer_fired() {
            return Err(HandlerError::new("timer was not fired"));
        }
        let next = self.first.condition_results(context)?[0];
        Ok(StepDecision::graceful_complete(input + next))
    }
}

struct TimerWorkflow {
    start: TimerStep,
}

impl Flow for TimerWorkflow {
    type StartInput = u64;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }
}

struct TimerStep;

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

struct GoInterStepWorkflow {
    first: Channel<i32>,
    second: Channel<i32>,
    start: GoInterStepStart,
    consumer: GoInterStepConsumer,
    publisher: GoInterStepPublisher,
}

impl GoInterStepWorkflow {
    fn new() -> Self {
        let first = Channel::new("inter-step-first");
        let second = Channel::new("inter-step-second");
        Self {
            start: GoInterStepStart,
            consumer: GoInterStepConsumer {
                first: first.clone(),
                second: second.clone(),
            },
            publisher: GoInterStepPublisher {
                second: second.clone(),
            },
            first,
            second,
        }
    }
}

impl Flow for GoInterStepWorkflow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
            .and(&self.consumer)
            .and(&self.publisher)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .channel(&self.first)
            .channel(&self.second)
    }
}

struct GoInterStepStart;

impl Step for GoInterStepStart {
    type Input = ();

    fn execute(&self, _context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to_many([
            StepMovement::to(
                &GoInterStepConsumer {
                    first: Channel::new("inter-step-first"),
                    second: Channel::new("inter-step-second"),
                },
                (),
            ),
            StepMovement::to(
                &GoInterStepPublisher {
                    second: Channel::new("inter-step-second"),
                },
                2,
            ),
        ]))
    }
}

struct GoInterStepConsumer {
    first: Channel<i32>,
    second: Channel<i32>,
}

impl Step for GoInterStepConsumer {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, (): ()) -> HandlerResult<Wait> {
        Ok(Wait::any_of([self.first.for_one(), self.second.for_one()]))
    }

    fn execute(&self, context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        let first = self.first.condition_results(context)?;
        let second = self.second.condition_results(context)?;
        if !first.is_empty() || second != [2] {
            return Err(HandlerError::new(format!(
                "unexpected channel results: first={first:?} second={second:?}"
            )));
        }
        Ok(StepDecision::graceful_complete(second[0]))
    }
}

struct GoInterStepPublisher {
    second: Channel<i32>,
}

impl Step for GoInterStepPublisher {
    type Input = i32;

    fn wait_for(&self, context: &mut Context, input: i32) -> HandlerResult<Wait> {
        self.second.publish(context, input)?;
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, _context: &mut Context, _input: i32) -> HandlerResult<StepDecision> {
        Ok(StepDecision::dead_end())
    }
}

struct GoChannelWorkflow {
    first: Channel<i32>,
    second: Channel<i32>,
    start: GoChannelFirstStep,
    finish: GoChannelSecondStep,
}

impl GoChannelWorkflow {
    fn new() -> Self {
        let first = Channel::new("first");
        let second = Channel::new("second");
        Self {
            start: GoChannelFirstStep {
                first: first.clone(),
                second: second.clone(),
            },
            finish: GoChannelSecondStep {
                first: first.clone(),
                second: second.clone(),
            },
            first,
            second,
        }
    }
}

impl Flow for GoChannelWorkflow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start).and(&self.finish)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .channel(&self.first)
            .channel(&self.second)
    }
}

struct GoChannelFirstStep {
    first: Channel<i32>,
    second: Channel<i32>,
}

impl Step for GoChannelFirstStep {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, (): ()) -> HandlerResult<Wait> {
        Ok(Wait::any_of([self.first.for_one(), self.second.for_one()]))
    }

    fn execute(&self, context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        let first = self.first.condition_results(context)?;
        let second = self.second.condition_results(context)?;
        if !first.is_empty() || second != [10] {
            return Err(HandlerError::new(format!(
                "unexpected first-step channel results: first={first:?} second={second:?}"
            )));
        }
        Ok(StepDecision::go_to(
            &GoChannelSecondStep {
                first: self.first.clone(),
                second: self.second.clone(),
            },
            (),
        ))
    }
}

struct GoChannelSecondStep {
    first: Channel<i32>,
    second: Channel<i32>,
}

impl Step for GoChannelSecondStep {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, (): ()) -> HandlerResult<Wait> {
        Ok(Wait::any_combination_of([ConditionCombination::all_of([
            self.first.for_one(),
            Timer::by_duration(Duration::from_secs(24 * 60 * 60)).with_id("finish-timer"),
        ])]))
    }

    fn execute(&self, context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        if !context.has_any_timer_fired() || !context.has_timer_fired(0) {
            return Err(HandlerError::new("skipped timer was not reported as fired"));
        }
        let first = self.first.condition_results(context)?;
        let second = self.second.condition_results(context)?;
        if first != [100] || !second.is_empty() {
            return Err(HandlerError::new(format!(
                "unexpected second-step channel results: first={first:?} second={second:?}"
            )));
        }
        Ok(StepDecision::graceful_complete(first[0]))
    }
}

struct GoTimerWorkflow {
    start: GoTimerStep,
}

impl Flow for GoTimerWorkflow {
    type StartInput = u64;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }
}

struct GoTimerStep;

impl Step for GoTimerStep {
    type Input = u64;

    fn wait_for(&self, _context: &mut Context, input: u64) -> HandlerResult<Wait> {
        Ok(Wait::until(Timer::by_duration(Duration::from_secs(input))))
    }

    fn execute(&self, context: &mut Context, input: u64) -> HandlerResult<StepDecision> {
        if !context.has_any_timer_fired() || !context.has_timer_fired(0) {
            return Err(HandlerError::new("natural timer was not reported as fired"));
        }
        Ok(StepDecision::graceful_complete(input + 1))
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
    client.invoke_rpc(
        "conditional-internal",
        ConditionalCompleteWorkflow::PUBLISH_TO_INTERNAL,
        3,
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

#[test]
#[ignore = "requires dexcli dev"]
fn invalid_condition_combination_fails_flow() {
    let environment = DexDevTestEnvironment::start(
        Registry::new().register(AnyCommandCombinationWorkflow::new()),
    );
    let workflow = AnyCommandCombinationWorkflow::new();
    let flow_id = flow_id("any-combination-fail");
    let run_id = environment
        .client
        .start_flow(&workflow, &flow_id, 5)
        .expect("start invalid-combination Flow");
    let failure = environment
        .client
        .wait_for_flow_with_timeout::<i32>(&flow_id, Duration::from_secs(30))
        .expect_err("invalid condition combination must fail");
    match failure {
        SdkError::FlowUncompleted {
            run_id: failed_run,
            status,
            error_type,
            message,
            result_count,
        } => {
            assert_eq!(run_id, failed_run);
            assert_eq!(FlowStatus::Failed, status);
            assert_eq!(Some(FlowErrorType::WorkerApiFailed), error_type);
            assert!(
                message
                    .as_deref()
                    .is_some_and(|message| message.contains("unknown condition ID"))
            );
            assert_eq!(0, result_count);
        }
        error => panic!("expected FlowUncompleted, got {error:?}"),
    }
    let info = environment
        .client
        .describe_flow(&flow_id)
        .expect("describe failed Flow");
    assert_eq!(run_id, info.run_id);
    assert_eq!(FlowStatus::Failed, info.status);
}

#[test]
#[ignore = "requires dexcli dev"]
fn conditional_complete_drains_signal_channel() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(ConditionalCompleteWorkflow::new()));
    let workflow = ConditionalCompleteWorkflow::new();
    let flow_id = flow_id("conditional-signal");
    environment
        .client
        .start_flow(&workflow, &flow_id, true)
        .expect("start conditional signal Flow");
    environment
        .client
        .publish_many(&flow_id, &workflow.signal, [(), (), ()])
        .expect("publish signal messages");
    assert_eq!(
        3,
        environment
            .client
            .wait_for_flow_with_timeout::<i32>(&flow_id, Duration::from_secs(30))
            .expect("drain signal channel")
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn conditional_complete_drains_internal_channel() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(ConditionalCompleteWorkflow::new()));
    let workflow = ConditionalCompleteWorkflow::new();
    let flow_id = flow_id("conditional-internal");
    environment
        .client
        .start_flow(&workflow, &flow_id, false)
        .expect("start conditional internal Flow");
    environment
        .client
        .invoke_rpc(
            &flow_id,
            ConditionalCompleteWorkflow::PUBLISH_TO_INTERNAL,
            3,
        )
        .expect("publish internal messages through RPC");
    assert_eq!(
        3,
        environment
            .client
            .wait_for_flow_with_timeout::<i32>(&flow_id, Duration::from_secs(30))
            .expect("drain internal channel")
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn internal_channels_coordinate_parallel_steps() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(InternalChannelWorkflow::new()));
    let workflow = InternalChannelWorkflow::new();
    let flow_id = flow_id("basic-internal");
    environment
        .client
        .start_flow(&workflow, &flow_id, 1)
        .expect("start internal-channel Flow");
    assert_eq!(
        3,
        environment
            .client
            .wait_for_flow_with_timeout::<i32>(&flow_id, Duration::from_secs(30))
            .expect("complete internal-channel Flow")
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn external_messages_satisfy_waiting_internal_channel() {
    let environment = DexDevTestEnvironment::start(
        Registry::new().register(InternalChannelWaitingWorkflow::new()),
    );
    let workflow = InternalChannelWaitingWorkflow::new();
    let flow_id = flow_id("waiting-internal");
    environment
        .client
        .start_flow(&workflow, &flow_id, 1)
        .expect("start waiting-channel Flow");
    environment
        .client
        .publish_many(&flow_id, &workflow.channel, [2, 3])
        .expect("publish waiting-channel messages");
    assert_eq!(
        6,
        environment
            .client
            .wait_for_flow_with_timeout::<i32>(&flow_id, Duration::from_secs(30))
            .expect("complete waiting-channel Flow")
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn signals_maps_and_skipped_timer_complete_flow() {
    let environment = DexDevTestEnvironment::start(Registry::new().register(SignalWorkflow::new()));
    let workflow = SignalWorkflow::new();
    let flow_id = flow_id("basic-signal");
    environment
        .client
        .start_flow(&workflow, &flow_id, 1)
        .expect("start signal Flow");
    environment
        .client
        .publish_many(&flow_id, &workflow.first, [2, 3, 5])
        .expect("publish first signals");
    environment
        .client
        .publish(&flow_id, &workflow.third, ())
        .expect("publish null signal");
    environment
        .client
        .publish_map(&flow_id, &workflow.signal_map, "one", [4])
        .expect("publish mapped signal");
    environment
        .client
        .skip_timer(
            &flow_id,
            StepExecutionId::of(&workflow.combination),
            TimerId::by_condition_id("test-timer-id"),
        )
        .expect("skip signal timer");
    assert_eq!(
        6,
        environment
            .client
            .wait_for_flow_with_timeout::<i32>(&flow_id, Duration::from_secs(30))
            .expect("complete signal Flow")
    );
    let closed = environment
        .client
        .publish(&flow_id, &workflow.first, 8)
        .expect_err("publishing to a closed Flow must fail");
    assert!(matches!(
        closed,
        SdkError::FlowNotFound { .. }
            | SdkError::Service {
                sub_status: ErrorSubStatus::FlowNotExists,
                ..
            }
    ));
}

#[test]
#[ignore = "requires dexcli dev"]
fn timer_waits_for_expected_duration() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(TimerWorkflow { start: TimerStep }));
    let workflow = TimerWorkflow { start: TimerStep };
    let flow_id = flow_id("basic-timer");
    let started_at = Instant::now();
    environment
        .client
        .start_flow(&workflow, &flow_id, 5)
        .expect("start timer Flow");
    environment
        .client
        .wait_for_step_completion(
            &flow_id,
            StepExecutionId::of(&workflow.start),
            Duration::from_secs(10),
        )
        .expect("wait for Timer Step");
    environment
        .client
        .wait_for_flow_with_timeout::<()>(&flow_id, Duration::from_secs(30))
        .expect("complete timer Flow");
    let elapsed = started_at.elapsed();
    assert!(
        (Duration::from_secs(4)..=Duration::from_secs(7)).contains(&elapsed),
        "actual duration: {elapsed:?}"
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn go_inter_step_channel_contract_completes_with_published_value() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(GoInterStepWorkflow::new()));
    let workflow = GoInterStepWorkflow::new();
    let flow_id = flow_id("go-inter-step");
    environment
        .client
        .start_flow(&workflow, &flow_id, ())
        .expect("start Go inter-Step channel Flow");
    assert_eq!(
        2,
        environment
            .client
            .wait_for_flow_with_timeout::<i32>(&flow_id, Duration::from_secs(30))
            .expect("complete Go inter-Step channel Flow")
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn go_channel_contract_reports_results_and_skipped_timer_by_index() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(GoChannelWorkflow::new()));
    let workflow = GoChannelWorkflow::new();
    let workflow_id = flow_id("go-channel");
    environment
        .client
        .start_flow(&workflow, &workflow_id, ())
        .expect("start Go channel compatibility Flow");
    environment
        .client
        .publish(&workflow_id, &workflow.second, 10)
        .expect("publish second-channel message");
    environment
        .client
        .wait_for_step_completion(
            &workflow_id,
            StepExecutionId::of(&workflow.start),
            Duration::from_secs(20),
        )
        .expect("wait for first channel Step");
    environment
        .client
        .publish(&workflow_id, &workflow.first, 100)
        .expect("publish first-channel message");
    let deadline = Instant::now() + Duration::from_secs(20);
    loop {
        if environment
            .client
            .skip_timer(
                &workflow_id,
                StepExecutionId::of(&workflow.finish),
                TimerId::by_condition_index(0),
            )
            .is_ok()
        {
            break;
        }
        assert!(Instant::now() < deadline, "SkipTimer did not become ready");
        std::thread::yield_now();
    }
    assert_eq!(
        100,
        environment
            .client
            .wait_for_flow_with_timeout::<i32>(&workflow_id, Duration::from_secs(30))
            .expect("complete Go channel compatibility Flow")
    );
    let missing = environment
        .client
        .publish(&flow_id("missing-channel-flow"), &workflow.first, 100)
        .expect_err("publishing to a missing Flow must fail");
    assert!(matches!(
        missing,
        SdkError::FlowNotFound { .. }
            | SdkError::Service {
                sub_status: ErrorSubStatus::FlowNotExists,
                ..
            }
    ));
}

#[test]
#[ignore = "requires dexcli dev"]
fn go_timer_contract_reports_firing_and_elapsed_time() {
    let environment = DexDevTestEnvironment::start(
        Registry::new().register(GoTimerWorkflow { start: GoTimerStep }),
    );
    let workflow = GoTimerWorkflow { start: GoTimerStep };
    let flow_id = flow_id("go-timer");
    let started_at = Instant::now();
    environment
        .client
        .start_flow(&workflow, &flow_id, 2)
        .expect("start Go timer compatibility Flow");
    assert_eq!(
        3,
        environment
            .client
            .wait_for_flow_with_timeout::<u64>(&flow_id, Duration::from_secs(30))
            .expect("complete Go timer compatibility Flow")
    );
    let elapsed = started_at.elapsed();
    assert!(elapsed >= Duration::from_millis(1_500));
    assert!(elapsed < Duration::from_secs(8));
}
