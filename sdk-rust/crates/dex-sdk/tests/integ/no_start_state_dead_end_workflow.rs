// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use std::sync::LazyLock;

use dex_sdk::{
    Channel, Context, Flow, HandlerError, HandlerResult, PersistenceSchema, Rpc, RpcList,
    RpcResult, Step, StepDecision, StepList, StepMovement,
};

static IDLE_SIGNAL: LazyLock<Channel<()>> = LazyLock::new(|| Channel::new("idle-signal"));
static IDLE_INTERNAL: LazyLock<Channel<()>> = LazyLock::new(|| Channel::new("idle-internal"));

pub(crate) struct NoStartStateDeadEndWorkflow {
    pub(crate) idle_signal: Channel<()>,
    idle_internal: Channel<()>,
    start: DeadEndStep,
    complete: CompleteStep,
}

impl NoStartStateDeadEndWorkflow {
    pub(crate) const SIGNAL_SIZE: Rpc<(), usize> = Rpc::new("signal_size");
    pub(crate) const PUBLISH_INTERNAL: Rpc<(), usize> = Rpc::new("publish_internal");
    pub(crate) const INVOKE: Rpc<String, i64> = Rpc::new("invoke");

    pub(crate) fn new() -> Self {
        Self {
            idle_signal: IDLE_SIGNAL.clone(),
            idle_internal: IDLE_INTERNAL.clone(),
            start: DeadEndStep,
            complete: CompleteStep,
        }
    }

    fn signal_size(&self, context: &mut Context) -> HandlerResult<RpcResult<usize>> {
        Ok(RpcResult::new(self.idle_signal.size(context)?))
    }

    fn publish_internal(&self, context: &mut Context) -> HandlerResult<RpcResult<usize>> {
        self.idle_internal.publish(context, ())?;
        Ok(RpcResult::new(self.idle_internal.size(context)?))
    }

    fn invoke(&self, context: &mut Context, _input: String) -> HandlerResult<RpcResult<i64>> {
        if context.flow_id().is_empty() || context.run_id().is_empty() {
            return Err(HandlerError::new(
                "NoStartStateDeadEndFailure",
                "invalid RPC context",
            ));
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

struct DeadEndStep;

impl Step for DeadEndStep {
    type Input = ();

    fn execute(&self, _context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::dead_end())
    }
}

struct CompleteStep;

impl Step for CompleteStep {
    type Input = ();

    fn execute(&self, _context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(()))
    }
}
