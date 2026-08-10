// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use std::time::{Duration, SystemTime};

use dex_sdk::{
    ActiveStepSearchMode, Client, Context, ErrorSubStatus, Flow, FlowConfig, FlowErrorType,
    FlowStatus, HandlerError, HandlerResult, IdReusePolicy, Registry, RetryPolicy, SdkError,
    SdkResult, StartFlowOptions, Step, StepDecision, StepExecutionId, StepList, StepMovement,
    StepOptions, StopFlowOptions, Timer, Wait, WaitForFailurePolicy, WorkerTarget,
};

use crate::channels::SignalWorkflow;
use crate::support::{DexDevTestEnvironment, flow_id};

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

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.first).and(&self.second)
    }
}

struct BasicFirstStep;

impl Step for BasicFirstStep {
    type Input = i32;

    fn wait_for(&self, context: &mut Context, input: i32) -> HandlerResult<Wait> {
        validate_attempt_metadata(context)?;
        if input < 0 {
            return Err(HandlerError::new("input must not be negative"));
        }
        context.set_step_execution_local("input", input)?;
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        validate_attempt_metadata(context)?;
        Ok(StepDecision::go_to(&BasicSecondStep, input + 1))
    }
}

fn validate_attempt_metadata(context: &Context) -> HandlerResult<()> {
    if context.attempt() < 1 || context.first_attempt_at() == SystemTime::UNIX_EPOCH {
        return Err(HandlerError::new("invalid first-step attempt metadata"));
    }
    Ok(())
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

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }
}

struct BasicAbnormalExitStep;

impl Step for BasicAbnormalExitStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, _input: i32) -> HandlerResult<StepDecision> {
        Err(HandlerError::new("abnormal exit step"))
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

    fn steps(&self) -> StepList<'_, Self::StartInput> {
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

    fn steps(&self) -> StepList<'_, Self::StartInput> {
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

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.first).and(&self.second)
    }
}

struct BasicFailingWaitStep;

impl Step for BasicFailingWaitStep {
    type Input = String;

    fn wait_for(&self, _context: &mut Context, _input: String) -> HandlerResult<Wait> {
        Err(HandlerError::new("wait failure"))
    }

    fn execute(&self, context: &mut Context, input: String) -> HandlerResult<StepDecision> {
        if !context.wait_for_method_failed() {
            return Err(HandlerError::new("wait_for failure was not reported"));
        }
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

    fn steps(&self) -> StepList<'_, Self::StartInput> {
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

    fn steps(&self) -> StepList<'_, Self::StartInput> {
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

    fn steps(&self) -> StepList<'_, Self::StartInput> {
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

#[test]
#[ignore = "requires dexcli dev"]
fn basic_workflow_runs_and_rejects_duplicate_id() {
    let environment = DexDevTestEnvironment::start(Registry::new().register(BasicWorkflow::new()));
    let workflow = BasicWorkflow::new();
    let flow_id = flow_id("rust-basic");
    let options = StartFlowOptions::new()
        .timeout(Duration::from_secs(30))
        .id_reuse_policy(IdReusePolicy::Disallow)
        .retry_policy(
            RetryPolicy::new()
                .initial_interval(Duration::from_secs(1))
                .maximum_attempts(3)
                .maximum_interval(Duration::from_secs(10))
                .backoff_coefficient(3.0),
        );
    let run_id = environment
        .client
        .start_flow_with_options(&workflow, &flow_id, 1, options.clone())
        .expect("start basic Flow");
    assert!(!run_id.is_empty());
    let output: i32 = environment
        .client
        .wait_for_flow_with_timeout(&flow_id, Duration::from_secs(30))
        .expect("complete basic Flow");
    assert_eq!(3, output);
    let duplicate = environment
        .client
        .start_flow_with_options(&workflow, &flow_id, 1, options)
        .expect_err("duplicate Flow ID must fail");
    assert!(matches!(
        duplicate,
        SdkError::FlowAlreadyStarted { .. }
            | SdkError::Service {
                sub_status: ErrorSubStatus::FlowAlreadyStarted,
                ..
            }
    ));
}

#[test]
#[ignore = "requires dexcli dev"]
fn abnormal_exit_allows_failed_flow_id_reuse() {
    let environment = DexDevTestEnvironment::start(
        Registry::new()
            .register(BasicAbnormalExitWorkflow {
                start: BasicAbnormalExitStep,
            })
            .register(BasicWorkflow::new()),
    );
    let abnormal = BasicAbnormalExitWorkflow {
        start: BasicAbnormalExitStep,
    };
    let workflow = BasicWorkflow::new();
    let flow_id = flow_id("abnormal-exit-reuse");
    let options = StartFlowOptions::new().id_reuse_policy(IdReusePolicy::AllowIfPreviousFailed);
    let failed_run = environment
        .client
        .start_flow_with_options(&abnormal, &flow_id, 0, options.clone())
        .expect("start abnormal-exit Flow");
    let failure = environment
        .client
        .wait_for_flow_with_timeout::<i32>(&flow_id, Duration::from_secs(30))
        .expect_err("abnormal-exit Flow must fail");
    match failure {
        SdkError::FlowUncompleted {
            run_id,
            status,
            error_type,
            message,
            result_count,
        } => {
            assert_eq!(failed_run, run_id);
            assert_eq!(FlowStatus::Failed, status);
            assert_eq!(Some(FlowErrorType::WorkerApiFailed), error_type);
            assert!(
                message
                    .as_deref()
                    .is_some_and(|message| message.contains("abnormal exit step"))
            );
            assert_eq!(0, result_count);
        }
        error => panic!("expected FlowUncompleted, got {error:?}"),
    }
    let new_run = environment
        .client
        .start_flow_with_options(&workflow, &flow_id, 1, options)
        .expect("reuse failed Flow ID");
    assert_ne!(failed_run, new_run);
    assert_eq!(
        3,
        environment
            .client
            .wait_for_flow_with_timeout::<i32>(&flow_id, Duration::from_secs(30))
            .expect("complete reused Flow ID")
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn empty_input_and_missing_flow_are_mapped() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(BasicEmptyInputWorkflow {
            first: BasicEmptyFirstStep,
            second: BasicEmptySecondStep,
        }));
    let workflow = BasicEmptyInputWorkflow {
        first: BasicEmptyFirstStep,
        second: BasicEmptySecondStep,
    };
    let workflow_id = flow_id("empty-input");
    environment
        .client
        .start_flow(&workflow, &workflow_id, ())
        .expect("start empty-input Flow");
    environment
        .client
        .wait_for_flow_with_timeout::<()>(&workflow_id, Duration::from_secs(30))
        .expect("complete empty-input Flow");
    let missing_id = flow_id("missing");
    let missing = environment
        .client
        .wait_for_flow_with_timeout::<()>(&missing_id, Duration::from_secs(1))
        .expect_err("missing Flow must fail");
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
fn custom_flow_type_runs_and_unregistered_type_is_rejected() {
    struct UnregisteredFlow {
        start: BasicEmptyFirstStep,
    }

    impl Flow for UnregisteredFlow {
        type StartInput = ();

        fn flow_type(&self) -> &'static str {
            "test-customized-flow-type"
        }

        fn steps(&self) -> StepList<'_, Self::StartInput> {
            StepList::start(&self.start)
        }
    }

    let environment =
        DexDevTestEnvironment::start(Registry::new().register(BasicEmptyInputWorkflow {
            first: BasicEmptyFirstStep,
            second: BasicEmptySecondStep,
        }));
    let workflow = BasicEmptyInputWorkflow {
        first: BasicEmptyFirstStep,
        second: BasicEmptySecondStep,
    };
    assert_eq!("test-customized-flow-type", workflow.flow_type());
    let workflow_id = flow_id("type-specified");
    environment
        .client
        .start_flow(&workflow, &workflow_id, ())
        .expect("start custom-type Flow");
    environment
        .client
        .wait_for_flow_with_timeout::<()>(&workflow_id, Duration::from_secs(30))
        .expect("complete custom-type Flow");
    let unregistered = UnregisteredFlow {
        start: BasicEmptyFirstStep,
    };
    assert!(matches!(
        environment
            .client
            .start_flow(&unregistered, &flow_id("unregistered"), ())
            .expect_err("unregistered Flow type must fail"),
        SdkError::FlowDefinition { .. }
    ));
}

#[test]
#[ignore = "requires dexcli dev"]
fn model_input_round_trips() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(BasicModelInputWorkflow {
            start: BasicModelInputStep,
        }));
    let workflow = BasicModelInputWorkflow {
        start: BasicModelInputStep,
    };
    let flow_id = flow_id("model-input");
    environment
        .client
        .start_flow(&workflow, &flow_id, ModelInput { value: 10 })
        .expect("start model-input Flow");
    assert_eq!(
        10,
        environment
            .client
            .wait_for_flow_with_timeout::<i32>(&flow_id, Duration::from_secs(30))
            .expect("complete model-input Flow")
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn flow_config_override_runs() {
    let environment = DexDevTestEnvironment::start(Registry::new().register(BasicWorkflow::new()));
    let workflow = BasicWorkflow::new();
    let flow_id = flow_id("config-override");
    let options =
        StartFlowOptions::new().config_override(FlowConfig::new().continue_as_new_threshold(1));
    environment
        .client
        .start_flow_with_options(&workflow, &flow_id, 0, options)
        .expect("start Flow with config override");
    assert_eq!(
        2,
        environment
            .client
            .wait_for_flow_with_timeout::<i32>(&flow_id, Duration::from_secs(30))
            .expect("complete Flow with config override")
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn describe_missing_flow_is_mapped() {
    let environment = DexDevTestEnvironment::start(Registry::new().register(BasicWorkflow::new()));
    let missing = environment
        .client
        .describe_flow(&flow_id("missing"))
        .expect_err("describing a missing Flow must fail");
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
fn describe_running_flow_reports_running() {
    let environment = DexDevTestEnvironment::start(Registry::new().register(SignalWorkflow::new()));
    let workflow = SignalWorkflow::new();
    let flow_id = flow_id("running");
    environment
        .client
        .start_flow(&workflow, &flow_id, 0)
        .expect("start waiting Flow");
    assert_eq!(
        FlowStatus::Running,
        environment
            .client
            .describe_flow(&flow_id)
            .expect("describe running Flow")
            .status
    );
    environment
        .client
        .stop_flow(&flow_id, StopFlowOptions::cancel())
        .expect("cancel waiting Flow");
}

#[test]
#[ignore = "requires dexcli dev"]
fn wait_for_step_completion_observes_second_step() {
    let environment = DexDevTestEnvironment::start(Registry::new().register(BasicWorkflow::new()));
    let workflow = BasicWorkflow::new();
    let flow_id = flow_id("wait-step");
    environment
        .client
        .start_flow(&workflow, &flow_id, 5)
        .expect("start basic Flow");
    environment
        .client
        .wait_for_step_completion(
            &flow_id,
            StepExecutionId::of(&workflow.second),
            Duration::from_secs(30),
        )
        .expect("wait for second Step");
    assert_eq!(
        7,
        environment
            .client
            .wait_for_flow_with_timeout::<i32>(&flow_id, Duration::from_secs(30))
            .expect("complete basic Flow")
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn wait_failure_proceeds_to_execute() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(BasicProceedOnWaitFailureWorkflow {
            first: BasicFailingWaitStep,
            second: BasicCompleteStep,
        }));
    let workflow = BasicProceedOnWaitFailureWorkflow {
        first: BasicFailingWaitStep,
        second: BasicCompleteStep,
    };
    let flow_id = flow_id("proceed-on-wait-failure");
    environment
        .client
        .start_flow(&workflow, &flow_id, "input".to_string())
        .expect("start wait-failure Flow");
    assert_eq!(
        "input-recovered",
        environment
            .client
            .wait_for_flow_with_timeout::<String>(&flow_id, Duration::from_secs(30))
            .expect("recover from wait failure")
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn mixed_wait_styles_complete() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(SkipWaitUntilMixedWaitWorkflow {
            first: SkipWaitUntilMixedFirstStep,
            second: SkipWaitUntilMixedSecondStep,
        }));
    let workflow = SkipWaitUntilMixedWaitWorkflow {
        first: SkipWaitUntilMixedFirstStep,
        second: SkipWaitUntilMixedSecondStep,
    };
    let flow_id = flow_id("mixed-wait");
    environment
        .client
        .start_flow(&workflow, &flow_id, 0)
        .expect("start mixed-wait Flow");
    assert_eq!(
        2,
        environment
            .client
            .wait_for_flow_with_timeout::<i32>(&flow_id, Duration::from_secs(30))
            .expect("complete mixed-wait Flow")
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn movement_options_do_not_mutate_step_defaults() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(BasicImmutableStepOptionsWorkflow {
            start: BasicOptionsStartStep,
            failing_wait: BasicOptionsFailingWaitStep,
        }));
    let workflow = BasicImmutableStepOptionsWorkflow {
        start: BasicOptionsStartStep,
        failing_wait: BasicOptionsFailingWaitStep,
    };
    let flow_id = flow_id("immutable-options");
    environment
        .client
        .start_flow(&workflow, &flow_id, 0)
        .expect("start immutable-options Flow");
    let failure = environment
        .client
        .wait_for_flow_with_timeout::<i32>(&flow_id, Duration::from_secs(30))
        .expect_err("second wait failure must fail the Flow");
    match failure {
        SdkError::FlowUncompleted {
            status,
            error_type,
            message,
            ..
        } => {
            assert_eq!(FlowStatus::Failed, status);
            assert_eq!(Some(FlowErrorType::WorkerApiFailed), error_type);
            assert_eq!(Some("expected wait failure 2".to_string()), message);
        }
        error => panic!("expected FlowUncompleted, got {error:?}"),
    }
}

#[test]
#[ignore = "requires dexcli dev"]
fn execute_only_steps_skip_wait_for() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(SkipWaitUntilWorkflow {
            first: SkipWaitUntilFirstStep,
            second: SkipWaitUntilSecondStep,
        }));
    let workflow = SkipWaitUntilWorkflow {
        first: SkipWaitUntilFirstStep,
        second: SkipWaitUntilSecondStep,
    };
    let flow_id = flow_id("skip-wait-until");
    let options =
        StartFlowOptions::new().config_override(FlowConfig::new().continue_as_new_threshold(1));
    environment
        .client
        .start_flow_with_options(&workflow, &flow_id, 0, options)
        .expect("start execute-only Flow");
    assert_eq!(
        2,
        environment
            .client
            .wait_for_flow_with_timeout::<i32>(&flow_id, Duration::from_secs(30))
            .expect("complete execute-only Flow")
    );
}
