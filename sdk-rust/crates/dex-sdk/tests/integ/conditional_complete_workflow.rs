// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use dex_sdk::{
    Attribute, Channel, Context, Flow, HandlerResult, PersistenceSchema, Rpc, RpcList, Step,
    StepDecision, StepList, StepMovement, Wait,
};

pub(crate) struct ConditionalCompleteWorkflow {
    pub(crate) signal: Channel<()>,
    internal: Channel<()>,
    counter: Attribute<i32>,
    start: ConditionalCompleteStep,
}

impl ConditionalCompleteWorkflow {
    pub(crate) const PUBLISH_TO_INTERNAL: Rpc<i32, ()> = Rpc::new("publish_to_internal_channel");

    pub(crate) fn new() -> Self {
        let signal = Channel::new("test-signal-channel");
        let internal = Channel::new("test-internal-channel");
        let counter = Attribute::new("counter");
        Self {
            start: ConditionalCompleteStep {
                signal: signal.clone(),
                internal: internal.clone(),
                counter: counter.clone(),
            },
            signal,
            internal,
            counter,
        }
    }

    fn publish_to_internal_channel(&self, context: &mut Context, count: i32) -> HandlerResult<()> {
        for _ in 0..count {
            self.internal.publish(context, ())?;
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
            .attribute(&self.counter)
            .channel(&self.signal)
            .channel(&self.internal)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().procedure(Self::PUBLISH_TO_INTERNAL, Self::publish_to_internal_channel)
    }
}

struct ConditionalCompleteStep {
    signal: Channel<()>,
    internal: Channel<()>,
    counter: Attribute<i32>,
}

impl Step for ConditionalCompleteStep {
    type Input = bool;

    fn wait_for(&self, _context: &mut Context, use_signal: bool) -> HandlerResult<Wait> {
        Ok(Wait::until(if use_signal {
            self.signal.for_one()
        } else {
            self.internal.for_one()
        }))
    }

    fn execute(&self, context: &mut Context, use_signal: bool) -> HandlerResult<StepDecision> {
        let next = self.counter.get(context)?.unwrap_or_default() + 1;
        self.counter.set(context, next)?;
        let selected = if use_signal {
            &self.signal
        } else {
            &self.internal
        };
        Ok(StepDecision::force_complete_if_channels_empty(
            next,
            StepMovement::to(self, use_signal),
            [selected.when_empty()],
        ))
    }
}
