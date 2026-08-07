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
    Attribute, AttributeIndex, AttributeIndexKind, AttributeMap, Client, Context, Flow,
    HandlerError, HandlerResult, PersistenceSchema, SdkResult, StartFlowOptions, Step,
    StepDecision, StepList, Wait,
};

#[derive(serde::Deserialize, serde::Serialize)]
struct PersistenceModel {
    value: i32,
}

struct PersistenceWorkflow {
    initial: Attribute<String>,
    data: Attribute<String>,
    model: Attribute<PersistenceModel>,
    data_map: AttributeMap<String>,
    keyword: Attribute<String>,
    integer: Attribute<i32>,
    datetime: Attribute<SystemTime>,
    start: PersistenceStep,
}

impl PersistenceWorkflow {
    fn new() -> Self {
        let data = Attribute::new("data-obj-1");
        let model = Attribute::new("data-obj-2");
        let data_map = AttributeMap::new("data-map");
        let keyword = Attribute::new("CustomKeywordField")
            .indexed(AttributeIndex::new(AttributeIndexKind::Keyword));
        let integer =
            Attribute::new("CustomIntField").indexed(AttributeIndex::new(AttributeIndexKind::Int));
        let datetime = Attribute::new("CustomDatetimeField")
            .indexed(AttributeIndex::new(AttributeIndexKind::DateTime));
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

    fn steps(&self) -> StepList<Self::StartInput> {
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
            return Err(HandlerError::new("step execution local did not round trip"));
        }
        self.keyword.set(context, input)?;
        self.integer.set(context, 1)?;
        self.datetime.set(context, SystemTime::UNIX_EPOCH)?;
        self.model.set(context, PersistenceModel { value: 1 })?;
        self.data_map.delete(context, "one")?;
        Ok(StepDecision::graceful_complete(
            self.data.get_required(context)?,
        ))
    }
}

struct PersistenceSetAttributesWorkflow {
    data: Attribute<String>,
    data_map: AttributeMap<String>,
    keyword: Attribute<String>,
    full_text: Attribute<String>,
    decimal: Attribute<f64>,
    integer: Attribute<i32>,
    boolean: Attribute<bool>,
    keywords: Attribute<Vec<String>>,
    datetime: Attribute<SystemTime>,
    start: SetAttributesStep,
}

impl PersistenceSetAttributesWorkflow {
    fn new() -> Self {
        Self {
            data: Attribute::new("data"),
            data_map: AttributeMap::new("data-map"),
            keyword: Attribute::new("keyword")
                .indexed(AttributeIndex::new(AttributeIndexKind::Keyword)),
            full_text: Attribute::new("full-text")
                .indexed(AttributeIndex::new(AttributeIndexKind::FullText)),
            decimal: Attribute::new("double")
                .indexed(AttributeIndex::new(AttributeIndexKind::Double)),
            integer: Attribute::new("int").indexed(AttributeIndex::new(AttributeIndexKind::Int)),
            boolean: Attribute::new("bool").indexed(AttributeIndex::new(AttributeIndexKind::Bool)),
            keywords: Attribute::new("keywords")
                .indexed(AttributeIndex::new(AttributeIndexKind::KeywordArray)),
            datetime: Attribute::new("datetime")
                .indexed(AttributeIndex::new(AttributeIndexKind::DateTime)),
            start: SetAttributesStep,
        }
    }
}

impl Flow for PersistenceSetAttributesWorkflow {
    type StartInput = String;

    fn steps(&self) -> StepList<Self::StartInput> {
        StepList::start(&self.start)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&self.data)
            .attribute_map(&self.data_map)
            .attribute(&self.keyword)
            .attribute(&self.full_text)
            .attribute(&self.decimal)
            .attribute(&self.integer)
            .attribute(&self.boolean)
            .attribute(&self.keywords)
            .attribute(&self.datetime)
    }
}

struct SetAttributesStep;

impl Step for SetAttributesStep {
    type Input = String;

    fn execute(&self, _context: &mut Context, input: String) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(input))
    }
}

fn compile_persistence_test(client: &Client) -> SdkResult<()> {
    let workflow = PersistenceWorkflow::new();
    let options = StartFlowOptions::new().initial_attribute(&workflow.initial, "initial".into());
    client.start_flow_with_options(&workflow, "persistence", "input".into(), options)?;
    let output: String = client.wait_for_flow("persistence")?;
    assert_eq!("input", output);
    client.set_attribute("persistence", &workflow.data, "updated".into())?;
    let value: Option<String> = client.get_attribute("persistence", &workflow.data)?;
    assert_eq!(Some("updated".into()), value);
    client.set_attribute_map("persistence", &workflow.data_map, "one", "value".into())?;
    let _: Option<String> = client.get_attribute_map("persistence", &workflow.data_map, "one")?;
    Ok(())
}

fn compile_set_attributes_test(client: &Client) -> SdkResult<()> {
    let workflow = PersistenceSetAttributesWorkflow::new();
    client.start_flow(&workflow, "set-attributes", "input".into())?;
    let _: String = client.wait_for_flow("set-attributes")?;
    Ok(())
}
