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
    Attribute, AttributeIndex, Context, Flow, HandlerResult, PersistenceSchema, Step, StepDecision,
    StepList,
};

static KEYWORD: LazyLock<Attribute<String>> = LazyLock::new(|| {
    Attribute::new(SearchFlowsWorkflow::KEYWORD_KEY).indexed(AttributeIndex::keyword())
});

pub(crate) struct SearchFlowsWorkflow {
    start: IndexStep,
}

impl SearchFlowsWorkflow {
    pub(crate) const KEYWORD_KEY: &str = "CustomKeywordField";

    pub(crate) fn new() -> Self {
        Self { start: IndexStep }
    }
}

impl Flow for SearchFlowsWorkflow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().attribute(&KEYWORD)
    }
}

struct IndexStep;

impl Step for IndexStep {
    type Input = String;

    fn execute(&self, context: &mut Context, input: String) -> HandlerResult<StepDecision> {
        KEYWORD.set(context, input.clone())?;
        Ok(StepDecision::graceful_complete(input))
    }
}
