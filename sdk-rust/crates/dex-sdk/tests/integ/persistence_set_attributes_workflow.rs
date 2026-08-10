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

use dex_sdk::{
    Attribute, AttributeIndex, AttributeMap, Channel, Context, Flow, HandlerResult,
    PersistenceSchema, Step, StepDecision, StepList, Wait,
};

use crate::persistence_workflow::PersistenceModel;

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
            data: Attribute::new("data"),
            data_map: AttributeMap::new("data-map"),
            model: Attribute::new("data-model"),
            keyword: Attribute::new("CustomKeywordField").indexed(AttributeIndex::keyword()),
            full_text: Attribute::new("CustomTextField").indexed(AttributeIndex::full_text()),
            decimal: Attribute::new("CustomDoubleField").indexed(AttributeIndex::double()),
            integer: Attribute::new("CustomIntField").indexed(AttributeIndex::int()),
            boolean: Attribute::new("CustomBoolField").indexed(AttributeIndex::bool()),
            keywords: Attribute::new("CustomKeywordArrayField")
                .indexed(AttributeIndex::keyword_array()),
            datetime: Attribute::new("CustomDatetimeField").indexed(AttributeIndex::date_time()),
            proceed: Channel::new("proceed"),
            start: SetAttributesStep {
                proceed: Channel::new("proceed"),
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
