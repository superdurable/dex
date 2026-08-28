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

use std::sync::LazyLock;

use dex_sdk::{
    Attribute, AttributeIndex, AttributeMap, Context, Flow, HandlerError, HandlerResult,
    PersistenceSchema, Step, StepDecision, StepList, Wait,
};

pub(crate) static INITIAL: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("data-obj-0"));
pub(crate) static DATA: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("data-obj-1"));
pub(crate) static MODEL: LazyLock<Attribute<PersistenceModel>> =
    LazyLock::new(|| Attribute::new("data-obj-2"));
pub(crate) static KEYWORD: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("CustomKeywordField").indexed(AttributeIndex::keyword()));
pub(crate) static INTEGER: LazyLock<Attribute<i32>> =
    LazyLock::new(|| Attribute::new("CustomIntField").indexed(AttributeIndex::int()));
pub(crate) static DATETIME: LazyLock<Attribute<SystemTime>> =
    LazyLock::new(|| Attribute::new("CustomDatetimeField").indexed(AttributeIndex::date_time()));

#[derive(Debug, PartialEq, serde::Deserialize, serde::Serialize)]
pub(crate) struct PersistenceModel {
    pub(crate) value: i32,
}

pub(crate) struct PersistenceWorkflow {
    pub(crate) data_map: AttributeMap<String>,
    start: PersistenceStep,
}

impl PersistenceWorkflow {
    pub(crate) fn new() -> Self {
        let data_map = AttributeMap::new("data-map");
        Self {
            start: PersistenceStep {
                data_map: data_map.clone(),
            },
            data_map,
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
            .attribute(&INITIAL)
            .attribute(&DATA)
            .attribute(&MODEL)
            .attribute_map(&self.data_map)
            .attribute(&KEYWORD)
            .attribute(&INTEGER)
            .attribute(&DATETIME)
    }
}

struct PersistenceStep {
    data_map: AttributeMap<String>,
}

impl Step for PersistenceStep {
    type Input = String;

    fn wait_for(&self, context: &mut Context, input: String) -> HandlerResult<Wait> {
        DATA.set(context, input.clone())?;
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
        KEYWORD.set(context, input)?;
        INTEGER.set(context, 1)?;
        DATETIME.set(
            context,
            SystemTime::UNIX_EPOCH + Duration::from_secs(1_681_766_269),
        )?;
        MODEL.set(context, PersistenceModel { value: 0 })?;
        self.data_map.delete(context, "one")?;
        Ok(StepDecision::graceful_complete(DATA.get_required(context)?))
    }
}
