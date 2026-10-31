// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use std::collections::HashMap;
use std::path::Path;
use std::sync::{Arc, LazyLock};
use std::time::{Duration, Instant};

use dex_protocol::dex::flow_service_client::FlowServiceClient;
use dex_protocol::dex::{GetAttributesRequest, value};
use dex_sdk::{
    Attribute, AttributeMap, BlobCache, BlobCacheConfig, Client, ClientOptions, FlowStatus,
    Registry, SdkError, StopFlowOptions,
};
use tokio::runtime::Runtime;

use crate::get_attributes_workflow::{
    CLOSED_RUN_LARGE_STRING_VALUE, CLOSED_RUN_LARGE_VALUE, COUNT, EMPTY_LABEL, GetAttributesFlow,
    INITIAL_COUNT, INITIAL_LABEL, INITIAL_NOTE, LABEL, LABEL_AS_NUMBER, LARGE_STRING_VALUE,
    LARGE_VALUE, LabelAsNumberFlow, NOTE, QUANTITIES, ROTATED_SECRET_VALUE, SECRET, SECRET_VALUE,
    SET_QUANTITY_INSTANCE, UNSET_COUNT, UNSET_QUANTITY_INSTANCE, ZERO_COUNT,
    get_attributes_start_options,
};
use crate::support::{self, DexDevTestEnvironment};

const COUNT_NAME: &str = "get-attributes-count";
const LABEL_NAME: &str = "get-attributes-label";
const QUANTITIES_NAME: &str = "get-attributes-quantities";
const SECRET_NAME: &str = "get-attributes-secret";
const LARGE_VALUE_NAME: &str = "get-attributes-large-value";
const CLOSED_RUN_LARGE_VALUE_NAME: &str = "get-attributes-closed-run-large-value";
const UNREGISTERED_NAME: &str = "get-attributes-unregistered";

static UNREGISTERED: LazyLock<Attribute<i64>> = LazyLock::new(|| Attribute::new(UNREGISTERED_NAME));
static MISMATCHED_COUNT: LazyLock<Attribute<String>> = LazyLock::new(|| Attribute::new(COUNT_NAME));
static MISMATCHED_SECRET: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new(SECRET_NAME));
static MISMATCHED_QUANTITIES: LazyLock<AttributeMap<String>> =
    LazyLock::new(|| AttributeMap::new(QUANTITIES_NAME));

#[test]
#[ignore = "requires dexcli dev"]
fn get_attributes_reads_committed_values_and_rejects_invalid_reads() {
    let environment = DexDevTestEnvironment::start(Registry::new().register(GetAttributesFlow));
    let client = &environment.client;
    let flow_id = support::flow_id("rust-get-attributes");
    client
        .start_flow_with_options(
            &GetAttributesFlow,
            &flow_id,
            (),
            get_attributes_start_options(),
        )
        .expect("start GetAttributes Flow");
    let raw_kinds = raw_value_kinds(
        &flow_id,
        &[LARGE_VALUE_NAME, CLOSED_RUN_LARGE_VALUE_NAME, SECRET_NAME],
    );
    assert!(matches!(
        raw_kinds.get(LARGE_VALUE_NAME),
        Some(value::Kind::InternalBlobIdForStringValue(_))
    ));
    assert!(matches!(
        raw_kinds.get(CLOSED_RUN_LARGE_VALUE_NAME),
        Some(value::Kind::InternalBlobIdForStringValue(_))
    ));
    // Dex returns hard-delete content inline, even above the large-value threshold.
    assert_eq!(
        Some(&value::Kind::StringValue(SECRET_VALUE.clone())),
        raw_kinds.get(SECRET_NAME)
    );

    let (
        label,
        empty_label,
        count,
        zero_count,
        unset_count,
        note,
        set_quantity,
        unset_quantity,
        secret,
        large_value,
    ) = client
        .get_attributes(
            &flow_id,
            (
                &LABEL,
                &EMPTY_LABEL,
                &COUNT,
                &ZERO_COUNT,
                &UNSET_COUNT,
                &NOTE,
                &QUANTITIES.instance(SET_QUANTITY_INSTANCE),
                &QUANTITIES.instance(UNSET_QUANTITY_INSTANCE),
                &SECRET,
                &LARGE_VALUE,
            ),
        )
        .expect("read open-run Attributes");
    assert_eq!(Some(INITIAL_LABEL.to_string()), label);
    assert_eq!(Some(String::new()), empty_label);
    assert_eq!(Some(INITIAL_COUNT), count);
    assert_eq!(Some(0), zero_count);
    assert_eq!(None, unset_count);
    assert_eq!(Some(INITIAL_NOTE.clone()), note);
    assert_eq!(Some(0), set_quantity);
    assert_eq!(None, unset_quantity);
    assert_eq!(Some(SECRET_VALUE.clone()), secret);
    assert_eq!(Some(LARGE_STRING_VALUE.clone()), large_value);

    let duplicate = client
        .get_attributes(&flow_id, (&COUNT, &COUNT))
        .expect_err("duplicate reads are rejected");
    assert!(matches!(duplicate, SdkError::InvalidArgument { .. }));
    assert_names(&duplicate, &flow_id, COUNT_NAME);
    assert!(duplicate.to_string().contains("duplicate Attribute read"));
    let duplicate_instance = client
        .get_attributes(
            &flow_id,
            (
                &QUANTITIES.instance(SET_QUANTITY_INSTANCE),
                &QUANTITIES.instance(SET_QUANTITY_INSTANCE),
            ),
        )
        .expect_err("duplicate map instance reads are rejected");
    assert!(matches!(
        duplicate_instance,
        SdkError::InvalidArgument { .. }
    ));
    assert_names(
        &duplicate_instance,
        &flow_id,
        "get-attributes-quantities/line%201",
    );

    let invalid_instance = client
        .get_attributes(&flow_id, (&QUANTITIES.instance("line/1"),))
        .expect_err("map instance keys must not contain a slash");
    assert!(matches!(invalid_instance, SdkError::InvalidArgument { .. }));
    assert_names(&invalid_instance, &flow_id, QUANTITIES_NAME);
    for blank_flow_id in ["", " \t"] {
        let blank = client
            .get_attributes(blank_flow_id, (&COUNT,))
            .expect_err("the Flow ID is required");
        assert!(matches!(blank, SdkError::InvalidArgument { .. }));
    }

    let unregistered = client
        .get_attributes(&flow_id, (&UNREGISTERED,))
        .expect_err("unregistered definitions are rejected");
    assert!(matches!(unregistered, SdkError::FlowDefinition { .. }));
    assert_names(&unregistered, &flow_id, UNREGISTERED_NAME);
    for mismatched in [
        client
            .get_attributes(&flow_id, (&MISMATCHED_COUNT,))
            .map(|_| ()),
        client
            .get_attributes(&flow_id, (&MISMATCHED_SECRET,))
            .map(|_| ()),
        client
            .get_attributes(&flow_id, (&MISMATCHED_QUANTITIES.instance("line-1"),))
            .map(|_| ()),
    ] {
        let mismatched = mismatched.expect_err("mismatched definitions are rejected");
        assert!(matches!(mismatched, SdkError::FlowDefinition { .. }));
        assert!(mismatched.to_string().contains(&flow_id), "{mismatched}");
        assert!(
            mismatched
                .to_string()
                .contains("differs from the definition"),
            "{mismatched}"
        );
    }

    let missing_flow_id = support::flow_id("rust-get-attributes-missing");
    let missing = client
        .get_attributes(&missing_flow_id, (&COUNT,))
        .expect_err("a missing Flow is rejected");
    assert!(matches!(missing, SdkError::FlowNotFound { .. }));
    assert_names(&missing, &missing_flow_id, COUNT_NAME);
    let missing_service = missing
        .service_error()
        .expect("FlowNotFound has service metadata");
    assert_eq!("get_attributes", missing_service.operation());
    assert_eq!(Some(missing_flow_id.as_str()), missing_service.flow_id());

    let deleted_flow_id = support::flow_id("rust-get-attributes-deleted-secret");
    client
        .start_flow_with_options(
            &GetAttributesFlow,
            &deleted_flow_id,
            (),
            get_attributes_start_options(),
        )
        .expect("start hard-delete GetAttributes Flow");
    client
        .invoke_rpc(
            &deleted_flow_id,
            GetAttributesFlow::ROTATE_SECRET,
            ROTATED_SECRET_VALUE.clone(),
        )
        .expect("rotate the hard-delete value");
    let (rotated_secret,) = client
        .get_attributes(&deleted_flow_id, (&SECRET,))
        .expect("read the rotated hard-delete value");
    assert_eq!(Some(ROTATED_SECRET_VALUE.clone()), rotated_secret);
    client
        .invoke_rpc_without_input(&deleted_flow_id, GetAttributesFlow::DELETE_SECRET)
        .expect("delete the hard-delete value");
    let (deleted_secret, label) = client
        .get_attributes(&deleted_flow_id, (&SECRET, &LABEL))
        .expect("read the deleted hard-delete value");
    assert_eq!(None, deleted_secret);
    assert_eq!(Some(INITIAL_LABEL.to_string()), label);
    client
        .stop_flow(&deleted_flow_id, StopFlowOptions::cancel())
        .expect("stop hard-delete GetAttributes Flow");

    client
        .stop_flow(&flow_id, StopFlowOptions::cancel())
        .expect("stop GetAttributes Flow");
    wait_until_closed(client, &flow_id);
    let fresh_cache_directory =
        tempfile::tempdir().expect("create fresh GetAttributes cache directory");
    let fresh_cache = Arc::new(
        BlobCache::open(
            BlobCacheConfig::new(fresh_cache_directory.path(), 64 * 1024 * 1024, 0)
                .expect("valid fresh GetAttributes cache config"),
        )
        .expect("open fresh GetAttributes cache"),
    );
    let fresh_client = Client::new(
        Registry::new()
            .register(GetAttributesFlow)
            .expect("register GetAttributes Flow"),
        Arc::clone(&fresh_cache),
        ClientOptions::new().server_address(server_address()),
    );
    let (closed_secret, closed_large_value, closed_unset_count) = fresh_client
        .get_attributes(&flow_id, (&SECRET, &CLOSED_RUN_LARGE_VALUE, &UNSET_COUNT))
        .expect("read closed-run Attributes with a fresh cache");
    drop(fresh_client);
    fresh_cache
        .close()
        .expect("close fresh GetAttributes cache");
    assert_eq!(Some(SECRET_VALUE.clone()), closed_secret);
    assert_eq!(
        Some(CLOSED_RUN_LARGE_STRING_VALUE.clone()),
        closed_large_value
    );
    assert_eq!(None, closed_unset_count);
    assert!(
        cache_contains(
            fresh_cache_directory.path(),
            CLOSED_RUN_LARGE_STRING_VALUE.as_bytes()
        ),
        "the closed-run large value was not loaded through LoadBlobs"
    );
    assert!(!cache_contains(
        fresh_cache_directory.path(),
        SECRET_VALUE.as_bytes()
    ));
}

#[test]
#[ignore = "requires dexcli dev"]
fn get_attributes_reports_value_mapping_for_a_same_named_definition_of_another_flow() {
    let environment = DexDevTestEnvironment::start(
        Registry::new()
            .register(GetAttributesFlow)
            .and_then(|registry| registry.register(LabelAsNumberFlow)),
    );
    let client = &environment.client;
    let flow_id = support::flow_id("rust-get-attributes-other-flow-definition");
    client
        .start_flow_with_options(
            &GetAttributesFlow,
            &flow_id,
            (),
            get_attributes_start_options(),
        )
        .expect("start GetAttributes Flow");

    let error = client
        .get_attributes(&flow_id, (&LABEL_AS_NUMBER,))
        .expect_err("a string label does not decode as i64");

    assert!(matches!(error, SdkError::ValueMapping { .. }), "{error:?}");
    assert_names(&error, &flow_id, LABEL_NAME);
    assert!(!error.to_string().contains(INITIAL_LABEL), "{error}");
    client
        .stop_flow(&flow_id, StopFlowOptions::cancel())
        .expect("stop GetAttributes Flow");
}

#[test]
#[ignore = "requires dexcli dev"]
fn get_attributes_reads_a_deleted_ordinary_attribute_as_unset() {
    let environment = DexDevTestEnvironment::start(Registry::new().register(GetAttributesFlow));
    let client = &environment.client;
    let flow_id = support::flow_id("rust-get-attributes-deleted-label");
    client
        .start_flow_with_options(
            &GetAttributesFlow,
            &flow_id,
            (),
            get_attributes_start_options(),
        )
        .expect("start GetAttributes Flow");
    let (label,) = client
        .get_attributes(&flow_id, (&LABEL,))
        .expect("read the set label");
    assert_eq!(Some(INITIAL_LABEL.to_string()), label);

    client
        .invoke_rpc_without_input(&flow_id, GetAttributesFlow::DELETE_LABEL)
        .expect("delete the label");
    let (label, count) = client
        .get_attributes(&flow_id, (&LABEL, &COUNT))
        .expect("read the deleted label");

    assert_eq!(None, label);
    assert_eq!(Some(INITIAL_COUNT), count);
    client
        .stop_flow(&flow_id, StopFlowOptions::cancel())
        .expect("stop GetAttributes Flow");
}

fn assert_names(error: &SdkError, flow_id: &str, attribute_name: &str) {
    let message = error.to_string();
    assert!(message.contains(flow_id), "{message}");
    assert!(message.contains(attribute_name), "{message}");
}

fn wait_until_closed(client: &Client, flow_id: &str) {
    let deadline = Instant::now() + Duration::from_secs(30);
    while Instant::now() < deadline {
        let status = client.describe_flow(flow_id).expect("describe Flow").status;
        if status != FlowStatus::Running {
            return;
        }
        std::thread::sleep(Duration::from_millis(50));
    }
    panic!("Flow {flow_id} did not close");
}

fn cache_contains(directory: &Path, expected: &[u8]) -> bool {
    std::fs::read_dir(directory)
        .expect("list cache directory")
        .map(|entry| entry.expect("read cache directory entry").path())
        .any(|path| {
            if path.is_dir() {
                cache_contains(&path, expected)
            } else {
                std::fs::read(&path)
                    .expect("read cache file")
                    .windows(expected.len())
                    .any(|window| window == expected)
            }
        })
}

fn server_address() -> String {
    std::env::var("DEX_SERVER_ADDRESS").unwrap_or_else(|_| "127.0.0.1:8801".to_string())
}

/// Returns the raw value kind of each set key, before the Client resolves large values.
fn raw_value_kinds(flow_id: &str, keys: &[&str]) -> HashMap<String, value::Kind> {
    let runtime = Runtime::new().expect("create FlowService runtime");
    runtime.block_on(async {
        let mut client = FlowServiceClient::connect(format!("http://{}", server_address()))
            .await
            .expect("connect FlowService");
        client
            .get_attributes(GetAttributesRequest {
                flow_id: flow_id.to_string(),
                run_id: String::new(),
                keys: keys.iter().map(|key| (*key).to_string()).collect(),
                all_keys: false,
            })
            .await
            .expect("GetAttributes")
            .into_inner()
            .attributes
            .into_iter()
            .map(|attribute| {
                let kind = attribute
                    .value
                    .and_then(|value| value.kind)
                    .expect("GetAttributes returns a value kind");
                (attribute.key, kind)
            })
            .collect()
    })
}
