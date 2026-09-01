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
use std::time::Duration;

use dex_sdk::{
    BufferedTextStreamOptions, Context, Flow, HandlerResult, PersistenceSchema, Step, StepDecision,
    StepList, Stream, Wait,
};

pub(crate) static PROGRESS: LazyLock<Stream<String>> =
    LazyLock::new(|| Stream::new("stream-test-progress", 1 << 20));

#[derive(Clone)]
pub(crate) struct StreamTestWorkflow {
    start: StreamTestStep,
}

impl StreamTestWorkflow {
    pub(crate) fn new() -> Self {
        Self {
            start: StreamTestStep,
        }
    }
}

impl Flow for StreamTestWorkflow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().stream(&PROGRESS)
    }
}

#[derive(Clone)]
struct StreamTestStep;

impl Step for StreamTestStep {
    type Input = ();

    fn wait_for(&self, context: &mut Context, _input: ()) -> HandlerResult<Wait> {
        context.record_heartbeat()?;
        let progress = PROGRESS.buffered_text(context)?;
        progress.write("wait-progress-")?;
        progress.write("1")?;
        progress.flush()?;
        context.record_heartbeat_value("wait-checkpoint".to_string())?;
        progress.write("wait-progress-")?;
        progress.write("2")?;
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        context.record_heartbeat()?;
        let progress = PROGRESS.buffered_text_with_options(
            context,
            BufferedTextStreamOptions::new(Duration::from_millis(250), 16 * 1024),
        )?;
        progress.write("execute-progress-")?;
        progress.write("1")?;
        progress.flush()?;
        context.record_heartbeat_value("execute-checkpoint".to_string())?;
        progress.write("execute-progress-")?;
        progress.write("2")?;
        Ok(StepDecision::graceful_complete(()))
    }
}
