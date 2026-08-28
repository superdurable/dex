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
    Attribute, Channel, Context, Flow, HandlerResult, PersistenceSchema, Rpc, RpcList, Step,
    StepDecision, StepList, StepMovement, Wait,
};

pub(crate) static SIGNAL: LazyLock<Channel<()>> =
    LazyLock::new(|| Channel::new("test-signal-channel"));
static INTERNAL: LazyLock<Channel<()>> = LazyLock::new(|| Channel::new("test-internal-channel"));
static COUNTER: LazyLock<Attribute<i32>> = LazyLock::new(|| Attribute::new("counter"));

pub(crate) struct ConditionalCompleteWorkflow {
    start: ConditionalCompleteStep,
}

impl ConditionalCompleteWorkflow {
    pub(crate) const PUBLISH_TO_INTERNAL: Rpc<i32, ()> = Rpc::new("publish_to_internal_channel");

    pub(crate) fn new() -> Self {
        Self {
            start: ConditionalCompleteStep,
        }
    }

    fn publish_to_internal_channel(&self, context: &mut Context, count: i32) -> HandlerResult<()> {
        for _ in 0..count {
            INTERNAL.publish(context, ())?;
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
            .attribute(&COUNTER)
            .channel(&SIGNAL)
            .channel(&INTERNAL)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().procedure(Self::PUBLISH_TO_INTERNAL, Self::publish_to_internal_channel)
    }
}

struct ConditionalCompleteStep;

impl Step for ConditionalCompleteStep {
    type Input = bool;

    fn wait_for(&self, _context: &mut Context, use_signal: bool) -> HandlerResult<Wait> {
        Ok(Wait::until(if use_signal {
            SIGNAL.for_one()
        } else {
            INTERNAL.for_one()
        }))
    }

    fn execute(&self, context: &mut Context, use_signal: bool) -> HandlerResult<StepDecision> {
        let next = COUNTER.get(context)?.unwrap_or_default() + 1;
        COUNTER.set(context, next)?;
        let selected = if use_signal { &*SIGNAL } else { &*INTERNAL };
        Ok(StepDecision::force_complete_if_channels_empty(
            next,
            StepMovement::to(self, use_signal),
            [selected.when_empty()],
        ))
    }
}
