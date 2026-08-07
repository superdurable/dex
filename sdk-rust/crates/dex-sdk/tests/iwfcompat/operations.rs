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
    Channel, Client, Context, Flow, HandlerResult, PersistenceSchema, ResetFlowOptions, Rpc,
    RpcList, RpcResult, SdkResult, StartFlowOptions, Step, StepDecision, StepExecutionId, StepList,
    StepMovement, StopFlowOptions, StopType,
};

struct NoStartStateWorkflow {
    triggered: NoStartTriggeredStep,
}

impl NoStartStateWorkflow {
    const INVOKE: Rpc<String, i64> = Rpc::new("invoke");

    fn invoke(&self, _context: &mut Context, _input: String) -> HandlerResult<RpcResult<i64>> {
        Ok(RpcResult::new(1).then(StepMovement::to(&self.triggered, ())))
    }
}

impl Flow for NoStartStateWorkflow {
    type StartInput = ();

    fn steps(&self) -> StepList<Self::StartInput> {
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

    fn steps(&self) -> StepList<Self::StartInput> {
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
    first: ResetFirstStep,
    second: ResetSecondStep,
}

impl Flow for ResetWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<Self::StartInput> {
        StepList::start(&self.first).and(&self.second)
    }
}

struct ResetFirstStep;

impl Step for ResetFirstStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(&ResetSecondStep, input + 1))
    }
}

struct ResetSecondStep;

impl Step for ResetSecondStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(input + 1))
    }
}

fn compile_no_start_state_test(client: &Client) -> SdkResult<()> {
    let _workflow = NoStartStateWorkflow {
        triggered: NoStartTriggeredStep,
    };
    let output: i64 =
        client.invoke_rpc("no-start", NoStartStateWorkflow::INVOKE, "input".into())?;
    assert_eq!(1, output);
    let output: i32 = client.wait_for_flow("no-start")?;
    assert_eq!(1, output);
    Ok(())
}

fn compile_reset_test(client: &Client) -> SdkResult<()> {
    let workflow = ResetWorkflow {
        first: ResetFirstStep,
        second: ResetSecondStep,
    };
    client.start_flow_with_options(
        &workflow,
        "reset",
        0,
        StartFlowOptions::new().timeout(Duration::from_secs(30)),
    )?;
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
    client.stop_flow(
        "stopped",
        StopFlowOptions::new(StopType::Terminate).reason("terminated"),
    )?;
    client.trigger_continue_as_new("stopped")?;
    Ok(())
}
