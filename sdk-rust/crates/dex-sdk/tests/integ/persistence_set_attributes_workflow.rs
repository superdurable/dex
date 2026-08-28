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

static DATA: LazyLock<Attribute<String>> = LazyLock::new(|| Attribute::new("data"));
static MODEL: LazyLock<Attribute<PersistenceModel>> =
    LazyLock::new(|| Attribute::new("data-model"));
static KEYWORD: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("CustomKeywordField").indexed(AttributeIndex::keyword()));
static FULL_TEXT: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("CustomTextField").indexed(AttributeIndex::full_text()));
static DECIMAL: LazyLock<Attribute<f64>> =
    LazyLock::new(|| Attribute::new("CustomDoubleField").indexed(AttributeIndex::double()));
static INTEGER: LazyLock<Attribute<i32>> =
    LazyLock::new(|| Attribute::new("CustomIntField").indexed(AttributeIndex::int()));
static BOOLEAN: LazyLock<Attribute<bool>> =
    LazyLock::new(|| Attribute::new("CustomBoolField").indexed(AttributeIndex::bool()));
static KEYWORDS: LazyLock<Attribute<Vec<String>>> = LazyLock::new(|| {
    Attribute::new("CustomKeywordArrayField").indexed(AttributeIndex::keyword_array())
});
static DATETIME: LazyLock<Attribute<SystemTime>> =
    LazyLock::new(|| Attribute::new("CustomDatetimeField").indexed(AttributeIndex::date_time()));
static PROCEED: LazyLock<Channel<()>> = LazyLock::new(|| Channel::new("proceed"));

pub(crate) struct PersistenceSetAttributesWorkflow {
    pub(crate) data: Attribute<String>,
    pub(crate) data_map: AttributeMap<String>,
    pub(crate) model: Attribute<PersistenceModel>,
    pub(crate) keyword: Attribute<String>,
    pub(crate) full_text: Attribute<String>,
    pub(crate) decimal: Attribute<f64>,
    pub(crate) integer: Attribute<i32>,
    pub(crate) boolean: Attribute<bool>,
    pub(crate) keywords: Attribute<Vec<String>>,
    pub(crate) datetime: Attribute<SystemTime>,
    pub(crate) proceed: Channel<()>,
    start: SetAttributesStep,
}

impl PersistenceSetAttributesWorkflow {
    pub(crate) fn new() -> Self {
        Self {
            data: DATA.clone(),
            data_map: AttributeMap::new("data-map"),
            model: MODEL.clone(),
            keyword: KEYWORD.clone(),
            full_text: FULL_TEXT.clone(),
            decimal: DECIMAL.clone(),
            integer: INTEGER.clone(),
            boolean: BOOLEAN.clone(),
            keywords: KEYWORDS.clone(),
            datetime: DATETIME.clone(),
            proceed: PROCEED.clone(),
            start: SetAttributesStep {
                proceed: PROCEED.clone(),
            },
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
            .attribute(&self.data)
            .attribute_map(&self.data_map)
            .attribute(&self.model)
            .attribute(&self.keyword)
            .attribute(&self.full_text)
            .attribute(&self.decimal)
            .attribute(&self.integer)
            .attribute(&self.boolean)
            .attribute(&self.keywords)
            .attribute(&self.datetime)
            .channel(&self.proceed)
    }
}

struct SetAttributesStep {
    proceed: Channel<()>,
}

impl Step for SetAttributesStep {
    type Input = String;

    fn wait_for(&self, _context: &mut Context, _input: String) -> HandlerResult<Wait> {
        Ok(Wait::until(self.proceed.for_one()))
    }

    fn execute(&self, _context: &mut Context, _input: String) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete("test-result".to_string()))
    }
}
