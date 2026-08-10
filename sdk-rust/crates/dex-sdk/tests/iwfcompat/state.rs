// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use std::sync::Mutex;
use std::time::Duration;

use dex_sdk::{
    Attribute, Channel, Client, Context, Flow, FlowErrorType, FlowStatus, HandlerError,
    HandlerResult, PersistenceSchema, Registry, RetryPolicy, SdkError, SdkResult, StartFlowOptions,
    Step, StepDecision, StepDurability, StepList, StepMovement, StepOptions, StopFlowOptions, Wait,
    WaitForFailurePolicy,
};

use crate::channels::SignalWorkflow;
use crate::support::{DexDevTestEnvironment, flow_id};

struct StateOptionsWorkflow {
    wait_value: Attribute<String>,
    execute_value: Attribute<String>,
    both_value: Attribute<String>,
    first: StateOptionsFirstStep,
    second: StateOptionsSecondStep,
    third: StateOptionsThirdStep,
}

impl StateOptionsWorkflow {
    fn new() -> Self {
        let wait_value = Attribute::new("DA_WAIT_UNTIL");
        let execute_value = Attribute::new("DA_EXECUTE");
        let both_value = Attribute::new("DA_BOTH");
        Self {
            first: StateOptionsFirstStep {
                wait_value: wait_value.clone(),
                execute_value: execute_value.clone(),
                both_value: both_value.clone(),
            },
            second: StateOptionsSecondStep {
                wait_value: wait_value.clone(),
                execute_value: execute_value.clone(),
                both_value: both_value.clone(),
            },
            third: StateOptionsThirdStep {
                both_value: both_value.clone(),
            },
            wait_value,
            execute_value,
            both_value,
        }
    }
}

impl Flow for StateOptionsWorkflow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.first)
            .and(&self.second)
            .and(&self.third)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&self.wait_value)
            .attribute(&self.execute_value)
            .attribute(&self.both_value)
    }
}

struct StateOptionsFirstStep {
    wait_value: Attribute<String>,
    execute_value: Attribute<String>,
    both_value: Attribute<String>,
}

impl Step for StateOptionsFirstStep {
    type Input = ();

    fn execute(&self, context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        self.execute_value.set(context, "execute".into())?;
        self.wait_value.set(context, "wait_until".into())?;
        self.both_value.set(context, "both".into())?;
        Ok(StepDecision::go_to(
            &StateOptionsSecondStep {
                wait_value: self.wait_value.clone(),
                execute_value: self.execute_value.clone(),
                both_value: self.both_value.clone(),
            },
            (),
        ))
    }
}

struct StateOptionsSecondStep {
    wait_value: Attribute<String>,
    execute_value: Attribute<String>,
    both_value: Attribute<String>,
}

impl Step for StateOptionsSecondStep {
    type Input = ();

    fn wait_for(&self, context: &mut Context, (): ()) -> HandlerResult<Wait> {
        require_attribute(context, &self.wait_value, "wait_until")?;
        require_attribute(context, &self.execute_value, "execute")?;
        require_attribute(context, &self.both_value, "both")?;
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        require_attribute(context, &self.execute_value, "execute")?;
        require_attribute(context, &self.wait_value, "wait_until")?;
        require_attribute(context, &self.both_value, "both")?;
        Ok(StepDecision::go_to(
            &StateOptionsThirdStep {
                both_value: self.both_value.clone(),
            },
            (),
        ))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .wait_for_lock(self.wait_value.lock())
            .execute_lock(self.execute_value.lock())
    }
}

struct StateOptionsThirdStep {
    both_value: Attribute<String>,
}

impl Step for StateOptionsThirdStep {
    type Input = ();

    fn wait_for(&self, context: &mut Context, (): ()) -> HandlerResult<Wait> {
        require_attribute(context, &self.both_value, "both")?;
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        require_attribute(context, &self.both_value, "both")?;
        Ok(StepDecision::graceful_complete("success".to_string()))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .wait_for_lock(self.both_value.lock())
            .execute_lock(self.both_value.lock())
    }
}

fn require_attribute(
    context: &Context,
    attribute: &Attribute<String>,
    expected: &str,
) -> HandlerResult<()> {
    let actual = attribute.get_required(context)?;
    if actual != expected {
        return Err(HandlerError::new(format!(
            "Attribute was {actual}, expected {expected}"
        )));
    }
    Ok(())
}

struct StateOptionsLockingWorkflow {
    wait_for_count: Attribute<i32>,
    execute_count: Attribute<i32>,
    completed: Channel<()>,
    start: StateOptionsLockingStartStep,
    locked: StateOptionsLockedStep,
    complete: StateOptionsLockingCompleteStep,
}

impl StateOptionsLockingWorkflow {
    fn new() -> Self {
        let wait_for_count = Attribute::new("step-lock-wait-for-count");
        let execute_count = Attribute::new("step-lock-execute-count");
        let completed = Channel::new("step-lock-completed");
        Self {
            start: StateOptionsLockingStartStep,
            locked: StateOptionsLockedStep {
                wait_for_count: wait_for_count.clone(),
                execute_count: execute_count.clone(),
                completed: completed.clone(),
            },
            complete: StateOptionsLockingCompleteStep {
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

struct StateOptionsLockingStartStep;

impl Step for StateOptionsLockingStartStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, parallelism: i32) -> HandlerResult<StepDecision> {
        let mut movements = (0..parallelism)
            .map(|index| {
                StepMovement::to(
                    &StateOptionsLockedStep {
                        wait_for_count: Attribute::new("step-lock-wait-for-count"),
                        execute_count: Attribute::new("step-lock-execute-count"),
                        completed: Channel::new("step-lock-completed"),
                    },
                    index,
                )
            })
            .collect::<Vec<_>>();
        movements.push(StepMovement::to(
            &StateOptionsLockingCompleteStep {
                wait_for_count: Attribute::new("step-lock-wait-for-count"),
                execute_count: Attribute::new("step-lock-execute-count"),
                completed: Channel::new("step-lock-completed"),
            },
            parallelism,
        ));
        Ok(StepDecision::go_to_many(movements))
    }
}

struct StateOptionsLockedStep {
    wait_for_count: Attribute<i32>,
    execute_count: Attribute<i32>,
    completed: Channel<()>,
}

impl Step for StateOptionsLockedStep {
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

struct StateOptionsLockingCompleteStep {
    wait_for_count: Attribute<i32>,
    execute_count: Attribute<i32>,
    completed: Channel<()>,
}

impl Step for StateOptionsLockingCompleteStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, parallelism: i32) -> HandlerResult<Wait> {
        Ok(Wait::until(self.completed.for_n(parallelism as usize)))
    }

    fn execute(&self, context: &mut Context, parallelism: i32) -> HandlerResult<StepDecision> {
        if self.completed.condition_results(context)?.len() != parallelism as usize {
            return Err(HandlerError::new("not all locked Steps completed"));
        }
        Ok(StepDecision::graceful_complete(format!(
            "{}:{}",
            self.wait_for_count.get_required(context)?,
            self.execute_count.get_required(context)?
        )))
    }
}

struct StateRecoveryWorkflow {
    start: StateRecoveryFailingStep,
    recover: StateRecoveryStep,
}

impl Flow for StateRecoveryWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start).and(&self.recover)
    }
}

struct StateRecoveryFailingStep;

impl Step for StateRecoveryFailingStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, _input: i32) -> HandlerResult<Wait> {
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, _context: &mut Context, _input: i32) -> HandlerResult<StepDecision> {
        Err(HandlerError::new("execute failure"))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .execute_retry(
                RetryPolicy::new()
                    .maximum_attempts(1)
                    .backoff_coefficient(2.0),
            )
            .on_execute_failure_proceed_to(&StateRecoveryStep)
    }
}

struct StateRecoveryStep;

impl Step for StateRecoveryStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, _input: i32) -> HandlerResult<Wait> {
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, _context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        if input == 10 {
            return Ok(StepDecision::graceful_complete(input));
        }
        if input == 5 {
            return Ok(StepDecision::go_to(&StateRecoveryFailingStep, input * 2));
        }
        Ok(StepDecision::force_fail(format!(
            "unexpected input {input}"
        )))
    }
}

struct StateRecoveryNoWaitWorkflow {
    start: StateRecoveryNoWaitFailingStep,
    recover: StateRecoveryNoWaitStep,
}

impl Flow for StateRecoveryNoWaitWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start).and(&self.recover)
    }
}

struct StateRecoveryNoWaitFailingStep;

impl Step for StateRecoveryNoWaitFailingStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, _input: i32) -> HandlerResult<StepDecision> {
        Err(HandlerError::new("execute failure"))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .execute_retry(RetryPolicy::new().maximum_attempts(1))
            .on_execute_failure_proceed_to(&StateRecoveryNoWaitStep)
    }
}

struct StateRecoveryNoWaitStep;

impl Step for StateRecoveryNoWaitStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        if input == 10 {
            return Ok(StepDecision::graceful_complete(input));
        }
        if input == 5 {
            return Ok(StepDecision::go_to(
                &StateRecoveryNoWaitFailingStep,
                input * 2,
            ));
        }
        Ok(StepDecision::force_fail(format!(
            "unexpected input {input}"
        )))
    }
}

struct GoExecuteRecoveryWorkflow {
    start: GoExecuteRecoveryFailStep,
    finish: GoExecuteRecoveryFinishStep,
}

impl Flow for GoExecuteRecoveryWorkflow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start).and(&self.finish)
    }
}

struct GoExecuteRecoveryFailStep;

impl Step for GoExecuteRecoveryFailStep {
    type Input = String;

    fn execute(&self, _context: &mut Context, _input: String) -> HandlerResult<StepDecision> {
        Err(HandlerError::new("planned Execute failure"))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .execute_retry(RetryPolicy::new().maximum_attempts(1))
            .on_execute_failure_proceed_to(&GoExecuteRecoveryFinishStep)
    }
}

struct GoExecuteRecoveryFinishStep;

impl Step for GoExecuteRecoveryFinishStep {
    type Input = String;

    fn execute(&self, _context: &mut Context, _input: String) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(
            "this is flow step 2".to_string(),
        ))
    }
}

struct StateOptionsOverrideWorkflow {
    first: StateOptionsOverrideFirstStep,
    second: StateOptionsOverrideSecondStep,
}

impl StateOptionsOverrideWorkflow {
    fn new() -> Self {
        Self {
            first: StateOptionsOverrideFirstStep {
                output: Mutex::new(String::new()),
            },
            second: StateOptionsOverrideSecondStep {
                output: Mutex::new(String::new()),
            },
        }
    }
}

impl Flow for StateOptionsOverrideWorkflow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.first).and(&self.second)
    }
}

struct StateOptionsOverrideFirstStep {
    output: Mutex<String>,
}

impl Step for StateOptionsOverrideFirstStep {
    type Input = String;

    fn wait_for(&self, _context: &mut Context, input: String) -> HandlerResult<Wait> {
        *self.output.lock().expect("first output lock") = format!("{input}_state1_start");
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, _context: &mut Context, _input: String) -> HandlerResult<StepDecision> {
        let options: StepOptions<String> = StepOptions::new()
            .wait_for_retry(RetryPolicy::new().maximum_attempts(2))
            .wait_for_failure(WaitForFailurePolicy::Proceed);
        let mut output = self.output.lock().expect("first output lock");
        output.push_str("_state1_decide");
        Ok(StepDecision::go_to_many([StepMovement::to_with_options(
            &StateOptionsOverrideSecondStep {
                output: Mutex::new(String::new()),
            },
            output.clone(),
            options,
        )]))
    }
}

struct StateOptionsOverrideSecondStep {
    output: Mutex<String>,
}

impl Step for StateOptionsOverrideSecondStep {
    type Input = String;

    fn wait_for(&self, _context: &mut Context, input: String) -> HandlerResult<Wait> {
        *self.output.lock().expect("second output lock") = format!("{input}_state2_start");
        Err(HandlerError::new("state 2 wait failure"))
    }

    fn execute(&self, context: &mut Context, _input: String) -> HandlerResult<StepDecision> {
        if !context.wait_for_method_failed() {
            return Err(HandlerError::new("waitFor failure was not reported"));
        }
        let mut output = self.output.lock().expect("second output lock");
        output.push_str("_state2_decide");
        Ok(StepDecision::graceful_complete(output.clone()))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .wait_for_retry(RetryPolicy::new().maximum_attempts(1))
            .wait_for_failure(WaitForFailurePolicy::FailFlow)
    }
}

struct WorkflowUncompletedForceFailWorkflow {
    start: WorkflowUncompletedForceFailStep,
}

impl Flow for WorkflowUncompletedForceFailWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }
}

struct WorkflowUncompletedForceFailStep;

impl Step for WorkflowUncompletedForceFailStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, _input: i32) -> HandlerResult<StepDecision> {
        Ok(StepDecision::force_fail("a failing message"))
    }
}

struct WorkflowUncompletedWaitForFailureWorkflow {
    start: WorkflowUncompletedWaitForFailureStep,
}

impl Flow for WorkflowUncompletedWaitForFailureWorkflow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }
}

struct WorkflowUncompletedWaitForFailureStep;

impl Step for WorkflowUncompletedWaitForFailureStep {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, (): ()) -> HandlerResult<Wait> {
        Err(HandlerError::new("test WaitFor failing"))
    }

    fn execute(&self, _context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::force_fail("must not execute"))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().wait_for_retry(RetryPolicy::new().maximum_attempts(1))
    }
}

struct WorkflowUncompletedWaitForTimeoutWorkflow {
    start: WorkflowUncompletedWaitForTimeoutStep,
}

impl Flow for WorkflowUncompletedWaitForTimeoutWorkflow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }
}

struct WorkflowUncompletedWaitForTimeoutStep;

impl Step for WorkflowUncompletedWaitForTimeoutStep {
    type Input = ();

    fn wait_for(&self, context: &mut Context, (): ()) -> HandlerResult<Wait> {
        context.wait_for_cancellation();
        Err(HandlerError::new("wait_for invocation was cancelled"))
    }

    fn execute(&self, _context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::force_fail("must not execute"))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .wait_for_method_timeout(Duration::from_secs(1))
            .wait_for_retry(RetryPolicy::new().maximum_attempts(1))
    }
}

struct WorkflowUncompletedStateTimeoutStep;

impl Step for WorkflowUncompletedStateTimeoutStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        std::thread::sleep(Duration::from_secs(2));
        Ok(StepDecision::graceful_complete(input))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .execute_method_timeout(Duration::from_secs(1))
            .execute_retry(RetryPolicy::new().maximum_attempts(1))
            .execute_durability(StepDurability::Sync)
    }
}

struct WorkflowUncompletedStateTimeoutWorkflow {
    start: WorkflowUncompletedStateTimeoutStep,
}

impl Flow for WorkflowUncompletedStateTimeoutWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }
}

struct WorkflowUncompletedStateFailureWorkflow {
    start: WorkflowUncompletedStateFailureStep,
}

impl Flow for WorkflowUncompletedStateFailureWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }
}

struct WorkflowUncompletedStateFailureStep;

impl Step for WorkflowUncompletedStateFailureStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, _input: i32) -> HandlerResult<Wait> {
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, _context: &mut Context, _input: i32) -> HandlerResult<StepDecision> {
        Err(HandlerError::new("test api failing"))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_retry(RetryPolicy::new().maximum_attempts(1))
    }
}

struct WorkflowUncompletedEmptyDecisionWorkflow {
    start: WorkflowUncompletedEmptyDecisionStep,
}

impl Flow for WorkflowUncompletedEmptyDecisionWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }
}

struct WorkflowUncompletedEmptyDecisionStep;

impl Step for WorkflowUncompletedEmptyDecisionStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, _input: i32) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to_many(std::iter::empty()))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_retry(RetryPolicy::new().maximum_attempts(1))
    }
}

fn compile_state_options_test(client: &Client) -> SdkResult<()> {
    let workflow = StateOptionsWorkflow::new();
    client.start_flow(&workflow, "state-options", ())?;
    let output: String = client.wait_for_flow("state-options")?;
    assert_eq!("success", output);
    Ok(())
}

fn compile_state_recovery_test(client: &Client) -> SdkResult<()> {
    let workflow = StateRecoveryWorkflow {
        start: StateRecoveryFailingStep,
        recover: StateRecoveryStep,
    };
    client.start_flow(&workflow, "state-recovery", 5)?;
    let output: i32 = client.wait_for_flow("state-recovery")?;
    assert_eq!(10, output);

    let no_wait = StateRecoveryNoWaitWorkflow {
        start: StateRecoveryNoWaitFailingStep,
        recover: StateRecoveryNoWaitStep,
    };
    client.start_flow(&no_wait, "state-recovery-no-wait", 5)?;
    let no_wait_output: i32 = client.wait_for_flow("state-recovery-no-wait")?;
    assert_eq!(10, no_wait_output);
    Ok(())
}

fn compile_workflow_uncompleted_test(client: &Client) -> SdkResult<()> {
    let force_fail = WorkflowUncompletedForceFailWorkflow {
        start: WorkflowUncompletedForceFailStep,
    };
    client.start_flow(&force_fail, "force-fail", 5)?;

    let worker_failure = WorkflowUncompletedStateFailureWorkflow {
        start: WorkflowUncompletedStateFailureStep,
    };
    client.start_flow(&worker_failure, "worker-failure", 5)?;

    let timeout = WorkflowUncompletedStateTimeoutWorkflow {
        start: WorkflowUncompletedStateTimeoutStep,
    };
    client.start_flow(&timeout, "worker-timeout", 5)?;

    let empty = WorkflowUncompletedEmptyDecisionWorkflow {
        start: WorkflowUncompletedEmptyDecisionStep,
    };
    client.start_flow(&empty, "empty-decision", 5)?;
    Ok(())
}

fn compile_state_options_override_test(client: &Client) -> SdkResult<()> {
    let workflow = StateOptionsOverrideWorkflow::new();
    client.start_flow(&workflow, "options-override", "input".into())?;
    let output: String = client.wait_for_flow("options-override")?;
    assert_eq!(
        "input_state1_start_state1_decide_state2_start_state2_decide",
        output
    );
    Ok(())
}

#[test]
#[ignore = "requires dexcli dev"]
fn step_options_load_locked_attributes_for_both_methods() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(StateOptionsWorkflow::new()));
    let workflow = StateOptionsWorkflow::new();
    let flow_id = flow_id("state-options");
    environment
        .client
        .start_flow(&workflow, &flow_id, ())
        .expect("start state-options Flow");
    assert_eq!(
        "success",
        environment
            .client
            .wait_for_flow_with_timeout::<String>(&flow_id, Duration::from_secs(30))
            .expect("complete state-options Flow")
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn wait_for_and_execute_locks_serialize_parallel_steps() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(StateOptionsLockingWorkflow::new()));
    let workflow = StateOptionsLockingWorkflow::new();
    let flow_id = flow_id("state-options-locks");
    let parallelism = 20;
    environment
        .client
        .start_flow(&workflow, &flow_id, parallelism)
        .expect("start step-locking Flow");
    assert_eq!(
        "20:20",
        environment
            .client
            .wait_for_flow_with_timeout::<String>(&flow_id, Duration::from_secs(30))
            .expect("complete step-locking Flow")
    );
    assert_eq!(
        Some(parallelism),
        environment
            .client
            .get_attribute(&flow_id, &workflow.wait_for_count)
            .expect("read waitFor count")
    );
    assert_eq!(
        Some(parallelism),
        environment
            .client
            .get_attribute(&flow_id, &workflow.execute_count)
            .expect("read execute count")
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn movement_options_override_step_defaults() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(StateOptionsOverrideWorkflow::new()));
    let workflow = StateOptionsOverrideWorkflow::new();
    let flow_id = flow_id("state-options-override");
    environment
        .client
        .start_flow(&workflow, &flow_id, "input".to_string())
        .expect("start options-override Flow");
    assert_eq!(
        "input_state1_start_state1_decide_state2_start_state2_decide",
        environment
            .client
            .wait_for_flow_with_timeout::<String>(&flow_id, Duration::from_secs(30))
            .expect("complete options-override Flow")
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn execute_failure_recovers_after_wait_for() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(StateRecoveryWorkflow {
            start: StateRecoveryFailingStep,
            recover: StateRecoveryStep,
        }));
    let workflow = StateRecoveryWorkflow {
        start: StateRecoveryFailingStep,
        recover: StateRecoveryStep,
    };
    let flow_id = flow_id("state-recovery");
    environment
        .client
        .start_flow(&workflow, &flow_id, 5)
        .expect("start state-recovery Flow");
    assert_eq!(
        10,
        environment
            .client
            .wait_for_flow_with_timeout::<i32>(&flow_id, Duration::from_secs(30))
            .expect("complete state-recovery Flow")
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn execute_only_failure_recovers() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(StateRecoveryNoWaitWorkflow {
            start: StateRecoveryNoWaitFailingStep,
            recover: StateRecoveryNoWaitStep,
        }));
    let workflow = StateRecoveryNoWaitWorkflow {
        start: StateRecoveryNoWaitFailingStep,
        recover: StateRecoveryNoWaitStep,
    };
    let flow_id = flow_id("state-recovery-no-wait");
    environment
        .client
        .start_flow(&workflow, &flow_id, 5)
        .expect("start execute-only recovery Flow");
    assert_eq!(
        10,
        environment
            .client
            .wait_for_flow_with_timeout::<i32>(&flow_id, Duration::from_secs(30))
            .expect("complete execute-only recovery Flow")
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn go_execute_recovery_contract_preserves_input_type_and_completes() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(GoExecuteRecoveryWorkflow {
            start: GoExecuteRecoveryFailStep,
            finish: GoExecuteRecoveryFinishStep,
        }));
    let workflow = GoExecuteRecoveryWorkflow {
        start: GoExecuteRecoveryFailStep,
        finish: GoExecuteRecoveryFinishStep,
    };
    let flow_id = flow_id("go-execute-recovery");
    environment
        .client
        .start_flow(&workflow, &flow_id, "unchanged input".to_string())
        .expect("start Go execute-recovery Flow");
    assert_eq!(
        "this is flow step 2",
        environment
            .client
            .wait_for_flow_with_timeout::<String>(&flow_id, Duration::from_secs(30))
            .expect("complete Go execute-recovery Flow")
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn waiting_for_flow_can_time_out_without_stopping_flow() {
    let environment = DexDevTestEnvironment::start(Registry::new().register(SignalWorkflow::new()));
    let workflow = SignalWorkflow::new();
    let flow_id = flow_id("wait-timeout");
    environment
        .client
        .start_flow(&workflow, &flow_id, 1)
        .expect("start waiting Flow");
    let failure = environment
        .client
        .wait_for_flow_with_timeout::<i32>(&flow_id, Duration::from_secs(1))
        .expect_err("waitForFlow must time out");
    match failure {
        SdkError::LongPollTimeout {
            flow_id: failed_flow_id,
            ..
        } => assert_eq!(flow_id, failed_flow_id),
        other => panic!("expected LongPollTimeout, got {other:?}"),
    }
}

#[test]
#[ignore = "requires dexcli dev"]
fn flow_timeout_reports_run_and_status() {
    let environment = DexDevTestEnvironment::start(Registry::new().register(SignalWorkflow::new()));
    let workflow = SignalWorkflow::new();
    let flow_id = flow_id("flow-timeout");
    let run_id = environment
        .client
        .start_flow_with_options(
            &workflow,
            &flow_id,
            1,
            StartFlowOptions::new().timeout(Duration::from_secs(1)),
        )
        .expect("start timed Flow");
    assert_flow_failure(
        wait_for_failure(&environment, &flow_id),
        &run_id,
        FlowStatus::TimedOut,
        None,
        None,
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn canceled_flow_reports_canceled_without_error() {
    assert_stopped_flow(StopFlowOptions::cancel(), FlowStatus::Canceled, None, None);
}

#[test]
#[ignore = "requires dexcli dev"]
fn terminated_flow_reports_reasonless_termination() {
    assert_stopped_flow(
        StopFlowOptions::terminate().reason("terminated"),
        FlowStatus::Terminated,
        None,
        None,
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn client_failed_flow_reports_reason() {
    assert_stopped_flow(
        StopFlowOptions::fail().reason("fail by API"),
        FlowStatus::Failed,
        Some(FlowErrorType::ClientApiFailed),
        Some("fail by API"),
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn force_fail_decision_reports_step_decision_failure() {
    let environment = DexDevTestEnvironment::start(Registry::new().register(
        WorkflowUncompletedForceFailWorkflow {
            start: WorkflowUncompletedForceFailStep,
        },
    ));
    let workflow = WorkflowUncompletedForceFailWorkflow {
        start: WorkflowUncompletedForceFailStep,
    };
    let flow_id = flow_id("force-fail");
    let run_id = environment
        .client
        .start_flow(&workflow, &flow_id, 5)
        .expect("start force-fail Flow");
    assert_flow_failure(
        wait_for_failure(&environment, &flow_id),
        &run_id,
        FlowStatus::Failed,
        Some(FlowErrorType::StepDecisionFailed),
        Some("a failing message"),
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn worker_api_failure_reports_handler_message() {
    let environment = DexDevTestEnvironment::start(Registry::new().register(
        WorkflowUncompletedStateFailureWorkflow {
            start: WorkflowUncompletedStateFailureStep,
        },
    ));
    let workflow = WorkflowUncompletedStateFailureWorkflow {
        start: WorkflowUncompletedStateFailureStep,
    };
    let flow_id = flow_id("worker-api-failure");
    let run_id = environment
        .client
        .start_flow(&workflow, &flow_id, 5)
        .expect("start worker-failure Flow");
    let failure = wait_for_failure(&environment, &flow_id);
    match failure {
        SdkError::FlowUncompleted {
            run_id: failed_run_id,
            status,
            error_type,
            message,
            result_count,
        } => {
            assert_eq!(run_id, failed_run_id);
            assert_eq!(FlowStatus::Failed, status);
            assert_eq!(Some(FlowErrorType::WorkerApiFailed), error_type);
            assert!(
                message
                    .as_deref()
                    .is_some_and(|message| message.contains("test api failing"))
            );
            assert_eq!(0, result_count);
        }
        other => panic!("expected FlowUncompleted, got {other:?}"),
    }
}

#[test]
#[ignore = "requires dexcli dev"]
fn wait_for_failure_reports_handler_message() {
    let environment = DexDevTestEnvironment::start(Registry::new().register(
        WorkflowUncompletedWaitForFailureWorkflow {
            start: WorkflowUncompletedWaitForFailureStep,
        },
    ));
    let workflow = WorkflowUncompletedWaitForFailureWorkflow {
        start: WorkflowUncompletedWaitForFailureStep,
    };
    let flow_id = flow_id("wait-for-failure");
    let run_id = environment
        .client
        .start_flow(&workflow, &flow_id, ())
        .expect("start waitFor-failure Flow");
    let failure = wait_for_failure(&environment, &flow_id);
    match failure {
        SdkError::FlowUncompleted {
            run_id: failed_run_id,
            status,
            error_type,
            message,
            result_count,
        } => {
            assert_eq!(run_id, failed_run_id);
            assert_eq!(FlowStatus::Failed, status);
            assert_eq!(Some(FlowErrorType::WorkerApiFailed), error_type);
            assert!(
                message
                    .as_deref()
                    .is_some_and(|message| message.contains("test WaitFor failing"))
            );
            assert_eq!(0, result_count);
        }
        other => panic!("expected FlowUncompleted, got {other:?}"),
    }
}

#[test]
#[ignore = "requires dexcli dev"]
fn wait_for_method_timeout_reports_timeout_message() {
    let environment = DexDevTestEnvironment::start(Registry::new().register(
        WorkflowUncompletedWaitForTimeoutWorkflow {
            start: WorkflowUncompletedWaitForTimeoutStep,
        },
    ));
    let workflow = WorkflowUncompletedWaitForTimeoutWorkflow {
        start: WorkflowUncompletedWaitForTimeoutStep,
    };
    let flow_id = flow_id("wait-for-timeout");
    let run_id = environment
        .client
        .start_flow(&workflow, &flow_id, ())
        .expect("start waitFor-timeout Flow");
    let failure = wait_for_failure(&environment, &flow_id);
    match failure {
        SdkError::FlowUncompleted {
            run_id: failed_run_id,
            status,
            error_type,
            message,
            result_count,
        } => {
            assert_eq!(run_id, failed_run_id);
            assert_eq!(FlowStatus::Failed, status);
            assert_eq!(Some(FlowErrorType::WorkerApiFailed), error_type);
            assert!(
                message
                    .as_deref()
                    .is_some_and(|message| !message.is_empty())
            );
            assert_eq!(0, result_count);
        }
        other => panic!("expected FlowUncompleted, got {other:?}"),
    }
}

#[test]
#[ignore = "requires dexcli dev"]
fn worker_api_timeout_reports_timeout_message() {
    let environment = DexDevTestEnvironment::start(Registry::new().register(
        WorkflowUncompletedStateTimeoutWorkflow {
            start: WorkflowUncompletedStateTimeoutStep,
        },
    ));
    let workflow = WorkflowUncompletedStateTimeoutWorkflow {
        start: WorkflowUncompletedStateTimeoutStep,
    };
    let flow_id = flow_id("worker-api-timeout");
    let run_id = environment
        .client
        .start_flow(&workflow, &flow_id, 5)
        .expect("start worker-timeout Flow");
    let failure = wait_for_failure(&environment, &flow_id);
    match failure {
        SdkError::FlowUncompleted {
            run_id: failed_run_id,
            status,
            error_type,
            message,
            result_count,
        } => {
            assert_eq!(run_id, failed_run_id);
            assert_eq!(FlowStatus::Failed, status);
            assert_eq!(Some(FlowErrorType::WorkerApiFailed), error_type);
            assert!(
                message
                    .as_deref()
                    .is_some_and(|message| message.to_lowercase().contains("timeout"))
            );
            assert_eq!(0, result_count);
        }
        other => panic!("expected FlowUncompleted, got {other:?}"),
    }
}

#[test]
#[ignore = "requires dexcli dev"]
fn empty_decision_reports_worker_api_failure() {
    let environment = DexDevTestEnvironment::start(Registry::new().register(
        WorkflowUncompletedEmptyDecisionWorkflow {
            start: WorkflowUncompletedEmptyDecisionStep,
        },
    ));
    let workflow = WorkflowUncompletedEmptyDecisionWorkflow {
        start: WorkflowUncompletedEmptyDecisionStep,
    };
    let flow_id = flow_id("empty-decision");
    let run_id = environment
        .client
        .start_flow(&workflow, &flow_id, 5)
        .expect("start empty-decision Flow");
    let failure = wait_for_failure(&environment, &flow_id);
    match failure {
        SdkError::FlowUncompleted {
            run_id: failed_run_id,
            status,
            error_type,
            message,
            result_count,
        } => {
            assert_eq!(run_id, failed_run_id);
            assert_eq!(FlowStatus::Failed, status);
            assert_eq!(Some(FlowErrorType::WorkerApiFailed), error_type);
            assert!(
                message
                    .as_deref()
                    .is_some_and(|message| message.contains("go_to_many requires a movement"))
            );
            assert_eq!(0, result_count);
        }
        other => panic!("expected FlowUncompleted, got {other:?}"),
    }
}

fn assert_stopped_flow(
    options: StopFlowOptions,
    expected_status: FlowStatus,
    expected_error_type: Option<FlowErrorType>,
    expected_message: Option<&str>,
) {
    let environment = DexDevTestEnvironment::start(Registry::new().register(SignalWorkflow::new()));
    let workflow = SignalWorkflow::new();
    let flow_id = flow_id("stopped");
    let run_id = environment
        .client
        .start_flow(&workflow, &flow_id, 1)
        .expect("start stoppable Flow");
    environment
        .client
        .stop_flow(&flow_id, options)
        .expect("stop Flow");
    assert_flow_failure(
        wait_for_failure(&environment, &flow_id),
        &run_id,
        expected_status,
        expected_error_type,
        expected_message,
    );
}

fn wait_for_failure(environment: &DexDevTestEnvironment, flow_id: &str) -> SdkError {
    environment
        .client
        .wait_for_flow_with_timeout::<i32>(flow_id, Duration::from_secs(15))
        .expect_err("Flow must not complete")
}

fn assert_flow_failure(
    failure: SdkError,
    run_id: &str,
    expected_status: FlowStatus,
    expected_error_type: Option<FlowErrorType>,
    expected_message: Option<&str>,
) {
    match failure {
        SdkError::FlowUncompleted {
            run_id: failed_run_id,
            status,
            error_type,
            message,
            result_count,
        } => {
            assert_eq!(run_id, failed_run_id);
            assert_eq!(expected_status, status);
            assert_eq!(expected_error_type, error_type);
            assert_eq!(expected_message, message.as_deref());
            assert_eq!(0, result_count);
        }
        other => panic!("expected FlowUncompleted, got {other:?}"),
    }
}
