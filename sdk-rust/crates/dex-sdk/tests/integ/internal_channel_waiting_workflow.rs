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
    Channel, Context, Flow, HandlerResult, PersistenceSchema, Step, StepDecision, StepList, Wait,
};

static CHANNEL: LazyLock<Channel<i32>> = LazyLock::new(|| Channel::new("waiting-channel"));

pub(crate) struct InternalChannelWaitingWorkflow {
    pub(crate) channel: Channel<i32>,
    start: WaitingStep,
}

impl InternalChannelWaitingWorkflow {
    pub(crate) fn new() -> Self {
        let channel = CHANNEL.clone();
        Self {
            start: WaitingStep {
                channel: channel.clone(),
            },
            channel,
        }
    }
}

impl Flow for InternalChannelWaitingWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().channel(&self.channel)
    }
}

struct WaitingStep {
    channel: Channel<i32>,
}

impl Step for WaitingStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, _input: i32) -> HandlerResult<Wait> {
        Ok(Wait::until(self.channel.for_n(2)))
    }

    fn execute(&self, context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        let output = self
            .channel
            .condition_results(context)?
            .into_iter()
            .fold(input, i32::saturating_add);
        Ok(StepDecision::graceful_complete(output))
    }
}
