// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::time::{Duration, Instant};

use dex_sdk::{
    Channel, ConditionCombination, Context, Flow, HandlerError, HandlerResult, PersistenceSchema,
    Registry, SdkError, Step, StepDecision, StepExecutionId, StepList, StepMovement, Timer,
    TimerId, Wait,
};

use crate::support::{DexDevTestEnvironment, flow_id};

struct InterStepChannelWorkflow {
    first: Channel<i32>,
    second: Channel<i32>,
    start: InterStepChannelStart,
    consumer: InterStepChannelConsumer,
    publisher: InterStepChannelPublisher,
}

impl InterStepChannelWorkflow {
    fn new() -> Self {
        let first = Channel::new("inter-step-first");
        let second = Channel::new("inter-step-second");
        Self {
            start: InterStepChannelStart,
            consumer: InterStepChannelConsumer {
                first: first.clone(),
                second: second.clone(),
            },
            publisher: InterStepChannelPublisher {
                second: second.clone(),
            },
            first,
            second,
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
            .channel(&self.first)
            .channel(&self.second)
    }
}

struct InterStepChannelStart;

impl Step for InterStepChannelStart {
    type Input = ();

    fn execute(&self, _context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to_many([
            StepMovement::to(
                &InterStepChannelConsumer {
                    first: Channel::new("inter-step-first"),
                    second: Channel::new("inter-step-second"),
                },
                (),
            ),
            StepMovement::to(
                &InterStepChannelPublisher {
                    second: Channel::new("inter-step-second"),
                },
                2,
            ),
        ]))
    }
}

struct InterStepChannelConsumer {
    first: Channel<i32>,
    second: Channel<i32>,
}

impl Step for InterStepChannelConsumer {
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

struct InterStepChannelPublisher {
    second: Channel<i32>,
}

impl Step for InterStepChannelPublisher {
    type Input = i32;

    fn wait_for(&self, context: &mut Context, input: i32) -> HandlerResult<Wait> {
        self.second.publish(context, input)?;
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, _context: &mut Context, _input: i32) -> HandlerResult<StepDecision> {
        Ok(StepDecision::dead_end())
    }
}

struct ChannelWorkflow {
    first: Channel<i32>,
    second: Channel<i32>,
    start: ChannelFirstStep,
    finish: ChannelSecondStep,
}

impl ChannelWorkflow {
    fn new() -> Self {
        let first = Channel::new("first");
        let second = Channel::new("second");
        Self {
            start: ChannelFirstStep {
                first: first.clone(),
                second: second.clone(),
            },
            finish: ChannelSecondStep {
                first: first.clone(),
                second: second.clone(),
            },
            first,
            second,
        }
    }
}

impl Flow for ChannelWorkflow {
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

struct ChannelFirstStep {
    first: Channel<i32>,
    second: Channel<i32>,
}

impl Step for ChannelFirstStep {
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
            &ChannelSecondStep {
                first: self.first.clone(),
                second: self.second.clone(),
            },
            (),
        ))
    }
}

struct ChannelSecondStep {
    first: Channel<i32>,
    second: Channel<i32>,
}

impl Step for ChannelSecondStep {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, (): ()) -> HandlerResult<Wait> {
        Ok(Wait::any_combination_of([ConditionCombination::all_of([
            self.first.for_one().with_id("first"),
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
            return Err(HandlerError::new("natural timer was not reported as fired"));
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
        .publish(&flow_id, &workflow.second, 10)
        .expect("publish second-channel message");
    environment
        .client
        .wait_for_step_completion(
            &flow_id,
            StepExecutionId::of(&workflow.start),
            Duration::from_secs(20),
        )
        .expect("wait for first channel Step");
    environment
        .client
        .publish(&flow_id, &workflow.first, 100)
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
        .publish(&missing_flow_id, &workflow.first, 100)
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
