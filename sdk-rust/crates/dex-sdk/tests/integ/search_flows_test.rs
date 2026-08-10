// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use std::time::{Duration, Instant};

use dex_sdk::{FlowStatus, IdReusePolicy, Registry, SdkError, StartFlowOptions};
use serde_json::Value as JsonValue;

use crate::search_flows_workflow::SearchFlowsWorkflow;
use crate::support::{DexDevTestEnvironment, flow_id};

#[test]
#[ignore = "requires dexcli dev"]
fn test_search_flows_finds_indexed_flow() {
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
        std::thread::yield_now();
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
fn test_search_flows_rejects_negative_page_size() {
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
