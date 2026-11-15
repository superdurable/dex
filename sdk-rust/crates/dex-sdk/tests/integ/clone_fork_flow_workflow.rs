// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use std::sync::LazyLock;

use dex_sdk::{
    Attribute, Channel, Context, Flow, HandlerResult, PersistenceSchema, Rpc, RpcList, Step,
    StepDecision, StepList, Wait,
};

pub(crate) static CLONE_FORK_LABEL: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("clone-fork-label"));
pub(crate) static CLONE_FORK_RELEASE: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("clone-fork-release"));

/// Records its start label, then waits for one release answer and completes with both.
pub(crate) struct RustCloneForkSessionWorkflow {
    start: RustCloneForkWaitStep,
}

impl RustCloneForkSessionWorkflow {
    pub(crate) const RELEASE_SESSION: Rpc<String, ()> = Rpc::new("releaseSession");

    pub(crate) fn new() -> Self {
        Self {
            start: RustCloneForkWaitStep,
        }
    }

    fn release_session(&self, context: &mut Context, answer: String) -> HandlerResult<()> {
        CLONE_FORK_RELEASE.publish(context, answer)
    }
}

impl Flow for RustCloneForkSessionWorkflow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&CLONE_FORK_LABEL)
            .channel(&CLONE_FORK_RELEASE)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().procedure(Self::RELEASE_SESSION, Self::release_session)
    }
}

struct RustCloneForkWaitStep;

impl Step for RustCloneForkWaitStep {
    type Input = String;

    fn wait_for(&self, context: &mut Context, label: String) -> HandlerResult<Wait> {
        CLONE_FORK_LABEL.set(context, label)?;
        Ok(Wait::until(CLONE_FORK_RELEASE.for_one()))
    }

    fn execute(&self, context: &mut Context, _label: String) -> HandlerResult<StepDecision> {
        let answers = CLONE_FORK_RELEASE.condition_results(context)?;
        let label = CLONE_FORK_LABEL.get(context)?.unwrap_or_default();
        let answer = answers.into_iter().next().unwrap_or_default();
        Ok(StepDecision::graceful_complete(format!("{label}|{answer}")))
    }
}
