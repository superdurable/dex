// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::LazyLock;
use std::time::{Duration, Instant};

use dex_sdk::{
    Channel, ConditionCombination, Context, Flow, HandlerError, HandlerResult, PersistenceSchema,
    Registry, Rpc, RpcList, SdkError, Step, StepDecision, StepExecutionId, StepList, StepMovement,
    Timer, TimerId, Wait,
};

use crate::support::{DexDevTestEnvironment, flow_id};

static INTER_STEP_FIRST: LazyLock<Channel<i32>> =
    LazyLock::new(|| Channel::new("inter-step-first"));
static INTER_STEP_SECOND: LazyLock<Channel<i32>> =
    LazyLock::new(|| Channel::new("inter-step-second"));
static FIRST: LazyLock<Channel<i32>> = LazyLock::new(|| Channel::new("first"));
static SECOND: LazyLock<Channel<i32>> = LazyLock::new(|| Channel::new("second"));

struct InterStepChannelWorkflow {
    start: InterStepChannelStart,
    consumer: InterStepChannelConsumer,
    publisher: InterStepChannelPublisher,
}

impl InterStepChannelWorkflow {
    fn new() -> Self {
        Self {
            start: InterStepChannelStart,
            consumer: InterStepChannelConsumer,
            publisher: InterStepChannelPublisher,
        }
    }
}

impl Flow for InterStepChannelWorkflow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
            .and(&self.consumer)
            .and(&self.publisher)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .channel(&INTER_STEP_FIRST)
            .channel(&INTER_STEP_SECOND)
    }
}

struct InterStepChannelStart;

impl Step for InterStepChannelStart {
    type Input = ();

    fn execute(&self, _context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to_many([
            StepMovement::to(&InterStepChannelConsumer, ()),
            StepMovement::to(&InterStepChannelPublisher, 2),
        ]))
    }
}

struct InterStepChannelConsumer;

impl Step for InterStepChannelConsumer {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, (): ()) -> HandlerResult<Wait> {
        Ok(Wait::any_of([
            INTER_STEP_FIRST.for_one(),
            INTER_STEP_SECOND.for_one(),
        ]))
    }

    fn execute(&self, context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        let first = INTER_STEP_FIRST.condition_results(context)?;
        let second = INTER_STEP_SECOND.condition_results(context)?;
        if !first.is_empty() || second != [2] {
            return Err(HandlerError::new(
                "ChannelsFailure",
                format!("unexpected channel results: first={first:?} second={second:?}"),
            ));
        }
        Ok(StepDecision::graceful_complete(second[0]))
    }
}

struct InterStepChannelPublisher;

impl Step for InterStepChannelPublisher {
    type Input = i32;

    fn wait_for(&self, context: &mut Context, input: i32) -> HandlerResult<Wait> {
        INTER_STEP_SECOND.publish(context, input)?;
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, _context: &mut Context, _input: i32) -> HandlerResult<StepDecision> {
        Ok(StepDecision::dead_end())
    }
}

struct ChannelWorkflow {
    start: ChannelFirstStep,
    finish: ChannelSecondStep,
}

impl ChannelWorkflow {
    const PUBLISH_FIRST: Rpc<i32, ()> = Rpc::new("publish_first");
    const PUBLISH_SECOND: Rpc<i32, ()> = Rpc::new("publish_second");

    fn new() -> Self {
        Self {
            start: ChannelFirstStep,
            finish: ChannelSecondStep,
        }
    }

    fn publish_first(&self, context: &mut Context, input: i32) -> HandlerResult<()> {
        FIRST.publish(context, input)
    }

    fn publish_second(&self, context: &mut Context, input: i32) -> HandlerResult<()> {
        SECOND.publish(context, input)
    }
}

impl Flow for ChannelWorkflow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start).and(&self.finish)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().channel(&FIRST).channel(&SECOND)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .procedure(Self::PUBLISH_FIRST, Self::publish_first)
            .procedure(Self::PUBLISH_SECOND, Self::publish_second)
    }
}

struct ChannelFirstStep;

impl Step for ChannelFirstStep {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, (): ()) -> HandlerResult<Wait> {
        Ok(Wait::any_of([FIRST.for_one(), SECOND.for_one()]))
    }

    fn execute(&self, context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        let first = FIRST.condition_results(context)?;
        let second = SECOND.condition_results(context)?;
        if !first.is_empty() || second != [10] {
            return Err(HandlerError::new(
                "ChannelsFailure",
                format!("unexpected first-step channel results: first={first:?} second={second:?}"),
            ));
        }
        Ok(StepDecision::go_to(&ChannelSecondStep, ()))
    }
}

struct ChannelSecondStep;

impl Step for ChannelSecondStep {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, (): ()) -> HandlerResult<Wait> {
        Ok(Wait::any_combination_of([ConditionCombination::all_of([
            FIRST.for_one().with_id("first"),
            Timer::by_duration(Duration::from_secs(24 * 60 * 60)).with_id("finish-timer"),
        ])]))
    }

    fn execute(&self, context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        if !context.has_any_timer_fired() || !context.has_timer_fired(0) {
            return Err(HandlerError::new(
                "ChannelsFailure",
                "skipped timer was not reported as fired",
            ));
        }
        let first = FIRST.condition_results(context)?;
        let second = SECOND.condition_results(context)?;
        if first != [100] || !second.is_empty() {
            return Err(HandlerError::new(
                "ChannelsFailure",
                format!(
                    "unexpected second-step channel results: first={first:?} second={second:?}"
                ),
            ));
        }
        Ok(StepDecision::graceful_complete(first[0]))
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
        Ok(Wait::until(Timer::by_duration(Duration::from_secs(input))))
    }

    fn execute(&self, context: &mut Context, input: u64) -> HandlerResult<StepDecision> {
        if !context.has_any_timer_fired() || !context.has_timer_fired(0) {
            return Err(HandlerError::new(
                "ChannelsFailure",
                "natural timer was not reported as fired",
            ));
        }
        Ok(StepDecision::graceful_complete(input + 1))
    }
}

#[test]
#[ignore = "requires dexcli dev"]
fn inter_step_channel_contract_completes_with_published_value() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(InterStepChannelWorkflow::new()));
    let workflow = InterStepChannelWorkflow::new();
    let flow_id = flow_id("go-inter-step");
    environment
        .client
        .start_flow(&workflow, &flow_id, ())
        .expect("start Go inter-Step channel Flow");
    assert_eq!(
        2,
        environment
            .client
            .wait_for_flow_with_timeout(&flow_id, Duration::from_secs(30))
            .and_then(|result| result.single_output::<i32>())
            .expect("complete Go inter-Step channel Flow")
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn channel_contract_reports_results_and_skipped_timer_by_index() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(ChannelWorkflow::new()));
    let workflow = ChannelWorkflow::new();
    let missing_flow_id = flow_id("missing-channel-flow");
    let flow_id = flow_id("go-channel");
    environment
        .client
        .start_flow(&workflow, &flow_id, ())
        .expect("start Go channel compatibility Flow");
    environment
        .client
        .invoke_rpc(&flow_id, ChannelWorkflow::PUBLISH_SECOND, 10)
        .expect("publish second-channel message");
    environment
        .client
        .wait_for_step_completion(
            &flow_id,
            StepExecutionId::of(&workflow.start),
            dex_sdk::WaitForStepCompletionOptions::new()
                .request_id(format!("{flow_id}-wait-first-channel-step"))
                .maximum_wait_time(Duration::from_secs(20)),
        )
        .expect("wait for first channel Step");
    environment
        .client
        .invoke_rpc(&flow_id, ChannelWorkflow::PUBLISH_FIRST, 100)
        .expect("publish first-channel message");
    let deadline = Instant::now() + Duration::from_secs(20);
    loop {
        if environment
            .client
            .skip_timer(
                &flow_id,
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
            .wait_for_flow_with_timeout(&flow_id, Duration::from_secs(30))
            .and_then(|result| result.single_output::<i32>())
            .expect("complete Go channel compatibility Flow")
    );
    let missing = environment
        .client
        .invoke_rpc(&missing_flow_id, ChannelWorkflow::PUBLISH_FIRST, 100)
        .expect_err("publishing to a missing Flow must fail");
    assert!(matches!(missing, SdkError::FlowNotActive { .. }));
}

#[test]
#[ignore = "requires dexcli dev"]
fn timer_contract_reports_firing_and_elapsed_time() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(TimerWorkflow { start: TimerStep }));
    let workflow = TimerWorkflow { start: TimerStep };
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
            .wait_for_flow_with_timeout(&flow_id, Duration::from_secs(30))
            .and_then(|result| result.single_output::<u64>())
            .expect("complete Go timer compatibility Flow")
    );
    let elapsed = started_at.elapsed();
    assert!(elapsed >= Duration::from_millis(1_500));
    assert!(elapsed < Duration::from_secs(8));
}
