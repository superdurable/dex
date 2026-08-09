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
    ActiveStepSearchMode, Client, Context, Flow, FlowConfig, FlowStatus, HandlerError,
    HandlerResult, IdReusePolicy, RetryPolicy, SdkResult, StartFlowOptions, Step, StepDecision,
    StepExecutionId, StepList, StepMovement, StepOptions, Timer, Wait, WaitForFailurePolicy,
    WorkerTarget,
};

struct BasicWorkflow {
    first: BasicFirstStep,
    second: BasicSecondStep,
}

impl BasicWorkflow {
    fn new() -> Self {
        Self {
            first: BasicFirstStep,
            second: BasicSecondStep,
        }
    }
}

impl Flow for BasicWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<Self::StartInput> {
        StepList::start(&self.first).and(&self.second)
    }
}

struct BasicFirstStep;

impl Step for BasicFirstStep {
    type Input = i32;

    fn wait_for(&self, context: &mut Context, input: i32) -> HandlerResult<Wait> {
        context.set_step_execution_local("input", input)?;
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, _context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(&BasicSecondStep, input + 1))
    }
}

struct BasicSecondStep;

impl Step for BasicSecondStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(input + 1))
    }
}

struct BasicAbnormalExitWorkflow {
    start: BasicAbnormalExitStep,
}

impl Flow for BasicAbnormalExitWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<Self::StartInput> {
        StepList::start(&self.start)
    }
}

struct BasicAbnormalExitStep;

impl Step for BasicAbnormalExitStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, _input: i32) -> HandlerResult<StepDecision> {
        Err(HandlerError::new("abnormal exit"))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_retry(RetryPolicy::new().maximum_attempts(1))
    }
}

struct BasicEmptyInputWorkflow {
    first: BasicEmptyFirstStep,
    second: BasicEmptySecondStep,
}

impl Flow for BasicEmptyInputWorkflow {
    type StartInput = ();

    fn flow_type(&self) -> &'static str {
        "test-customized-flow-type"
    }

    fn steps(&self) -> StepList<Self::StartInput> {
        StepList::start(&self.first).and(&self.second)
    }
}

struct BasicEmptyFirstStep;

impl Step for BasicEmptyFirstStep {
    type Input = ();

    fn execute(&self, _context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(&BasicEmptySecondStep, ()))
    }
}

struct BasicEmptySecondStep;

impl Step for BasicEmptySecondStep {
    type Input = ();

    fn execute(&self, _context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(()))
    }
}

#[derive(serde::Deserialize, serde::Serialize)]
struct ModelInput {
    value: i32,
}

struct BasicModelInputWorkflow {
    start: BasicModelInputStep,
}

impl Flow for BasicModelInputWorkflow {
    type StartInput = ModelInput;

    fn steps(&self) -> StepList<Self::StartInput> {
        StepList::start(&self.start)
    }
}

struct BasicModelInputStep;

impl Step for BasicModelInputStep {
    type Input = ModelInput;

    fn execute(&self, _context: &mut Context, input: ModelInput) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(input.value))
    }
}

struct BasicProceedOnWaitFailureWorkflow {
    first: BasicFailingWaitStep,
    second: BasicCompleteStep,
}

impl Flow for BasicProceedOnWaitFailureWorkflow {
    type StartInput = String;

    fn steps(&self) -> StepList<Self::StartInput> {
        StepList::start(&self.first).and(&self.second)
    }
}

struct BasicFailingWaitStep;

impl Step for BasicFailingWaitStep {
    type Input = String;

    fn wait_for(&self, _context: &mut Context, _input: String) -> HandlerResult<Wait> {
        Err(HandlerError::new("wait failure"))
    }

    fn execute(&self, _context: &mut Context, input: String) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(
            &BasicCompleteStep,
            format!("{input}-recovered"),
        ))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .wait_for_failure(WaitForFailurePolicy::Proceed)
            .wait_for_retry(RetryPolicy::new().maximum_attempts(2))
    }
}

struct BasicCompleteStep;

impl Step for BasicCompleteStep {
    type Input = String;

    fn execute(&self, _context: &mut Context, input: String) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(input))
    }
}

struct BasicImmutableStepOptionsWorkflow {
    start: BasicOptionsStartStep,
    failing_wait: BasicOptionsFailingWaitStep,
}

impl Flow for BasicImmutableStepOptionsWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<Self::StartInput> {
        StepList::start(&self.start).and(&self.failing_wait)
    }
}

struct BasicOptionsStartStep;

impl Step for BasicOptionsStartStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, _input: i32) -> HandlerResult<StepDecision> {
        let options: StepOptions<i32> = StepOptions::new()
            .wait_for_retry(RetryPolicy::new().maximum_attempts(1))
            .wait_for_failure(WaitForFailurePolicy::Proceed);
        Ok(StepDecision::go_to_many([StepMovement::to_with_options(
            &BasicOptionsFailingWaitStep,
            1,
            options,
        )]))
    }
}

struct BasicOptionsFailingWaitStep;

impl Step for BasicOptionsFailingWaitStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, input: i32) -> HandlerResult<Wait> {
        Err(HandlerError::new(format!("expected wait failure {input}")))
    }

    fn execute(&self, context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        if !context.wait_for_method_failed() {
            return Err(HandlerError::new("wait failure was not reported"));
        }
        if input == 1 {
            return Ok(StepDecision::go_to(self, 2));
        }
        Ok(StepDecision::graceful_complete(input))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .wait_for_retry(RetryPolicy::new().maximum_attempts(1))
            .wait_for_failure(WaitForFailurePolicy::FailFlow)
    }
}

struct SkipWaitUntilWorkflow {
    first: SkipWaitUntilFirstStep,
    second: SkipWaitUntilSecondStep,
}

impl Flow for SkipWaitUntilWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<Self::StartInput> {
        StepList::start(&self.first).and(&self.second)
    }
}

struct SkipWaitUntilFirstStep;

impl Step for SkipWaitUntilFirstStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(&SkipWaitUntilSecondStep, input + 1))
    }
}

struct SkipWaitUntilSecondStep;

impl Step for SkipWaitUntilSecondStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(input + 1))
    }
}

struct SkipWaitUntilMixedWaitWorkflow {
    first: SkipWaitUntilMixedFirstStep,
    second: SkipWaitUntilMixedSecondStep,
}

impl Flow for SkipWaitUntilMixedWaitWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<Self::StartInput> {
        StepList::start(&self.first).and(&self.second)
    }
}

struct SkipWaitUntilMixedFirstStep;

impl Step for SkipWaitUntilMixedFirstStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(
            &SkipWaitUntilMixedSecondStep,
            input + 1,
        ))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_method_timeout(Duration::from_secs(5))
    }
}

struct SkipWaitUntilMixedSecondStep;

impl Step for SkipWaitUntilMixedSecondStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, _input: i32) -> HandlerResult<Wait> {
        Ok(Wait::until(Timer::by_duration(Duration::from_secs(1))))
    }

    fn execute(&self, _context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(input + 1))
    }
}

fn compile_basic_test(client: &Client) -> SdkResult<()> {
    let workflow = BasicWorkflow::new();
    let options = StartFlowOptions::new()
        .timeout(Duration::from_secs(10))
        .id_reuse_policy(IdReusePolicy::AllowIfNotRunning);
    client.start_flow_with_options(&workflow, "basic", 10, options.clone())?;
    let output: i32 = client.wait_for_flow("basic")?;
    assert_eq!(12, output);

    let abnormal = BasicAbnormalExitWorkflow {
        start: BasicAbnormalExitStep,
    };
    client.start_flow_with_options(&abnormal, "abnormal", 10, options.clone())?;
    let _: SdkResult<i32> = client.wait_for_flow("abnormal");
    client.start_flow_with_options(&workflow, "abnormal", output, options)?;

    let empty = BasicEmptyInputWorkflow {
        first: BasicEmptyFirstStep,
        second: BasicEmptySecondStep,
    };
    client.start_flow(&empty, "empty", ())?;

    let model = BasicModelInputWorkflow {
        start: BasicModelInputStep,
    };
    client.start_flow(&model, "model", ModelInput { value: 10 })?;

    let recovering = BasicProceedOnWaitFailureWorkflow {
        first: BasicFailingWaitStep,
        second: BasicCompleteStep,
    };
    client.start_flow(&recovering, "recover", "input".into())?;
    let recovered: String = client.wait_for_flow("recover")?;
    assert_eq!("input-recovered", recovered);

    let immutable = BasicImmutableStepOptionsWorkflow {
        start: BasicOptionsStartStep,
        failing_wait: BasicOptionsFailingWaitStep,
    };
    client.start_flow(&immutable, "immutable-options", 0)?;
    let _: SdkResult<i32> = client.wait_for_flow("immutable-options");

    let config = FlowConfig::new()
        .active_step_search_mode(ActiveStepSearchMode::All)
        .worker_target(WorkerTarget::new("worker:8803"))
        .continue_as_new_threshold(1);
    client.update_flow_config("basic", config)?;
    let info = client.describe_flow("basic")?;
    assert_eq!(FlowStatus::Completed, info.status);
    client.wait_for_step_completion(
        "basic",
        StepExecutionId::of(&workflow.second),
        Duration::from_secs(5),
    )?;
    Ok(())
}

fn compile_skip_wait_until_test(client: &Client) -> SdkResult<()> {
    let workflow = SkipWaitUntilWorkflow {
        first: SkipWaitUntilFirstStep,
        second: SkipWaitUntilSecondStep,
    };
    client.start_flow(&workflow, "execute-only", 0)?;
    let output: i32 = client.wait_for_flow("execute-only")?;
    assert_eq!(2, output);

    let mixed = SkipWaitUntilMixedWaitWorkflow {
        first: SkipWaitUntilMixedFirstStep,
        second: SkipWaitUntilMixedSecondStep,
    };
    client.start_flow(&mixed, "mixed-wait", 0)?;
    let mixed_output: i32 = client.wait_for_flow("mixed-wait")?;
    assert_eq!(2, mixed_output);
    Ok(())
}
