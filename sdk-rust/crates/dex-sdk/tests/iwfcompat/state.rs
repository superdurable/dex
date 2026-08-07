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
    Attribute, Client, Context, Flow, HandlerError, HandlerResult, PersistenceSchema, RetryPolicy,
    SdkResult, Step, StepDecision, StepDurability, StepList, StepMovement, StepOptions, Wait,
    WaitForFailurePolicy,
};

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

    fn steps(&self) -> StepList<Self::StartInput> {
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
        let _: String = self.wait_value.get_required(context)?;
        let _: String = self.execute_value.get_required(context)?;
        let _: String = self.both_value.get_required(context)?;
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        let _: String = self.execute_value.get_required(context)?;
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

    fn execute(&self, context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        let _: String = self.both_value.get_required(context)?;
        Ok(StepDecision::graceful_complete("success".to_string()))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .wait_for_lock(self.both_value.lock())
            .execute_lock(self.both_value.lock())
    }
}

struct StateRecoveryWorkflow {
    start: StateRecoveryFailingStep,
    recover: StateRecoveryStep,
}

impl Flow for StateRecoveryWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<Self::StartInput> {
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
        Ok(StepDecision::go_to(&StateRecoveryFailingStep, input * 2))
    }
}

struct StateRecoveryNoWaitWorkflow {
    start: StateRecoveryNoWaitFailingStep,
    recover: StateRecoveryNoWaitStep,
}

impl Flow for StateRecoveryNoWaitWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<Self::StartInput> {
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
        Ok(StepDecision::go_to(
            &StateRecoveryNoWaitFailingStep,
            input * 2,
        ))
    }
}

struct StateOptionsOverrideWorkflow {
    first: StateOptionsOverrideFirstStep,
    second: StateOptionsOverrideSecondStep,
}

impl Flow for StateOptionsOverrideWorkflow {
    type StartInput = String;

    fn steps(&self) -> StepList<Self::StartInput> {
        StepList::start(&self.first).and(&self.second)
    }
}

struct StateOptionsOverrideFirstStep;

impl Step for StateOptionsOverrideFirstStep {
    type Input = String;

    fn execute(&self, _context: &mut Context, input: String) -> HandlerResult<StepDecision> {
        let options: StepOptions<String> = StepOptions::new()
            .wait_for_retry(RetryPolicy::new().maximum_attempts(2))
            .wait_for_failure(WaitForFailurePolicy::Proceed);
        Ok(StepDecision::go_to_many([StepMovement::to_with_options(
            &StateOptionsOverrideSecondStep,
            format!("{input}_state1_start_state1_decide"),
            options,
        )]))
    }
}

struct StateOptionsOverrideSecondStep;

impl Step for StateOptionsOverrideSecondStep {
    type Input = String;

    fn wait_for(&self, _context: &mut Context, _input: String) -> HandlerResult<Wait> {
        Err(HandlerError::new("state 2 wait failure"))
    }

    fn execute(&self, context: &mut Context, input: String) -> HandlerResult<StepDecision> {
        if !context.wait_for_method_failed() {
            return Err(HandlerError::new("waitFor failure was not reported"));
        }
        Ok(StepDecision::graceful_complete(format!(
            "{input}_state2_start_state2_decide"
        )))
    }
}

struct WorkflowUncompletedForceFailWorkflow {
    start: WorkflowUncompletedForceFailStep,
}

impl Flow for WorkflowUncompletedForceFailWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<Self::StartInput> {
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

struct WorkflowUncompletedStateTimeoutStep;

impl Step for WorkflowUncompletedStateTimeoutStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
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

    fn steps(&self) -> StepList<Self::StartInput> {
        StepList::start(&self.start)
    }
}

struct WorkflowUncompletedStateFailureWorkflow {
    start: WorkflowUncompletedStateFailureStep,
}

impl Flow for WorkflowUncompletedStateFailureWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<Self::StartInput> {
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
}

struct WorkflowUncompletedEmptyDecisionWorkflow {
    start: WorkflowUncompletedEmptyDecisionStep,
}

impl Flow for WorkflowUncompletedEmptyDecisionWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<Self::StartInput> {
        StepList::start(&self.start)
    }
}

struct WorkflowUncompletedEmptyDecisionStep;

impl Step for WorkflowUncompletedEmptyDecisionStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, _input: i32) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to_many(std::iter::empty()))
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
    let workflow = StateOptionsOverrideWorkflow {
        first: StateOptionsOverrideFirstStep,
        second: StateOptionsOverrideSecondStep,
    };
    client.start_flow(&workflow, "options-override", "input".into())?;
    let output: String = client.wait_for_flow("options-override")?;
    assert_eq!(
        "input_state1_start_state1_decide_state2_start_state2_decide",
        output
    );
    Ok(())
}
