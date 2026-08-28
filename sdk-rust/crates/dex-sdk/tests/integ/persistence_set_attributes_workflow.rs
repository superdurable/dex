// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use std::time::SystemTime;

use std::sync::LazyLock;

use dex_sdk::{
    Attribute, AttributeIndex, AttributeMap, Channel, Context, Flow, HandlerResult,
    PersistenceSchema, Step, StepDecision, StepList, Wait,
};

use crate::persistence_workflow::PersistenceModel;

pub(crate) static DATA: LazyLock<Attribute<String>> = LazyLock::new(|| Attribute::new("data"));
pub(crate) static MODEL: LazyLock<Attribute<PersistenceModel>> =
    LazyLock::new(|| Attribute::new("data-model"));
pub(crate) static KEYWORD: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("CustomKeywordField").indexed(AttributeIndex::keyword()));
pub(crate) static FULL_TEXT: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("CustomTextField").indexed(AttributeIndex::full_text()));
pub(crate) static DECIMAL: LazyLock<Attribute<f64>> =
    LazyLock::new(|| Attribute::new("CustomDoubleField").indexed(AttributeIndex::double()));
pub(crate) static INTEGER: LazyLock<Attribute<i32>> =
    LazyLock::new(|| Attribute::new("CustomIntField").indexed(AttributeIndex::int()));
pub(crate) static BOOLEAN: LazyLock<Attribute<bool>> =
    LazyLock::new(|| Attribute::new("CustomBoolField").indexed(AttributeIndex::bool()));
pub(crate) static KEYWORDS: LazyLock<Attribute<Vec<String>>> = LazyLock::new(|| {
    Attribute::new("CustomKeywordArrayField").indexed(AttributeIndex::keyword_array())
});
pub(crate) static DATETIME: LazyLock<Attribute<SystemTime>> =
    LazyLock::new(|| Attribute::new("CustomDatetimeField").indexed(AttributeIndex::date_time()));
pub(crate) static PROCEED: LazyLock<Channel<()>> = LazyLock::new(|| Channel::new("proceed"));

pub(crate) struct PersistenceSetAttributesWorkflow {
    pub(crate) data_map: AttributeMap<String>,
    start: SetAttributesStep,
}

impl PersistenceSetAttributesWorkflow {
    pub(crate) fn new() -> Self {
        Self {
            data_map: AttributeMap::new("data-map"),
            start: SetAttributesStep,
        }
    }
}

impl Flow for PersistenceSetAttributesWorkflow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&DATA)
            .attribute_map(&self.data_map)
            .attribute(&MODEL)
            .attribute(&KEYWORD)
            .attribute(&FULL_TEXT)
            .attribute(&DECIMAL)
            .attribute(&INTEGER)
            .attribute(&BOOLEAN)
            .attribute(&KEYWORDS)
            .attribute(&DATETIME)
            .channel(&PROCEED)
    }
}

struct SetAttributesStep;

impl Step for SetAttributesStep {
    type Input = String;

    fn wait_for(&self, _context: &mut Context, _input: String) -> HandlerResult<Wait> {
        Ok(Wait::until(PROCEED.for_one()))
    }

    fn execute(&self, _context: &mut Context, _input: String) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete("test-result".to_string()))
    }
}
