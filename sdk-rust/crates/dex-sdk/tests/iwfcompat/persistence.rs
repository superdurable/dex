// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use std::time::{Duration, Instant, SystemTime};

use dex_sdk::{
    Attribute, AttributeIndex, AttributeMap, Channel, Client, Context, Flow, FlowStatus,
    HandlerError, HandlerResult, IdReusePolicy, PersistenceSchema, Registry, SdkError, SdkResult,
    StartFlowOptions, Step, StepDecision, StepList, Wait,
};
use serde_json::Value as JsonValue;

use crate::support::{DexDevTestEnvironment, flow_id};

#[derive(Debug, PartialEq, serde::Deserialize, serde::Serialize)]
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

#[derive(Debug, PartialEq, serde::Deserialize, serde::Serialize)]
struct GoPersistenceModel {
    number: i64,
    text: String,
    datetime: SystemTime,
}

struct GoPersistenceWorkflow {
    data: Attribute<GoPersistenceModel>,
    text: Attribute<String>,
    data_map: AttributeMap<i32>,
    keyword: Attribute<String>,
    full_text: Attribute<String>,
    boolean: Attribute<bool>,
    datetime: Attribute<SystemTime>,
    integer: Attribute<i64>,
    decimal: Attribute<f64>,
    first: GoPersistenceFirstStep,
    second: GoPersistenceSecondStep,
}

impl GoPersistenceWorkflow {
    const KEYWORD_KEY: &str = "CustomKeywordField";
    const FULL_TEXT_KEY: &str = "CustomStringField";
    const BOOL_KEY: &str = "CustomBoolField";
    const DATETIME_KEY: &str = "CustomDatetimeField";
    const INT_KEY: &str = "CustomIntField";
    const DOUBLE_KEY: &str = "CustomDoubleField";

    fn new() -> Self {
        let data = Attribute::new("data");
        let text = Attribute::new("text");
        let data_map = AttributeMap::new("items");
        let keyword = Attribute::new("keyword")
            .indexed(AttributeIndex::keyword().with_key(Self::KEYWORD_KEY));
        let full_text = Attribute::new("search-text")
            .indexed(AttributeIndex::full_text().with_key(Self::FULL_TEXT_KEY));
        let boolean =
            Attribute::new("bool").indexed(AttributeIndex::bool().with_key(Self::BOOL_KEY));
        let datetime = Attribute::new("datetime")
            .indexed(AttributeIndex::date_time().with_key(Self::DATETIME_KEY));
        let integer = Attribute::new("int").indexed(AttributeIndex::int().with_key(Self::INT_KEY));
        let decimal =
            Attribute::new("double").indexed(AttributeIndex::double().with_key(Self::DOUBLE_KEY));
        let first = GoPersistenceFirstStep {
            data: data.clone(),
            text: text.clone(),
            data_map: data_map.clone(),
            keyword: keyword.clone(),
            full_text: full_text.clone(),
            boolean: boolean.clone(),
            datetime: datetime.clone(),
            integer: integer.clone(),
            decimal: decimal.clone(),
        };
        let second = GoPersistenceSecondStep {
            data: data.clone(),
            keyword: keyword.clone(),
            full_text: full_text.clone(),
            boolean: boolean.clone(),
            datetime: datetime.clone(),
            decimal: decimal.clone(),
        };
        Self {
            data,
            text,
            data_map,
            keyword,
            full_text,
            boolean,
            datetime,
            integer,
            decimal,
            first,
            second,
        }
    }
}

impl Flow for GoPersistenceWorkflow {
    type StartInput = GoPersistenceModel;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.first).and(&self.second)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&self.data)
            .attribute(&self.text)
            .attribute_map(&self.data_map)
            .attribute(&self.keyword)
            .attribute(&self.full_text)
            .attribute(&self.boolean)
            .attribute(&self.datetime)
            .attribute(&self.integer)
            .attribute(&self.decimal)
    }
}

struct GoPersistenceFirstStep {
    data: Attribute<GoPersistenceModel>,
    text: Attribute<String>,
    data_map: AttributeMap<i32>,
    keyword: Attribute<String>,
    full_text: Attribute<String>,
    boolean: Attribute<bool>,
    datetime: Attribute<SystemTime>,
    integer: Attribute<i64>,
    decimal: Attribute<f64>,
}

impl Step for GoPersistenceFirstStep {
    type Input = GoPersistenceModel;

    fn wait_for(&self, context: &mut Context, input: GoPersistenceModel) -> HandlerResult<Wait> {
        if self.keyword.get_required(context)? != "init-keyword" {
            return Err(HandlerError::new("unexpected initial keyword"));
        }
        if self.full_text.get_required(context)? != "init-text" {
            return Err(HandlerError::new("unexpected initial search text"));
        }
        if self.data.get(context)?.is_some() {
            return Err(HandlerError::new(
                "data must not exist before its first write",
            ));
        }
        if self.data_map.get_required(context, "one")? != 10 {
            return Err(HandlerError::new("unexpected initial map value"));
        }
        if self.boolean.get_required(context)? {
            return Err(HandlerError::new("unexpected initial bool value"));
        }
        if self.integer.get_required(context)? != 0 {
            return Err(HandlerError::new("unexpected initial integer value"));
        }
        if self.decimal.get_required(context)? != 2.1 {
            return Err(HandlerError::new("unexpected initial double value"));
        }
        if self.datetime.get_required(context)? != input.datetime {
            return Err(HandlerError::new("unexpected initial datetime value"));
        }
        self.data.set(context, input)?;
        self.text.set(context, "a string".to_string())?;
        self.data_map.set(context, "one", 11)?;
        self.integer.set(context, 1)?;
        Ok(Wait::skip_immediately())
    }

    fn execute(
        &self,
        context: &mut Context,
        input: GoPersistenceModel,
    ) -> HandlerResult<StepDecision> {
        if self.integer.get_required(context)? != 1 {
            return Err(HandlerError::new("wait_for integer write was not visible"));
        }
        let data = self.data.get_required(context)?;
        if data.text != input.text || data.number != input.number {
            return Err(HandlerError::new("wait_for data write was not visible"));
        }
        self.datetime.set(context, data.datetime)?;
        self.boolean.set(context, true)?;
        Ok(StepDecision::go_to(&GoPersistenceSecondStep::new(), ()))
    }
}

struct GoPersistenceSecondStep {
    data: Attribute<GoPersistenceModel>,
    keyword: Attribute<String>,
    full_text: Attribute<String>,
    boolean: Attribute<bool>,
    datetime: Attribute<SystemTime>,
    decimal: Attribute<f64>,
}

impl GoPersistenceSecondStep {
    fn new() -> Self {
        Self {
            data: Attribute::new("data"),
            keyword: Attribute::new("keyword")
                .indexed(AttributeIndex::keyword().with_key(GoPersistenceWorkflow::KEYWORD_KEY)),
            full_text: Attribute::new("search-text").indexed(
                AttributeIndex::full_text().with_key(GoPersistenceWorkflow::FULL_TEXT_KEY),
            ),
            boolean: Attribute::new("bool")
                .indexed(AttributeIndex::bool().with_key(GoPersistenceWorkflow::BOOL_KEY)),
            datetime: Attribute::new("datetime")
                .indexed(AttributeIndex::date_time().with_key(GoPersistenceWorkflow::DATETIME_KEY)),
            decimal: Attribute::new("double")
                .indexed(AttributeIndex::double().with_key(GoPersistenceWorkflow::DOUBLE_KEY)),
        }
    }
}

impl Step for GoPersistenceSecondStep {
    type Input = ();

    fn wait_for(&self, context: &mut Context, (): ()) -> HandlerResult<Wait> {
        let data = self.data.get_required(context)?;
        if self.datetime.get_required(context)? != data.datetime {
            return Err(HandlerError::new("persisted datetime did not round trip"));
        }
        if !self.boolean.get_required(context)? {
            return Err(HandlerError::new("persisted bool is false"));
        }
        self.decimal.set(context, 1.0)?;
        self.full_text.set(context, "Hail Dex!".to_string())?;
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        if self.full_text.get_required(context)? != "Hail Dex!" {
            return Err(HandlerError::new("unexpected persisted text"));
        }
        self.keyword.set(context, "Dex".to_string())?;
        Ok(StepDecision::graceful_complete("done".to_string()))
    }
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

struct PersistenceSetAttributesWorkflow {
    data: Attribute<String>,
    data_map: AttributeMap<String>,
    model: Attribute<PersistenceModel>,
    keyword: Attribute<String>,
    full_text: Attribute<String>,
    decimal: Attribute<f64>,
    integer: Attribute<i32>,
    boolean: Attribute<bool>,
    keywords: Attribute<Vec<String>>,
    datetime: Attribute<SystemTime>,
    proceed: Channel<()>,
    start: SetAttributesStep,
}

impl PersistenceSetAttributesWorkflow {
    fn new() -> Self {
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

struct SearchFlowsWorkflow {
    keyword: Attribute<String>,
    start: SearchFlowsIndexStep,
}

impl SearchFlowsWorkflow {
    const KEYWORD_KEY: &str = "CustomKeywordField";

    fn new() -> Self {
        let keyword = Attribute::new(Self::KEYWORD_KEY).indexed(AttributeIndex::keyword());
        Self {
            start: SearchFlowsIndexStep {
                keyword: keyword.clone(),
            },
            keyword,
        }
    }
}

impl Flow for SearchFlowsWorkflow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().attribute(&self.keyword)
    }
}

struct SearchFlowsIndexStep {
    keyword: Attribute<String>,
}

impl Step for SearchFlowsIndexStep {
    type Input = String;

    fn execute(&self, context: &mut Context, input: String) -> HandlerResult<StepDecision> {
        self.keyword.set(context, input.clone())?;
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

#[test]
#[ignore = "requires dexcli dev"]
fn persistence_reads_wait_and_execute_writes() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(PersistenceWorkflow::new()));
    let workflow = PersistenceWorkflow::new();
    let flow_id = flow_id("persistence");
    let options = StartFlowOptions::new()
        .initial_attribute(&workflow.initial, "initial".to_string())
        .initial_attribute_map(&workflow.data_map, "one", "initial".to_string());
    environment
        .client
        .start_flow_with_options(&workflow, &flow_id, "input".to_string(), options)
        .expect("start persistence Flow");
    assert_eq!(
        "input",
        environment
            .client
            .wait_for_flow_with_timeout::<String>(&flow_id, Duration::from_secs(30))
            .expect("complete persistence Flow")
    );
    assert_eq!(
        Some("input".to_string()),
        environment
            .client
            .get_attribute(&flow_id, &workflow.data)
            .expect("get data Attribute")
    );
    assert_eq!(
        Some("initial".to_string()),
        environment
            .client
            .get_attribute(&flow_id, &workflow.initial)
            .expect("get initial Attribute")
    );
    assert_eq!(
        None,
        environment
            .client
            .get_attribute_map(&flow_id, &workflow.data_map, "one")
            .expect("get deleted AttributeMap entry")
    );
    assert_eq!(
        Some("input".to_string()),
        environment
            .client
            .get_attribute(&flow_id, &workflow.keyword)
            .expect("get keyword Attribute")
    );
    assert_eq!(
        Some(1),
        environment
            .client
            .get_attribute(&flow_id, &workflow.integer)
            .expect("get integer Attribute")
    );
    assert_eq!(
        Some(SystemTime::UNIX_EPOCH + Duration::from_secs(1_681_766_269)),
        environment
            .client
            .get_attribute(&flow_id, &workflow.datetime)
            .expect("get datetime Attribute")
    );
    assert_eq!(
        Some(PersistenceModel { value: 0 }),
        environment
            .client
            .get_attribute(&flow_id, &workflow.model)
            .expect("get model Attribute")
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn client_sets_and_reads_search_attributes() {
    let environment = DexDevTestEnvironment::start(
        Registry::new().register(PersistenceSetAttributesWorkflow::new()),
    );
    let workflow = PersistenceSetAttributesWorkflow::new();
    let flow_id = flow_id("set-search-attributes");
    let keywords = vec!["keyword-1".to_string(), "keyword-2".to_string()];
    let datetime = SystemTime::UNIX_EPOCH + Duration::new(1_731_456_001, 731_455_544);
    environment
        .client
        .start_flow(&workflow, &flow_id, "start".to_string())
        .expect("start set-search-attributes Flow");
    environment
        .client
        .set_attribute(&flow_id, &workflow.keyword, "keyword-1".to_string())
        .expect("set keyword Attribute");
    environment
        .client
        .set_attribute(&flow_id, &workflow.full_text, "text-1".to_string())
        .expect("set full-text Attribute");
    environment
        .client
        .set_attribute(&flow_id, &workflow.decimal, 1.0)
        .expect("set double Attribute");
    environment
        .client
        .set_attribute(&flow_id, &workflow.integer, 1)
        .expect("set integer Attribute");
    environment
        .client
        .set_attribute(&flow_id, &workflow.boolean, true)
        .expect("set boolean Attribute");
    environment
        .client
        .set_attribute(&flow_id, &workflow.keywords, keywords.clone())
        .expect("set keyword-array Attribute");
    environment
        .client
        .set_attribute(&flow_id, &workflow.datetime, datetime)
        .expect("set datetime Attribute");
    environment
        .client
        .publish(&flow_id, &workflow.proceed, ())
        .expect("publish proceed message");
    assert_eq!(
        "test-result",
        environment
            .client
            .wait_for_flow_with_timeout::<String>(&flow_id, Duration::from_secs(30))
            .expect("complete set-search-attributes Flow")
    );
    assert_eq!(
        Some("keyword-1".to_string()),
        environment
            .client
            .get_attribute(&flow_id, &workflow.keyword)
            .expect("get keyword Attribute")
    );
    assert_eq!(
        Some("text-1".to_string()),
        environment
            .client
            .get_attribute(&flow_id, &workflow.full_text)
            .expect("get full-text Attribute")
    );
    assert_eq!(
        Some(1.0),
        environment
            .client
            .get_attribute(&flow_id, &workflow.decimal)
            .expect("get double Attribute")
    );
    assert_eq!(
        Some(1),
        environment
            .client
            .get_attribute(&flow_id, &workflow.integer)
            .expect("get integer Attribute")
    );
    assert_eq!(
        Some(true),
        environment
            .client
            .get_attribute(&flow_id, &workflow.boolean)
            .expect("get boolean Attribute")
    );
    assert_eq!(
        Some(keywords),
        environment
            .client
            .get_attribute(&flow_id, &workflow.keywords)
            .expect("get keyword-array Attribute")
    );
    assert_eq!(
        Some(datetime),
        environment
            .client
            .get_attribute(&flow_id, &workflow.datetime)
            .expect("get datetime Attribute")
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn client_sets_and_reads_data_attributes() {
    let environment = DexDevTestEnvironment::start(
        Registry::new().register(PersistenceSetAttributesWorkflow::new()),
    );
    let workflow = PersistenceSetAttributesWorkflow::new();
    let flow_id = flow_id("set-data-attributes");
    environment
        .client
        .start_flow(&workflow, &flow_id, "start".to_string())
        .expect("start set-data-attributes Flow");
    environment
        .client
        .set_attribute(&flow_id, &workflow.data, "query-start".to_string())
        .expect("set data Attribute");
    environment
        .client
        .set_attribute_map(
            &flow_id,
            &workflow.data_map,
            "one",
            "mapped-value".to_string(),
        )
        .expect("set AttributeMap entry");
    environment
        .client
        .set_attribute(&flow_id, &workflow.model, PersistenceModel { value: 7 })
        .expect("set model Attribute");
    environment
        .client
        .publish(&flow_id, &workflow.proceed, ())
        .expect("publish proceed message");
    assert_eq!(
        "test-result",
        environment
            .client
            .wait_for_flow_with_timeout::<String>(&flow_id, Duration::from_secs(30))
            .expect("complete set-data-attributes Flow")
    );
    assert_eq!(
        Some("query-start".to_string()),
        environment
            .client
            .get_attribute(&flow_id, &workflow.data)
            .expect("get data Attribute")
    );
    assert_eq!(
        Some("mapped-value".to_string()),
        environment
            .client
            .get_attribute_map(&flow_id, &workflow.data_map, "one")
            .expect("get AttributeMap entry")
    );
    assert_eq!(
        Some(PersistenceModel { value: 7 }),
        environment
            .client
            .get_attribute(&flow_id, &workflow.model)
            .expect("get model Attribute")
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn go_persistence_contract_round_trips_all_values_and_search_index() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(GoPersistenceWorkflow::new()));
    let workflow = GoPersistenceWorkflow::new();
    let flow_id = flow_id("go-persistence");
    let datetime = SystemTime::now();
    let input = GoPersistenceModel {
        number: datetime
            .duration_since(SystemTime::UNIX_EPOCH)
            .expect("current time after epoch")
            .as_nanos() as i64,
        text: flow_id.clone(),
        datetime,
    };
    let options = StartFlowOptions::new()
        .initial_attribute(&workflow.keyword, "init-keyword".to_string())
        .initial_attribute(&workflow.full_text, "init-text".to_string())
        .initial_attribute(&workflow.boolean, false)
        .initial_attribute(&workflow.datetime, datetime)
        .initial_attribute(&workflow.integer, 0)
        .initial_attribute(&workflow.decimal, 2.1)
        .initial_attribute_map(&workflow.data_map, "one", 10);
    environment
        .client
        .start_flow_with_options(&workflow, &flow_id, input, options)
        .expect("start Go persistence compatibility Flow");
    assert_eq!(
        "done",
        environment
            .client
            .wait_for_flow_with_timeout::<String>(&flow_id, Duration::from_secs(30))
            .expect("complete Go persistence compatibility Flow")
    );

    assert_eq!(
        Some(GoPersistenceModel {
            number: datetime
                .duration_since(SystemTime::UNIX_EPOCH)
                .expect("current time after epoch")
                .as_nanos() as i64,
            text: flow_id.clone(),
            datetime,
        }),
        environment
            .client
            .get_attribute(&flow_id, &workflow.data)
            .expect("get persisted model")
    );
    assert_eq!(
        Some(11),
        environment
            .client
            .get_attribute_map(&flow_id, &workflow.data_map, "one")
            .expect("get persisted AttributeMap value")
    );
    assert_eq!(
        Some("a string".to_string()),
        environment
            .client
            .get_attribute(&flow_id, &workflow.text)
            .expect("get persisted text")
    );
    assert_eq!(
        Some("Dex".to_string()),
        environment
            .client
            .get_attribute(&flow_id, &workflow.keyword)
            .expect("get persisted keyword")
    );
    assert_eq!(
        Some("Hail Dex!".to_string()),
        environment
            .client
            .get_attribute(&flow_id, &workflow.full_text)
            .expect("get persisted full-text value")
    );
    assert_eq!(
        Some(true),
        environment
            .client
            .get_attribute(&flow_id, &workflow.boolean)
            .expect("get persisted bool")
    );
    assert_eq!(
        Some(1),
        environment
            .client
            .get_attribute(&flow_id, &workflow.integer)
            .expect("get persisted integer")
    );
    assert_eq!(
        Some(1.0),
        environment
            .client
            .get_attribute(&flow_id, &workflow.decimal)
            .expect("get persisted double")
    );

    let query = format!("{} = 'Dex'", GoPersistenceWorkflow::KEYWORD_KEY);
    let deadline = Instant::now() + Duration::from_secs(20);
    loop {
        if environment
            .client
            .search_flows_page(&query, 100, "")
            .is_ok_and(|page| {
                page.flows.into_iter().any(|entry| {
                    entry.flow_id == flow_id && entry.flow_type == workflow.flow_type()
                })
            })
        {
            break;
        }
        assert!(
            Instant::now() < deadline,
            "Go persistence compatibility Flow was not indexed"
        );
        std::thread::sleep(Duration::from_millis(200));
    }
}

#[test]
#[ignore = "requires dexcli dev"]
fn search_flows_finds_indexed_flow() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(SearchFlowsWorkflow::new()));
    let workflow = SearchFlowsWorkflow::new();
    let keyword_value = flow_id("sf");
    let flow_id = flow_id("search-flows");
    environment
        .client
        .start_flow_with_options(
            &workflow,
            &flow_id,
            keyword_value.clone(),
            StartFlowOptions::new().id_reuse_policy(IdReusePolicy::Disallow),
        )
        .expect("start indexed Flow");
    assert_eq!(
        keyword_value,
        environment
            .client
            .wait_for_flow_with_timeout::<String>(&flow_id, Duration::from_secs(30))
            .expect("complete indexed Flow")
    );
    let query = format!("{} = '{}'", SearchFlowsWorkflow::KEYWORD_KEY, keyword_value);
    let deadline = Instant::now() + Duration::from_secs(30);
    let mut last_error = None;
    let entry = loop {
        match environment.client.search_flows_page(&query, 100, "") {
            Ok(page) => {
                if let Some(entry) = page
                    .flows
                    .into_iter()
                    .find(|entry| entry.flow_id == flow_id)
                {
                    break entry;
                }
            }
            Err(error) => last_error = Some(error.to_string()),
        }
        assert!(
            Instant::now() < deadline,
            "Flow {flow_id} not found via SearchFlows: {last_error:?}"
        );
        std::thread::sleep(Duration::from_millis(200));
    };
    assert_eq!(flow_id, entry.flow_id);
    assert!(!entry.run_id.is_empty());
    assert_eq!(FlowStatus::Completed, entry.status);
    assert!(entry.started_at.is_some());
    assert_eq!(
        Some(&JsonValue::String(keyword_value)),
        entry
            .search_attributes
            .get(SearchFlowsWorkflow::KEYWORD_KEY)
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn search_flows_rejects_negative_page_size() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(SearchFlowsWorkflow::new()));
    assert!(matches!(
        environment
            .client
            .search_flows("CustomKeywordField = 'x'", -1)
            .expect_err("negative search page size must fail"),
        SdkError::InvalidArgument { .. }
    ));
}
