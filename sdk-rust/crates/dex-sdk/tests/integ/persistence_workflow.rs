// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use std::time::{Duration, SystemTime};

use dex_sdk::{
    Attribute, AttributeIndex, AttributeMap, Context, Flow, HandlerError, HandlerResult,
    PersistenceSchema, Step, StepDecision, StepList, Wait,
};

#[derive(Debug, PartialEq, serde::Deserialize, serde::Serialize)]
pub(crate) struct PersistenceModel {
    pub(crate) value: i32,
}

pub(crate) struct PersistenceWorkflow {
    pub(crate) initial: Attribute<String>,
    pub(crate) data: Attribute<String>,
    pub(crate) model: Attribute<PersistenceModel>,
    pub(crate) data_map: AttributeMap<String>,
    pub(crate) keyword: Attribute<String>,
    pub(crate) integer: Attribute<i32>,
    pub(crate) datetime: Attribute<SystemTime>,
    start: PersistenceStep,
}

impl PersistenceWorkflow {
    pub(crate) fn new() -> Self {
        let data = Attribute::new("data-obj-1");
        let model = Attribute::new("data-obj-2");
        let data_map = AttributeMap::new("data-map");
        let keyword = Attribute::new("CustomKeywordField").indexed(AttributeIndex::keyword());
        let integer = Attribute::new("CustomIntField").indexed(AttributeIndex::int());
        let datetime = Attribute::new("CustomDatetimeField").indexed(AttributeIndex::date_time());
        Self {
            initial: Attribute::new("data-obj-0"),
            start: PersistenceStep {
                data: data.clone(),
                model: model.clone(),
                data_map: data_map.clone(),
                keyword: keyword.clone(),
                integer: integer.clone(),
                datetime: datetime.clone(),
            },
            data,
            model,
            data_map,
            keyword,
            integer,
            datetime,
        }
    }
}

impl Flow for PersistenceWorkflow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&self.initial)
            .attribute(&self.data)
            .attribute(&self.model)
            .attribute_map(&self.data_map)
            .attribute(&self.keyword)
            .attribute(&self.integer)
            .attribute(&self.datetime)
    }
}

struct PersistenceStep {
    data: Attribute<String>,
    model: Attribute<PersistenceModel>,
    data_map: AttributeMap<String>,
    keyword: Attribute<String>,
    integer: Attribute<i32>,
    datetime: Attribute<SystemTime>,
}

impl Step for PersistenceStep {
    type Input = String;

    fn wait_for(&self, context: &mut Context, input: String) -> HandlerResult<Wait> {
        self.data.set(context, input.clone())?;
        self.data_map.set(context, "one", input.clone())?;
        context.set_step_execution_local("local", input.clone())?;
        context.record_event("written", input)?;
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, context: &mut Context, input: String) -> HandlerResult<StepDecision> {
        let local: String = context.step_execution_local("local")?;
        if local != input {
            return Err(HandlerError::new(
                "PersistenceFailure",
                "step execution local did not round trip",
            ));
        }
        self.keyword.set(context, input)?;
        self.integer.set(context, 1)?;
        self.datetime.set(
            context,
            SystemTime::UNIX_EPOCH + Duration::from_secs(1_681_766_269),
        )?;
        self.model.set(context, PersistenceModel { value: 0 })?;
        self.data_map.delete(context, "one")?;
        Ok(StepDecision::graceful_complete(
            self.data.get_required(context)?,
        ))
    }
}
