// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::time::{Duration, Instant, SystemTime};

use dex_sdk::{
    Attribute, AttributeIndex, AttributeMap, Context, Flow, HandlerError, HandlerResult,
    PersistenceSchema, Registry, StartFlowOptions, Step, StepDecision, StepList, StepOptions, Wait,
};

use crate::support::{DexDevTestEnvironment, flow_id};

#[derive(Debug, PartialEq, serde::Deserialize, serde::Serialize)]
struct PersistenceModel {
    number: i64,
    text: String,
    datetime: SystemTime,
}

struct PersistenceWorkflow {
    data: Attribute<PersistenceModel>,
    text: Attribute<String>,
    data_map: AttributeMap<i32>,
    keyword: Attribute<String>,
    full_text: Attribute<String>,
    boolean: Attribute<bool>,
    datetime: Attribute<SystemTime>,
    integer: Attribute<i64>,
    decimal: Attribute<f64>,
    first: PersistenceFirstStep,
    second: PersistenceSecondStep,
}

impl PersistenceWorkflow {
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
        Self {
            first: PersistenceFirstStep {
                data: data.clone(),
                text: text.clone(),
                data_map: data_map.clone(),
                keyword: keyword.clone(),
                full_text: full_text.clone(),
                boolean: boolean.clone(),
                datetime: datetime.clone(),
                integer: integer.clone(),
                decimal: decimal.clone(),
            },
            second: PersistenceSecondStep {
                data: data.clone(),
                keyword: keyword.clone(),
                full_text: full_text.clone(),
                boolean: boolean.clone(),
                datetime: datetime.clone(),
                decimal: decimal.clone(),
            },
            data,
            text,
            data_map,
            keyword,
            full_text,
            boolean,
            datetime,
            integer,
            decimal,
        }
    }
}

impl Flow for PersistenceWorkflow {
    type StartInput = PersistenceModel;

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

struct PersistenceFirstStep {
    data: Attribute<PersistenceModel>,
    text: Attribute<String>,
    data_map: AttributeMap<i32>,
    keyword: Attribute<String>,
    full_text: Attribute<String>,
    boolean: Attribute<bool>,
    datetime: Attribute<SystemTime>,
    integer: Attribute<i64>,
    decimal: Attribute<f64>,
}

impl Step for PersistenceFirstStep {
    type Input = PersistenceModel;

    fn wait_for(&self, context: &mut Context, input: PersistenceModel) -> HandlerResult<Wait> {
        if self.keyword.get_required(context)? != "init-keyword"
            || self.full_text.get_required(context)? != "init-text"
            || self.data.get(context)?.is_some()
            || self.data_map.get_required(context, "one")? != 10
            || self.boolean.get_required(context)?
            || self.integer.get_required(context)? != 0
            || self.decimal.get_required(context)? != 2.1
            || self.datetime.get_required(context)? != input.datetime
        {
            return Err(HandlerError::new(
                "PersistenceFailure",
                "unexpected initial attributes",
            ));
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
        input: PersistenceModel,
    ) -> HandlerResult<StepDecision> {
        if self.integer.get_required(context)? != 1 {
            return Err(HandlerError::new(
                "PersistenceFailure",
                "wait_for integer write was not visible",
            ));
        }
        let data = self.data.get_required(context)?;
        if data.text != input.text || data.number != input.number {
            return Err(HandlerError::new(
                "PersistenceFailure",
                "wait_for data write was not visible",
            ));
        }
        self.datetime.set(context, data.datetime)?;
        self.boolean.set(context, true)?;
        Ok(StepDecision::go_to(&PersistenceSecondStep::new(), ()))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().wait_for_load_attribute_map(&self.data_map)
    }
}

struct PersistenceSecondStep {
    data: Attribute<PersistenceModel>,
    keyword: Attribute<String>,
    full_text: Attribute<String>,
    boolean: Attribute<bool>,
    datetime: Attribute<SystemTime>,
    decimal: Attribute<f64>,
}

impl PersistenceSecondStep {
    fn new() -> Self {
        Self {
            data: Attribute::new("data"),
            keyword: Attribute::new("keyword")
                .indexed(AttributeIndex::keyword().with_key(PersistenceWorkflow::KEYWORD_KEY)),
            full_text: Attribute::new("search-text")
                .indexed(AttributeIndex::full_text().with_key(PersistenceWorkflow::FULL_TEXT_KEY)),
            boolean: Attribute::new("bool")
                .indexed(AttributeIndex::bool().with_key(PersistenceWorkflow::BOOL_KEY)),
            datetime: Attribute::new("datetime")
                .indexed(AttributeIndex::date_time().with_key(PersistenceWorkflow::DATETIME_KEY)),
            decimal: Attribute::new("double")
                .indexed(AttributeIndex::double().with_key(PersistenceWorkflow::DOUBLE_KEY)),
        }
    }
}

impl Step for PersistenceSecondStep {
    type Input = ();

    fn wait_for(&self, context: &mut Context, (): ()) -> HandlerResult<Wait> {
        let data = self.data.get_required(context)?;
        if self.datetime.get_required(context)? != data.datetime
            || !self.boolean.get_required(context)?
        {
            return Err(HandlerError::new(
                "PersistenceFailure",
                "persisted values did not round trip",
            ));
        }
        self.decimal.set(context, 1.0)?;
        self.full_text.set(context, "Hail Dex!".to_string())?;
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        if self.full_text.get_required(context)? != "Hail Dex!" {
            return Err(HandlerError::new(
                "PersistenceFailure",
                "unexpected persisted text",
            ));
        }
        self.keyword.set(context, "Dex".to_string())?;
        Ok(StepDecision::graceful_complete("done".to_string()))
    }
}

#[test]
#[ignore = "requires dexcli dev"]
fn persistence_contract_round_trips_all_values_and_search_index() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(PersistenceWorkflow::new()));
    let workflow = PersistenceWorkflow::new();
    let flow_id = flow_id("go-persistence");
    let datetime = SystemTime::now();
    let number = datetime
        .duration_since(SystemTime::UNIX_EPOCH)
        .expect("current time after epoch")
        .as_nanos() as i64;
    let input = PersistenceModel {
        number,
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
            .wait_for_flow_with_timeout(&flow_id, Duration::from_secs(30))
            .and_then(|result| result.single_output::<String>())
            .expect("complete Go persistence compatibility Flow")
    );

    assert_eq!(
        Some(PersistenceModel {
            number,
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
            .get_attribute_map_instance(&flow_id, &workflow.data_map, "one")
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

    let query = format!("{} = 'Dex'", PersistenceWorkflow::KEYWORD_KEY);
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
        std::thread::yield_now();
    }
}
