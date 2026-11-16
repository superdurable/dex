// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, LazyLock, Mutex};
use std::time::Duration;

use dex_sdk::{
    Channel, Condition, Context, Flow, HandlerResult, PersistenceSchema, Rpc, RpcList, Step,
    StepDecision, StepList, Wait,
};

use crate::stop_complete_workflow::StopCompleteLatch;

static GRACEFUL_MESSAGES: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("graceful-messages"));

/// One `execute` invocation of the message reader Step.
#[derive(Clone, Debug, Eq, PartialEq)]
pub(crate) struct GracefulReaderObservation {
    pub(crate) messages: Vec<String>,
    pub(crate) is_graceful_completed: bool,
}

/// Shares the test's latches and the reader observations with the registered Flow.
#[derive(Clone, Default)]
pub(crate) struct GracefulConditionProbe {
    pub(crate) wait_for_started: StopCompleteLatch,
    pub(crate) release_wait_for: StopCompleteLatch,
    should_block_wait_for: Arc<AtomicBool>,
    observations: Arc<Mutex<Vec<GracefulReaderObservation>>>,
    support_observations: Arc<Mutex<Vec<bool>>>,
}

impl GracefulConditionProbe {
    pub(crate) fn blocking_first_wait_for() -> Self {
        let probe = Self::default();
        probe.should_block_wait_for.store(true, Ordering::SeqCst);
        probe
    }

    pub(crate) fn observations(&self) -> Vec<GracefulReaderObservation> {
        self.observations.lock().expect("observations lock").clone()
    }

    pub(crate) fn support_observations(&self) -> Vec<bool> {
        self.support_observations
            .lock()
            .expect("support observations lock")
            .clone()
    }
}

pub(crate) struct GracefulConditionWorkflow {
    reader: GracefulMessageReader,
}

impl GracefulConditionWorkflow {
    pub(crate) const PUBLISH_MESSAGE: Rpc<String, ()> = Rpc::new("publish_message");

    pub(crate) fn new(probe: GracefulConditionProbe) -> Self {
        Self {
            reader: GracefulMessageReader { probe },
        }
    }

    fn publish_message(&self, context: &mut Context, message: String) -> HandlerResult<()> {
        GRACEFUL_MESSAGES.publish(context, message)
    }
}

impl Flow for GracefulConditionWorkflow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.reader)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().channel(&GRACEFUL_MESSAGES)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().procedure(Self::PUBLISH_MESSAGE, Self::publish_message)
    }
}

struct GracefulMessageReader {
    probe: GracefulConditionProbe,
}

impl Step for GracefulMessageReader {
    type Input = ();

    fn wait_for(&self, context: &mut Context, _input: ()) -> HandlerResult<Wait> {
        let is_supported = context.supports_graceful_completion_condition();
        self.probe
            .support_observations
            .lock()
            .expect("support observations lock")
            .push(is_supported);
        if self
            .probe
            .should_block_wait_for
            .swap(false, Ordering::SeqCst)
        {
            self.probe.wait_for_started.open();
            self.probe.release_wait_for.wait(Duration::from_secs(20));
        }
        if !is_supported {
            return Ok(Wait::until(GRACEFUL_MESSAGES.for_one()));
        }
        Ok(Wait::any_of([
            GRACEFUL_MESSAGES.for_one(),
            Condition::graceful_completion_requested(),
        ]))
    }

    fn execute(&self, context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        let messages = GRACEFUL_MESSAGES.condition_results(context)?;
        let is_graceful_completed = context.is_graceful_completion_condition_completed();
        self.probe
            .observations
            .lock()
            .expect("observations lock")
            .push(GracefulReaderObservation {
                messages: messages.clone(),
                is_graceful_completed,
            });
        if !messages.is_empty() {
            return Ok(StepDecision::go_to(self, ()));
        }
        if is_graceful_completed {
            return Ok(StepDecision::dead_end());
        }
        Ok(StepDecision::go_to(self, ()))
    }
}
