// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::LazyLock;
use std::time::{Duration, Instant, SystemTime};

use dex_sdk::{
    Attribute, AttributeIndex, AttributeMap, Context, Flow, HandlerError, HandlerResult,
    PersistenceSchema, Registry, StartFlowOptions, Step, StepDecision, StepList, StepOptions, Wait,
};

use crate::support::{DexDevTestEnvironment, flow_id};

static DATA: LazyLock<Attribute<PersistenceModel>> = LazyLock::new(|| Attribute::new("data"));
static TEXT: LazyLock<Attribute<String>> = LazyLock::new(|| Attribute::new("text"));
static DATA_MAP: LazyLock<AttributeMap<i32>> = LazyLock::new(|| AttributeMap::new("items"));
static KEYWORD: LazyLock<Attribute<String>> = LazyLock::new(|| {
    Attribute::new("keyword")
        .indexed(AttributeIndex::keyword().with_key(PersistenceWorkflow::KEYWORD_KEY))
});
static FULL_TEXT: LazyLock<Attribute<String>> = LazyLock::new(|| {
    Attribute::new("search-text")
        .indexed(AttributeIndex::full_text().with_key(PersistenceWorkflow::FULL_TEXT_KEY))
});
static BOOLEAN: LazyLock<Attribute<bool>> = LazyLock::new(|| {
    Attribute::new("bool").indexed(AttributeIndex::bool().with_key(PersistenceWorkflow::BOOL_KEY))
});
static DATETIME: LazyLock<Attribute<SystemTime>> = LazyLock::new(|| {
    Attribute::new("datetime")
        .indexed(AttributeIndex::date_time().with_key(PersistenceWorkflow::DATETIME_KEY))
});
static INTEGER: LazyLock<Attribute<i64>> = LazyLock::new(|| {
    Attribute::new("int").indexed(AttributeIndex::int().with_key(PersistenceWorkflow::INT_KEY))
});
static DECIMAL: LazyLock<Attribute<f64>> = LazyLock::new(|| {
    Attribute::new("double")
        .indexed(AttributeIndex::double().with_key(PersistenceWorkflow::DOUBLE_KEY))
});

#[derive(Debug, PartialEq, serde::Deserialize, serde::Serialize)]
struct PersistenceModel {
    number: i64,
    text: String,
    datetime: SystemTime,
}

struct PersistenceWorkflow {
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
        Self {
            first: PersistenceFirstStep,
            second: PersistenceSecondStep,
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
            .attribute(&DATA)
            .attribute(&TEXT)
            .attribute_map(&DATA_MAP)
            .attribute(&KEYWORD)
            .attribute(&FULL_TEXT)
            .attribute(&BOOLEAN)
            .attribute(&DATETIME)
            .attribute(&INTEGER)
            .attribute(&DECIMAL)
    }
}

struct PersistenceFirstStep;

impl Step for PersistenceFirstStep {
    type Input = PersistenceModel;

    fn wait_for(&self, context: &mut Context, input: PersistenceModel) -> HandlerResult<Wait> {
        if KEYWORD.get_required(context)? != "init-keyword"
            || FULL_TEXT.get_required(context)? != "init-text"
            || DATA.get(context)?.is_some()
            || DATA_MAP.get_required(context, "one")? != 10
            || BOOLEAN.get_required(context)?
            || INTEGER.get_required(context)? != 0
            || DECIMAL.get_required(context)? != 2.1
            || DATETIME.get_required(context)? != input.datetime
        {
            return Err(HandlerError::new(
                "PersistenceFailure",
                "unexpected initial attributes",
            ));
        }
        DATA.set(context, input)?;
        TEXT.set(context, "a string".to_string())?;
        DATA_MAP.set(context, "one", 11)?;
        INTEGER.set(context, 1)?;
        Ok(Wait::skip_immediately())
    }

    fn execute(
        &self,
        context: &mut Context,
        input: PersistenceModel,
    ) -> HandlerResult<StepDecision> {
        if INTEGER.get_required(context)? != 1 {
            return Err(HandlerError::new(
                "PersistenceFailure",
                "wait_for integer write was not visible",
            ));
        }
        let data = DATA.get_required(context)?;
        if data.text != input.text || data.number != input.number {
            return Err(HandlerError::new(
                "PersistenceFailure",
                "wait_for data write was not visible",
            ));
        }
        DATETIME.set(context, data.datetime)?;
        BOOLEAN.set(context, true)?;
        Ok(StepDecision::go_to(&PersistenceSecondStep, ()))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().wait_for_load_attribute_map(&DATA_MAP)
    }
}

struct PersistenceSecondStep;

impl Step for PersistenceSecondStep {
    type Input = ();

    fn wait_for(&self, context: &mut Context, (): ()) -> HandlerResult<Wait> {
        let data = DATA.get_required(context)?;
        if DATETIME.get_required(context)? != data.datetime || !BOOLEAN.get_required(context)? {
            return Err(HandlerError::new(
                "PersistenceFailure",
                "persisted values did not round trip",
            ));
        }
        DECIMAL.set(context, 1.0)?;
        FULL_TEXT.set(context, "Hail Dex!".to_string())?;
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        if FULL_TEXT.get_required(context)? != "Hail Dex!" {
            return Err(HandlerError::new(
                "PersistenceFailure",
                "unexpected persisted text",
            ));
        }
        KEYWORD.set(context, "Dex".to_string())?;
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
        .initial_attribute(&KEYWORD, "init-keyword".to_string())
        .initial_attribute(&FULL_TEXT, "init-text".to_string())
        .initial_attribute(&BOOLEAN, false)
        .initial_attribute(&DATETIME, datetime)
        .initial_attribute(&INTEGER, 0)
        .initial_attribute(&DECIMAL, 2.1)
        .initial_attribute_map(&DATA_MAP, "one", 10);
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
