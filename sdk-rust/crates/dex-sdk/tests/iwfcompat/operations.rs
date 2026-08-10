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
    Attribute, AttributeIndex, AttributeMap, Channel, Client, Context, Flow, FlowStatus, GrpcCode,
    HandlerResult, PersistenceSchema, Registry, ResetFlowOptions, Rpc, RpcList, RpcResult,
    SdkError, SdkResult, StartFlowOptions, Step, StepDecision, StepExecutionId, StepList,
    StepMovement, StepOptions, StopFlowOptions,
};

use crate::support::{DexDevTestEnvironment, flow_id};

struct NoStartStateWorkflow {
    triggered: NoStartTriggeredStep,
}

impl NoStartStateWorkflow {
    const RPC_OUTPUT: i64 = 100;
    const INVOKE: Rpc<String, i64> = Rpc::new("invoke");

    fn invoke(&self, context: &mut Context, _input: String) -> HandlerResult<RpcResult<i64>> {
        if context.flow_id().is_empty() || context.run_id().is_empty() {
            return Err(dex_sdk::HandlerError::new("invalid RPC context"));
        }
        Ok(RpcResult::new(Self::RPC_OUTPUT).then(StepMovement::to(&self.triggered, ())))
    }
}

impl Flow for NoStartStateWorkflow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::empty().and(&self.triggered)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().function(Self::INVOKE, Self::invoke)
    }
}

struct NoStartTriggeredStep;

impl Step for NoStartTriggeredStep {
    type Input = ();

    fn execute(&self, _context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(1_i32))
    }
}

struct GoNoStartWorkflow {
    finish: GoNoStartFinishStep,
}

impl GoNoStartWorkflow {
    const START: Rpc<i32, i32> = Rpc::new("start");

    fn start(&self, _context: &mut Context, _input: i32) -> HandlerResult<RpcResult<i32>> {
        Ok(RpcResult::new(2).then(StepMovement::to(&self.finish, 2)))
    }
}

impl Flow for GoNoStartWorkflow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::empty().and(&self.finish)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().function(Self::START, Self::start)
    }
}

struct GoNoStartFinishStep;

impl Step for GoNoStartFinishStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(input + 1))
    }
}

struct NoStartStateDeadEndWorkflow {
    idle_signal: Channel<()>,
    idle_internal: Channel<()>,
    start: NoStartStateDeadEndStep,
    complete: NoStartStateCompleteStep,
}

impl NoStartStateDeadEndWorkflow {
    const SIGNAL_SIZE: Rpc<(), usize> = Rpc::new("signal_size");
    const PUBLISH_INTERNAL: Rpc<(), usize> = Rpc::new("publish_internal");
    const INVOKE: Rpc<String, i64> = Rpc::new("invoke");

    fn signal_size(&self, context: &mut Context) -> HandlerResult<RpcResult<usize>> {
        Ok(RpcResult::new(self.idle_signal.size(context)?))
    }

    fn publish_internal(&self, context: &mut Context) -> HandlerResult<RpcResult<usize>> {
        self.idle_internal.publish(context, ())?;
        Ok(RpcResult::new(self.idle_internal.size(context)?))
    }

    fn invoke(&self, context: &mut Context, _input: String) -> HandlerResult<RpcResult<i64>> {
        if context.flow_id().is_empty() || context.run_id().is_empty() {
            return Err(dex_sdk::HandlerError::new("invalid RPC context"));
        }
        Ok(RpcResult::new(100).then(StepMovement::to(&self.complete, ())))
    }
}

impl Flow for NoStartStateDeadEndWorkflow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start).and(&self.complete)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .channel(&self.idle_signal)
            .channel(&self.idle_internal)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function_without_input(Self::SIGNAL_SIZE, Self::signal_size)
            .function_without_input(Self::PUBLISH_INTERNAL, Self::publish_internal)
            .function(Self::INVOKE, Self::invoke)
    }
}

struct NoStartStateDeadEndStep;

impl Step for NoStartStateDeadEndStep {
    type Input = ();

    fn execute(&self, _context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::dead_end())
    }
}

struct NoStartStateCompleteStep;

impl Step for NoStartStateCompleteStep {
    type Input = ();

    fn execute(&self, _context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(()))
    }
}

struct ResetWorkflow {
    channel: Channel<()>,
    data: Attribute<String>,
    keyword: Attribute<String>,
    counter: Attribute<i32>,
    items: AttributeMap<String>,
    execution_count: Attribute<i32>,
    first: ResetLockWaitStep,
    second: ResetLockCompleteStep,
}

impl ResetWorkflow {
    const EXPECTED_VALUE: &str = "random-string";
    const WITH_LOCKING: Rpc<(), ()> = Rpc::new("with_locking");
    const WITH_ATTRIBUTE_MAP_LOCK: Rpc<(), ()> = Rpc::new("with_attribute_map_lock");
    const WITHOUT_LOCKING: Rpc<(), ()> = Rpc::new("without_locking");

    fn new() -> Self {
        let channel = Channel::new("rpc-channel");
        let data = Attribute::new("rpc-lock-data");
        let keyword = Attribute::new("CustomKeywordField").indexed(AttributeIndex::keyword());
        let counter = Attribute::new("CustomIntField").indexed(AttributeIndex::int());
        let items = AttributeMap::new("rpc-lock-items");
        let execution_count = Attribute::new("reset-execution-count");
        Self {
            first: ResetLockWaitStep {
                channel: channel.clone(),
            },
            second: ResetLockCompleteStep {
                execution_count: execution_count.clone(),
            },
            channel,
            data,
            keyword,
            counter,
            items,
            execution_count,
        }
    }

    fn with_locking(&self, context: &mut Context) -> HandlerResult<RpcResult<()>> {
        self.write_attributes(context)?;
        self.channel.publish(context, ())?;
        Ok(RpcResult::new(()).then(StepMovement::to(&self.second, ())))
    }

    fn with_attribute_map_lock(&self, context: &mut Context) -> HandlerResult<()> {
        self.items.set(context, "order-1", "locked".to_string())
    }

    fn without_locking(&self, context: &mut Context) -> HandlerResult<RpcResult<()>> {
        self.write_attributes(context)?;
        self.channel.publish(context, ())?;
        Ok(RpcResult::new(()).then(StepMovement::to(&self.second, ())))
    }

    fn write_attributes(&self, context: &mut Context) -> HandlerResult<()> {
        self.data.set(context, Self::EXPECTED_VALUE.to_string())?;
        self.keyword
            .set(context, Self::EXPECTED_VALUE.to_string())?;
        self.counter.set(context, 100)
    }
}

impl Flow for ResetWorkflow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.first).and(&self.second)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&self.data)
            .attribute(&self.keyword)
            .attribute(&self.counter)
            .attribute_map(&self.items)
            .attribute(&self.execution_count)
            .channel(&self.channel)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function_without_input(
                Self::WITH_LOCKING
                    .lock(self.data.lock())
                    .lock(self.keyword.lock())
                    .lock(self.counter.lock()),
                Self::with_locking,
            )
            .procedure_without_input(
                Self::WITH_ATTRIBUTE_MAP_LOCK.lock(self.items.lock("order-1")),
                Self::with_attribute_map_lock,
            )
            .function_without_input(Self::WITHOUT_LOCKING, Self::without_locking)
    }
}

struct ResetLockWaitStep {
    channel: Channel<()>,
}

impl Step for ResetLockWaitStep {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, (): ()) -> HandlerResult<dex_sdk::Wait> {
        Ok(dex_sdk::Wait::until(self.channel.for_one()))
    }

    fn execute(&self, _context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(
            &ResetLockCompleteStep {
                execution_count: Attribute::new("reset-execution-count"),
            },
            (),
        ))
    }
}

struct ResetLockCompleteStep {
    execution_count: Attribute<i32>,
}

impl Step for ResetLockCompleteStep {
    type Input = ();

    fn execute(&self, context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        let next = self.execution_count.get(context)?.unwrap_or_default() + 1;
        self.execution_count.set(context, next)?;
        Ok(StepDecision::graceful_complete(next))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_lock(self.execution_count.lock())
    }
}

fn compile_no_start_state_test(client: &Client) -> SdkResult<()> {
    let _workflow = NoStartStateWorkflow {
        triggered: NoStartTriggeredStep,
    };
    let output: i64 =
        client.invoke_rpc("no-start", NoStartStateWorkflow::INVOKE, "input".into())?;
    assert_eq!(NoStartStateWorkflow::RPC_OUTPUT, output);
    let output: i32 = client.wait_for_flow("no-start")?;
    assert_eq!(1, output);
    Ok(())
}

fn compile_reset_test(client: &Client) -> SdkResult<()> {
    let workflow = ResetWorkflow::new();
    client.start_flow_with_options(
        &workflow,
        "reset",
        (),
        StartFlowOptions::new().timeout(Duration::from_secs(30)),
    )?;
    client.invoke_rpc_without_input("reset", ResetWorkflow::WITH_LOCKING)?;
    client.invoke_rpc_without_input("reset", ResetWorkflow::WITH_ATTRIBUTE_MAP_LOCK)?;
    client.reset_flow("reset", ResetFlowOptions::from_beginning())?;
    client.reset_flow("reset", ResetFlowOptions::from_history_event_id(1))?;
    client.reset_flow(
        "reset",
        ResetFlowOptions::from_history_event_time(SystemTime::UNIX_EPOCH),
    )?;
    client.reset_flow("reset", ResetFlowOptions::from_step(&workflow.second))?;
    client.reset_flow(
        "reset",
        ResetFlowOptions::from_step_execution(
            StepExecutionId::of(&workflow.second).execution_number(1),
        ),
    )?;
    Ok(())
}

fn compile_workflow_uncompleted_test(client: &Client) -> SdkResult<()> {
    let workflow = NoStartStateDeadEndWorkflow {
        idle_signal: Channel::new("idle-signal"),
        idle_internal: Channel::new("idle-internal"),
        start: NoStartStateDeadEndStep,
        complete: NoStartStateCompleteStep,
    };
    client.start_flow(&workflow, "stopped", ())?;
    client.publish_many("stopped", &workflow.idle_signal, [(), (), ()])?;
    let size: usize =
        client.invoke_rpc_without_input("stopped", NoStartStateDeadEndWorkflow::SIGNAL_SIZE)?;
    assert_eq!(3, size);
    let published: usize = client
        .invoke_rpc_without_input("stopped", NoStartStateDeadEndWorkflow::PUBLISH_INTERNAL)?;
    assert_eq!(1, published);
    let _: i64 = client.invoke_rpc(
        "stopped",
        NoStartStateDeadEndWorkflow::INVOKE,
        "input".into(),
    )?;
    client.stop_flow("stopped", StopFlowOptions::terminate().reason("terminated"))?;
    client.trigger_continue_as_new("stopped")?;
    Ok(())
}

fn compile_domain_error_handling(error: SdkError) -> bool {
    match error {
        SdkError::FlowAlreadyStarted { .. } => false,
        SdkError::FlowNotFound { .. } | SdkError::FlowNotActive { .. } => false,
        SdkError::RpcLockConflict { .. } => true,
        SdkError::WorkerInvocation {
            worker_code: Some(GrpcCode::Unavailable),
            ..
        } => true,
        SdkError::LongPollTimeout { .. } => true,
        _ => false,
    }
}

#[test]
#[ignore = "requires dexcli dev"]
fn no_start_step_flow_starts_from_rpc_movement() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(NoStartStateWorkflow {
            triggered: NoStartTriggeredStep,
        }));
    let workflow = NoStartStateWorkflow {
        triggered: NoStartTriggeredStep,
    };
    let flow_id = flow_id("no-start");
    environment
        .client
        .start_flow(&workflow, &flow_id, ())
        .expect("start Flow without a start Step");
    assert_eq!(
        NoStartStateWorkflow::RPC_OUTPUT,
        environment
            .client
            .invoke_rpc(
                &flow_id,
                NoStartStateWorkflow::INVOKE,
                "rpc-input".to_string()
            )
            .expect("invoke no-start RPC")
    );
    assert_eq!(
        1,
        environment
            .client
            .wait_for_flow_with_timeout::<i32>(&flow_id, Duration::from_secs(30))
            .expect("complete no-start Flow")
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn go_no_start_step_contract_starts_from_typed_rpc_movement() {
    let environment = DexDevTestEnvironment::start(Registry::new().register(GoNoStartWorkflow {
        finish: GoNoStartFinishStep,
    }));
    let workflow = GoNoStartWorkflow {
        finish: GoNoStartFinishStep,
    };
    let flow_id = flow_id("go-no-start");
    environment
        .client
        .start_flow(&workflow, &flow_id, ())
        .expect("start Go no-start-Step Flow");
    assert_eq!(
        2,
        environment
            .client
            .invoke_rpc(&flow_id, GoNoStartWorkflow::START, 1)
            .expect("invoke Go no-start-Step RPC")
    );
    assert_eq!(
        3,
        environment
            .client
            .wait_for_flow_with_timeout::<i32>(&flow_id, Duration::from_secs(30))
            .expect("complete Go no-start-Step Flow")
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn dead_end_flow_resumes_from_rpc_movement() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(NoStartStateDeadEndWorkflow {
            idle_signal: Channel::new("idle-signal"),
            idle_internal: Channel::new("idle-internal"),
            start: NoStartStateDeadEndStep,
            complete: NoStartStateCompleteStep,
        }));
    let workflow = NoStartStateDeadEndWorkflow {
        idle_signal: Channel::new("idle-signal"),
        idle_internal: Channel::new("idle-internal"),
        start: NoStartStateDeadEndStep,
        complete: NoStartStateCompleteStep,
    };
    let flow_id = flow_id("dead-end");
    environment
        .client
        .start_flow(&workflow, &flow_id, ())
        .expect("start dead-end Flow");
    assert_eq!(
        100,
        environment
            .client
            .invoke_rpc(
                &flow_id,
                NoStartStateDeadEndWorkflow::INVOKE,
                "rpc-input".to_string(),
            )
            .expect("resume dead-end Flow")
    );
    environment
        .client
        .wait_for_flow_with_timeout::<()>(&flow_id, Duration::from_secs(30))
        .expect("complete resumed dead-end Flow");
}

#[test]
#[ignore = "requires dexcli dev"]
fn rpc_observes_external_and_internal_channel_sizes() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(NoStartStateDeadEndWorkflow {
            idle_signal: Channel::new("idle-signal"),
            idle_internal: Channel::new("idle-internal"),
            start: NoStartStateDeadEndStep,
            complete: NoStartStateCompleteStep,
        }));
    let workflow = NoStartStateDeadEndWorkflow {
        idle_signal: Channel::new("idle-signal"),
        idle_internal: Channel::new("idle-internal"),
        start: NoStartStateDeadEndStep,
        complete: NoStartStateCompleteStep,
    };
    let flow_id = flow_id("channel-size");
    environment
        .client
        .start_flow(&workflow, &flow_id, ())
        .expect("start channel-size Flow");
    environment
        .client
        .invoke_rpc_without_input::<usize>(&flow_id, NoStartStateDeadEndWorkflow::PUBLISH_INTERNAL)
        .expect("publish first internal message");
    assert_eq!(
        2,
        environment
            .client
            .invoke_rpc_without_input::<usize>(
                &flow_id,
                NoStartStateDeadEndWorkflow::PUBLISH_INTERNAL,
            )
            .expect("publish second internal message")
    );
    environment
        .client
        .publish_many(&flow_id, &workflow.idle_signal, [(), (), ()])
        .expect("publish external messages");
    assert_eq!(
        3,
        environment
            .client
            .invoke_rpc_without_input::<usize>(&flow_id, NoStartStateDeadEndWorkflow::SIGNAL_SIZE,)
            .expect("read signal channel size")
    );
    environment
        .client
        .stop_flow(&flow_id, StopFlowOptions::cancel())
        .expect("stop channel-size Flow");
}

#[test]
#[ignore = "requires dexcli dev"]
fn reset_reapplies_locking_rpc() {
    run_reset_scenario(true, false, false);
}

#[test]
#[ignore = "requires dexcli dev"]
fn reset_can_skip_locking_rpc_reapply() {
    run_reset_scenario(true, true, true);
}

#[test]
#[ignore = "requires dexcli dev"]
fn reset_reapplies_nonlocking_rpc_channel_message() {
    run_reset_scenario(false, false, false);
}

#[test]
#[ignore = "requires dexcli dev"]
fn reset_can_skip_nonlocking_rpc_channel_reapply() {
    run_reset_scenario(false, true, true);
}

fn run_reset_scenario(locking: bool, skip_locking_rpc: bool, skip_channels: bool) {
    let environment = DexDevTestEnvironment::start(Registry::new().register(ResetWorkflow::new()));
    let workflow = ResetWorkflow::new();
    let flow_id = flow_id("reset");
    environment
        .client
        .start_flow_with_options(
            &workflow,
            &flow_id,
            (),
            StartFlowOptions::new().timeout(Duration::from_secs(3)),
        )
        .expect("start reset Flow");
    if locking {
        environment
            .client
            .invoke_rpc_without_input(&flow_id, ResetWorkflow::WITH_ATTRIBUTE_MAP_LOCK)
            .expect("invoke AttributeMap-locking RPC");
        environment
            .client
            .invoke_rpc_without_input(&flow_id, ResetWorkflow::WITH_LOCKING)
            .expect("invoke locking RPC");
    } else {
        environment
            .client
            .invoke_rpc_without_input(&flow_id, ResetWorkflow::WITHOUT_LOCKING)
            .expect("invoke non-locking RPC");
    }
    assert_reset_completed(&environment, &workflow, &flow_id, locking);
    let reset_run_id = environment
        .client
        .reset_flow(
            &flow_id,
            ResetFlowOptions::from_beginning()
                .reason("testing reset")
                .skip_locking_rpc_reapply(skip_locking_rpc)
                .skip_channel_messages_reapply(skip_channels),
        )
        .expect("reset Flow");
    if skip_locking_rpc && skip_channels {
        assert_reset_times_out(&environment, &workflow, &flow_id, &reset_run_id);
    } else {
        assert_reset_completed(&environment, &workflow, &flow_id, locking);
        assert_eq!(
            reset_run_id,
            environment
                .client
                .describe_flow(&flow_id)
                .expect("describe reset Flow")
                .run_id
        );
    }
}

fn assert_reset_completed(
    environment: &DexDevTestEnvironment,
    workflow: &ResetWorkflow,
    flow_id: &str,
    expects_attribute_map_value: bool,
) {
    assert_eq!(
        2,
        environment
            .client
            .wait_for_flow_with_timeout::<i32>(flow_id, Duration::from_secs(10))
            .expect("complete reset Flow")
    );
    assert_eq!(
        FlowStatus::Completed,
        environment
            .client
            .describe_flow(flow_id)
            .expect("describe completed reset Flow")
            .status
    );
    assert_eq!(
        Some(ResetWorkflow::EXPECTED_VALUE.to_string()),
        environment
            .client
            .get_attribute(flow_id, &workflow.data)
            .expect("get reset data Attribute")
    );
    assert_eq!(
        Some(ResetWorkflow::EXPECTED_VALUE.to_string()),
        environment
            .client
            .get_attribute(flow_id, &workflow.keyword)
            .expect("get reset keyword Attribute")
    );
    assert_eq!(
        Some(100),
        environment
            .client
            .get_attribute(flow_id, &workflow.counter)
            .expect("get reset counter Attribute")
    );
    assert_eq!(
        Some(2),
        environment
            .client
            .get_attribute(flow_id, &workflow.execution_count)
            .expect("get reset execution-count Attribute")
    );
    let item = environment
        .client
        .get_attribute_map(flow_id, &workflow.items, "order-1")
        .expect("get reset AttributeMap entry");
    if expects_attribute_map_value {
        assert_eq!(Some("locked".to_string()), item);
    } else {
        assert_eq!(None, item);
    }
}

fn assert_reset_times_out(
    environment: &DexDevTestEnvironment,
    workflow: &ResetWorkflow,
    flow_id: &str,
    reset_run_id: &str,
) {
    let failure = environment
        .client
        .wait_for_flow_with_timeout::<i32>(flow_id, Duration::from_secs(10))
        .expect_err("reset Flow without reapplied input must time out");
    match failure {
        SdkError::FlowUncompleted {
            run_id,
            status,
            result_count,
            ..
        } => {
            assert_eq!(reset_run_id, run_id);
            assert_eq!(FlowStatus::TimedOut, status);
            assert_eq!(0, result_count);
        }
        error => panic!("expected FlowUncompleted, got {error:?}"),
    }
    assert_eq!(
        None,
        environment
            .client
            .get_attribute(flow_id, &workflow.data)
            .expect("get cleared reset data Attribute")
    );
    assert_eq!(
        None,
        environment
            .client
            .get_attribute(flow_id, &workflow.keyword)
            .expect("get cleared reset keyword Attribute")
    );
    assert_eq!(
        None,
        environment
            .client
            .get_attribute(flow_id, &workflow.counter)
            .expect("get cleared reset counter Attribute")
    );
    assert_eq!(
        None,
        environment
            .client
            .get_attribute(flow_id, &workflow.execution_count)
            .expect("get cleared reset execution-count Attribute")
    );
    assert_eq!(
        None,
        environment
            .client
            .get_attribute_map(flow_id, &workflow.items, "order-1")
            .expect("get cleared reset AttributeMap entry")
    );
}
