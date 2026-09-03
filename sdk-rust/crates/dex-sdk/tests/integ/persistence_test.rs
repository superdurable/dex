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

use dex_sdk::{Attribute, Client, Registry, SdkError, SdkResult, StartFlowOptions};

use crate::persistence_set_attributes_workflow::{
    self as set_attributes, PersistenceSetAttributesWorkflow,
};
use crate::persistence_workflow::{self as persistence, PersistenceModel, PersistenceWorkflow};
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
            .get_attribute(&flow_id("missing"), &persistence::DATA),
        Err(SdkError::FlowNotFound { .. })
    ));
    let flow_id = flow_id("persistence");
    let options = StartFlowOptions::new()
        .initial_attribute(&persistence::INITIAL, "initial".to_string())
        .initial_attribute_map(&workflow.data_map, "one", "initial".to_string());
    environment
        .client
        .start_flow_with_options(&workflow, &flow_id, "input".to_string(), options)
        .expect("start persistence Flow");
    assert_eq!(
        "input",
        environment
            .client
            .wait_for_flow_with_timeout(&flow_id, Duration::from_secs(30))
            .and_then(|result| result.single_output::<String>())
            .expect("complete persistence Flow")
    );
    assert_eq!(
        Some("input".to_string()),
        environment
            .client
            .get_attribute(&flow_id, &persistence::DATA)
            .expect("get data Attribute")
    );
    assert_eq!(
        Some("initial".to_string()),
        environment
            .client
            .get_attribute(&flow_id, &persistence::INITIAL)
            .expect("get initial Attribute")
    );
    assert_eq!(
        None,
        environment
            .client
            .get_attribute_map_instance(&flow_id, &workflow.data_map, "one")
            .expect("get deleted AttributeMap entry")
    );
    assert_eq!(
        Some("input".to_string()),
        environment
            .client
            .get_attribute(&flow_id, &persistence::KEYWORD)
            .expect("get keyword Attribute")
    );
    assert_eq!(
        Some(1),
        environment
            .client
            .get_attribute(&flow_id, &persistence::INTEGER)
            .expect("get integer Attribute")
    );
    assert_eq!(
        Some(SystemTime::UNIX_EPOCH + Duration::from_secs(1_681_766_269)),
        environment
            .client
            .get_attribute(&flow_id, &persistence::DATETIME)
            .expect("get datetime Attribute")
    );
    assert_eq!(
        Some(PersistenceModel { value: 0 }),
        environment
            .client
            .get_attribute(&flow_id, &persistence::MODEL)
            .expect("get model Attribute")
    );
    assert!(matches!(
        environment
            .client
            .set_attribute(&flow_id, &persistence::DATA, "closed".to_string()),
        Err(SdkError::FlowNotActive { .. })
    ));
}

#[test]
#[ignore = "requires dexcli dev"]
fn test_set_indexed_attributes() {
    let environment = DexDevTestEnvironment::start(
        Registry::new().register(PersistenceSetAttributesWorkflow::new()),
    );
    let workflow = PersistenceSetAttributesWorkflow::new();
    let flow_id = flow_id("set-indexed-attributes");
    let keywords = vec!["keyword-1".to_string(), "keyword-2".to_string()];
    let datetime = SystemTime::UNIX_EPOCH + Duration::new(1_731_456_001, 731_455_544);
    environment
        .client
        .start_flow(&workflow, &flow_id, "start".to_string())
        .expect("start set-indexed-attributes Flow");
    environment
        .client
        .set_attribute(&flow_id, &set_attributes::KEYWORD, "keyword-1".to_string())
        .expect("set keyword Attribute");
    environment
        .client
        .set_attribute(&flow_id, &set_attributes::FULL_TEXT, "text-1".to_string())
        .expect("set full-text Attribute");
    environment
        .client
        .set_attribute(&flow_id, &set_attributes::DECIMAL, 1.0)
        .expect("set double Attribute");
    environment
        .client
        .set_attribute(&flow_id, &set_attributes::INTEGER, 1)
        .expect("set integer Attribute");
    environment
        .client
        .set_attribute(&flow_id, &set_attributes::BOOLEAN, true)
        .expect("set boolean Attribute");
    environment
        .client
        .set_attribute(&flow_id, &set_attributes::KEYWORDS, keywords.clone())
        .expect("set keyword-array Attribute");
    environment
        .client
        .set_attribute(&flow_id, &set_attributes::DATETIME, datetime)
        .expect("set datetime Attribute");
    environment
        .client
        .publish(&flow_id, &set_attributes::PROCEED, ())
        .expect("publish proceed message");
    assert_eq!(
        "test-result",
        environment
            .client
            .wait_for_flow_with_timeout(&flow_id, Duration::from_secs(30))
            .and_then(|result| result.single_output::<String>())
            .expect("complete set-indexed-attributes Flow")
    );
    assert_eq!(
        Some("keyword-1".to_string()),
        environment
            .client
            .get_attribute(&flow_id, &set_attributes::KEYWORD)
            .expect("get keyword Attribute")
    );
    assert_eq!(
        Some("text-1".to_string()),
        environment
            .client
            .get_attribute(&flow_id, &set_attributes::FULL_TEXT)
            .expect("get full-text Attribute")
    );
    assert_eq!(
        Some(1.0),
        environment
            .client
            .get_attribute(&flow_id, &set_attributes::DECIMAL)
            .expect("get double Attribute")
    );
    assert_eq!(
        Some(1),
        environment
            .client
            .get_attribute(&flow_id, &set_attributes::INTEGER)
            .expect("get integer Attribute")
    );
    assert_eq!(
        Some(true),
        environment
            .client
            .get_attribute(&flow_id, &set_attributes::BOOLEAN)
            .expect("get boolean Attribute")
    );
    assert_eq!(
        Some(keywords),
        environment
            .client
            .get_attribute(&flow_id, &set_attributes::KEYWORDS)
            .expect("get keyword-array Attribute")
    );
    assert_eq!(
        Some(datetime),
        environment
            .client
            .get_attribute(&flow_id, &set_attributes::DATETIME)
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
    assert!(matches!(
        environment.client.wait_for_attribute_equal(
            &flow_id,
            &set_attributes::DATA,
            "never".to_string(),
            Duration::from_secs(1),
        ),
        Err(SdkError::LongPollTimeout { .. })
    ));
    std::thread::scope(|scope| {
        let waiting = scope.spawn(|| {
            environment.client.wait_for_attribute_equal(
                &flow_id,
                &set_attributes::DATA,
                "query-start".to_string(),
                Duration::from_secs(30),
            )
        });
        environment
            .client
            .set_attribute(&flow_id, &set_attributes::DATA, "query-start".to_string())
            .expect("set data Attribute");
        waiting
            .join()
            .expect("join Attribute wait")
            .expect("wait for data Attribute");
    });
    std::thread::scope(|scope| {
        let waiting = scope.spawn(|| {
            environment.client.wait_for_attribute_map_instance_equal(
                &flow_id,
                &workflow.data_map,
                "special % key",
                "mapped-value".to_string(),
                Duration::from_secs(30),
            )
        });
        environment
            .client
            .set_attribute_map_instance(
                &flow_id,
                &workflow.data_map,
                "special % key",
                "mapped-value".to_string(),
            )
            .expect("set special AttributeMap entry");
        waiting
            .join()
            .expect("join AttributeMap wait")
            .expect("wait for AttributeMap entry");
    });
    assert!(matches!(
        environment.client.wait_for_attribute_equal(
            &flow_id,
            &set_attributes::MODEL,
            PersistenceModel { value: 8 },
            Duration::from_secs(30),
        ),
        Err(SdkError::InvalidArgument { .. })
    ));
    assert!(matches!(
        environment.client.wait_for_attribute_equal(
            &flow_id,
            &Attribute::<Vec<u8>>::new("bytes"),
            vec![1],
            Duration::from_secs(30),
        ),
        Err(SdkError::InvalidArgument { .. })
    ));
    assert!(matches!(
        environment.client.wait_for_attribute_equal(
            &flow_id,
            &Attribute::<()>::new("null"),
            (),
            Duration::from_secs(30),
        ),
        Err(SdkError::InvalidArgument { .. })
    ));
    environment
        .client
        .set_attribute_map_instance(
            &flow_id,
            &workflow.data_map,
            "one",
            "mapped-value".to_string(),
        )
        .expect("set AttributeMap entry");
    environment
        .client
        .set_attribute(
            &flow_id,
            &set_attributes::MODEL,
            PersistenceModel { value: 7 },
        )
        .expect("set model Attribute");
    environment
        .client
        .publish(&flow_id, &set_attributes::PROCEED, ())
        .expect("publish proceed message");
    assert_eq!(
        "test-result",
        environment
            .client
            .wait_for_flow_with_timeout(&flow_id, Duration::from_secs(30))
            .and_then(|result| result.single_output::<String>())
            .expect("complete set-data-attributes Flow")
    );
    assert_eq!(
        Some("query-start".to_string()),
        environment
            .client
            .get_attribute(&flow_id, &set_attributes::DATA)
            .expect("get data Attribute")
    );
    assert_eq!(
        Some("mapped-value".to_string()),
        environment
            .client
            .get_attribute_map_instance(&flow_id, &workflow.data_map, "one")
            .expect("get AttributeMap entry")
    );
    assert_eq!(
        Some(PersistenceModel { value: 7 }),
        environment
            .client
            .get_attribute(&flow_id, &set_attributes::MODEL)
            .expect("get model Attribute")
    );
}

#[allow(dead_code)]
fn compile_persistence_reads(client: &Client) -> SdkResult<()> {
    let workflow = PersistenceWorkflow::new();
    let options = StartFlowOptions::new()
        .initial_attribute(&persistence::INITIAL, "initial".to_string())
        .initial_attribute_map(&workflow.data_map, "one", "initial".to_string());
    client.start_flow_with_options(&workflow, "persistence", "input".to_string(), options)?;
    let _: Option<String> = client.get_attribute("persistence", &persistence::DATA)?;
    let _: Option<String> =
        client.get_attribute_map_instance("persistence", &workflow.data_map, "one")?;
    let _: Option<i32> = client.get_attribute("persistence", &persistence::INTEGER)?;
    let _: Option<SystemTime> = client.get_attribute("persistence", &persistence::DATETIME)?;
    Ok(())
}

#[allow(dead_code)]
fn compile_persistence_writes(client: &Client) -> SdkResult<()> {
    let workflow = PersistenceSetAttributesWorkflow::new();
    client.start_flow(&workflow, "set-attributes", "input".to_string())?;
    client.set_attribute("set-attributes", &set_attributes::DATA, "value".to_string())?;
    client.set_attribute_map_instance(
        "set-attributes",
        &workflow.data_map,
        "one",
        "value".to_string(),
    )?;
    client.set_attribute(
        "set-attributes",
        &set_attributes::KEYWORD,
        "keyword".to_string(),
    )?;
    client.set_attribute("set-attributes", &set_attributes::DECIMAL, 1.5)?;
    client.set_attribute("set-attributes", &set_attributes::INTEGER, 1)?;
    client.set_attribute("set-attributes", &set_attributes::BOOLEAN, true)?;
    client.set_attribute(
        "set-attributes",
        &set_attributes::KEYWORDS,
        vec!["one".to_string(), "two".to_string()],
    )?;
    client.wait_for_attribute_equal(
        "set-attributes",
        &set_attributes::DATA,
        "value".to_string(),
        Duration::from_secs(30),
    )?;
    client.wait_for_attribute_map_instance_equal(
        "set-attributes",
        &workflow.data_map,
        "one",
        "value".to_string(),
        Duration::from_secs(30),
    )?;
    let _: String = client.wait_for_flow("set-attributes")?.single_output()?;
    Ok(())
}
