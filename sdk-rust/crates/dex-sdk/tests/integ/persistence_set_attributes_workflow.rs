// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use std::time::{Duration, SystemTime};

use std::sync::LazyLock;

use dex_sdk::{
    Attribute, AttributeIndex, AttributeMap, Channel, Context, Flow, HandlerResult,
    PersistenceSchema, Rpc, RpcList, Step, StepDecision, StepList, Wait,
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
pub(crate) static DATA_MAP: LazyLock<AttributeMap<String>> =
    LazyLock::new(|| AttributeMap::new("data-map"));

pub(crate) struct PersistenceSetAttributesWorkflow {
    start: SetAttributesStep,
}

impl PersistenceSetAttributesWorkflow {
    pub(crate) const SET_INDEXED: Rpc<(), ()> = Rpc::new("set_indexed");
    pub(crate) const SET_DATA: Rpc<String, ()> = Rpc::new("set_data");
    pub(crate) const SET_MAP_ONE: Rpc<String, ()> = Rpc::new("set_map_one");
    pub(crate) const SET_MAP_SPECIAL: Rpc<String, ()> = Rpc::new("set_map_special");
    pub(crate) const SET_INTEGER: Rpc<i32, ()> = Rpc::new("set_integer");
    pub(crate) const SET_MODEL: Rpc<PersistenceModel, ()> = Rpc::new("set_model");
    pub(crate) const COMPLETE: Rpc<(), ()> = Rpc::new("complete");

    pub(crate) fn new() -> Self {
        Self {
            start: SetAttributesStep,
        }
    }

    fn set_indexed(&self, context: &mut Context) -> HandlerResult<()> {
        KEYWORD.set(context, "keyword-1".to_string())?;
        FULL_TEXT.set(context, "text-1".to_string())?;
        DECIMAL.set(context, 1.0)?;
        INTEGER.set(context, 1)?;
        BOOLEAN.set(context, true)?;
        KEYWORDS.set(
            context,
            vec!["keyword-1".to_string(), "keyword-2".to_string()],
        )?;
        DATETIME.set(
            context,
            SystemTime::UNIX_EPOCH + Duration::new(1_731_456_001, 731_000_000),
        )
    }

    fn set_data(&self, context: &mut Context, input: String) -> HandlerResult<()> {
        DATA.set(context, input)
    }

    fn set_map_one(&self, context: &mut Context, input: String) -> HandlerResult<()> {
        DATA_MAP.set(context, "one", input)
    }

    fn set_map_special(&self, context: &mut Context, input: String) -> HandlerResult<()> {
        DATA_MAP.set(context, "special % key", input)
    }

    fn set_integer(&self, context: &mut Context, input: i32) -> HandlerResult<()> {
        INTEGER.set(context, input)
    }

    fn set_model(&self, context: &mut Context, input: PersistenceModel) -> HandlerResult<()> {
        MODEL.set(context, input)
    }

    fn complete(&self, context: &mut Context) -> HandlerResult<()> {
        PROCEED.publish(context, ())
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
            .attribute_map(&DATA_MAP)
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

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .procedure_without_input(Self::SET_INDEXED, Self::set_indexed)
            .procedure(Self::SET_DATA, Self::set_data)
            .procedure(Self::SET_MAP_ONE, Self::set_map_one)
            .procedure(Self::SET_MAP_SPECIAL, Self::set_map_special)
            .procedure(Self::SET_INTEGER, Self::set_integer)
            .procedure(Self::SET_MODEL, Self::set_model)
            .procedure_without_input(Self::COMPLETE, Self::complete)
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
