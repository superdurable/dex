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

use dex_sdk::{Client, Registry, SdkError, SdkResult, StartFlowOptions};

use crate::persistence_set_attributes_workflow::PersistenceSetAttributesWorkflow;
use crate::persistence_workflow::{PersistenceModel, PersistenceWorkflow};
use crate::support::{DexDevTestEnvironment, flow_id};

#[test]
#[ignore = "requires dexcli dev"]
fn test_persistence_reads() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(PersistenceWorkflow::new()));
    let workflow = PersistenceWorkflow::new();
    assert!(matches!(
        environment
            .client
            .get_attribute(&flow_id("missing"), &workflow.data),
        Err(SdkError::FlowNotFound { .. })
    ));
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
    assert!(matches!(
        environment
            .client
            .set_attribute(&flow_id, &workflow.data, "closed".to_string()),
        Err(SdkError::FlowNotActive { .. })
    ));
}

#[test]
#[ignore = "requires dexcli dev"]
fn test_set_search_attributes() {
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
fn test_set_data_attributes() {
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

#[allow(dead_code)]
fn compile_persistence_reads(client: &Client) -> SdkResult<()> {
    let workflow = PersistenceWorkflow::new();
    let options = StartFlowOptions::new()
        .initial_attribute(&workflow.initial, "initial".to_string())
        .initial_attribute_map(&workflow.data_map, "one", "initial".to_string());
    client.start_flow_with_options(&workflow, "persistence", "input".to_string(), options)?;
    let _: Option<String> = client.get_attribute("persistence", &workflow.data)?;
    let _: Option<i32> = client.get_attribute("persistence", &workflow.integer)?;
    let _: Option<SystemTime> = client.get_attribute("persistence", &workflow.datetime)?;
    Ok(())
}

#[allow(dead_code)]
fn compile_persistence_writes(client: &Client) -> SdkResult<()> {
    let workflow = PersistenceSetAttributesWorkflow::new();
    client.start_flow(&workflow, "set-attributes", "input".to_string())?;
    client.set_attribute("set-attributes", &workflow.data, "value".to_string())?;
    client.set_attribute_map(
        "set-attributes",
        &workflow.data_map,
        "one",
        "value".to_string(),
    )?;
    client.set_attribute("set-attributes", &workflow.keyword, "keyword".to_string())?;
    client.set_attribute("set-attributes", &workflow.decimal, 1.5)?;
    client.set_attribute("set-attributes", &workflow.integer, 1)?;
    client.set_attribute("set-attributes", &workflow.boolean, true)?;
    client.set_attribute(
        "set-attributes",
        &workflow.keywords,
        vec!["one".to_string(), "two".to_string()],
    )?;
    let _: String = client.wait_for_flow("set-attributes")?;
    Ok(())
}
