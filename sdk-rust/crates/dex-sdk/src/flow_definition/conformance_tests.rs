// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::time::{Duration, SystemTime};

use dex_protocol::dex::{
    FlowDefinitionUploadResult as ProtoFlowDefinitionUploadResult, UploadFlowDefinitionsResponse,
};
use serde_json::Value as JsonValue;

use super::generated_at::parse_generated_at;
use super::source_path::{encode_json_string, relative_source_path, rewrite_source_path};
use super::{
    FlowDefinitionDocument, FlowDefinitionUploadOutcome, map_upload_results, upload_requests,
};

fn fixture() -> JsonValue {
    let path = concat!(
        env!("CARGO_MANIFEST_DIR"),
        "/../../../protos/conformance/flow_definition_upload.json"
    );
    serde_json::from_slice(&std::fs::read(path).expect("read conformance fixture"))
        .expect("parse conformance fixture")
}

fn cases(name: &str) -> Vec<JsonValue> {
    let cases = fixture()[name]
        .as_array()
        .expect("conformance case list")
        .clone();
    assert!(!cases.is_empty());
    cases
}

#[test]
fn rewrites_source_paths_like_every_sdk() {
    for case in cases("sourcePathRewrite") {
        assert_eq!(
            case["expected"].as_str(),
            relative_source_path(
                case["sourcePath"].as_str().expect("sourcePath"),
                case["cwd"].as_str()
            )
            .as_deref(),
            "{}",
            case["name"]
        );
    }
}

#[test]
fn encodes_json_strings_like_every_sdk() {
    for case in cases("jsonStringEncoding") {
        assert_eq!(
            case["encoded"].as_str().expect("encoded"),
            encode_json_string(case["value"].as_str().expect("value"))
        );
    }
}

#[test]
fn parses_generated_at_like_every_sdk() {
    for case in cases("generatedAt") {
        let value = case["value"].as_str().expect("value");
        let parsed = parse_generated_at(value);
        if case["error"].as_bool() == Some(true) {
            assert!(parsed.is_err(), "{value} must be rejected");
        } else {
            let millis = parsed
                .expect("valid generatedAt")
                .duration_since(SystemTime::UNIX_EPOCH)
                .expect("time after the epoch")
                .as_millis();
            assert_eq!(
                u128::from(case["unixMillis"].as_u64().expect("unixMillis")),
                millis,
                "{value}"
            );
        }
    }
}

#[test]
fn rewrites_only_the_source_path_bytes() {
    let content = "{ \"schemaVersion\":\"2.0\",\n \"source\" : {\"path\": \"/work/über/a.json\", \"language\":\"rust\"}, \"n\": 1.50 }";
    let rewritten = rewrite_source_path(content.as_bytes(), Some("/work"))
        .expect("valid JSON")
        .expect("absolute path is rewritten");
    assert_eq!(
        content
            .replace("/work/über/a.json", "über/a.json")
            .as_bytes(),
        rewritten.as_slice()
    );
    assert_eq!(
        None,
        rewrite_source_path(b"{\"source\":{\"path\":\"a/b.json\"}}", Some("/work"))
            .expect("valid JSON")
    );
}

#[test]
fn truncates_generated_at_to_milliseconds_and_maps_unknown_outcomes() {
    let generated_at = SystemTime::UNIX_EPOCH + Duration::new(1_793_491_200, 123_456_789);
    let requests = upload_requests(
        vec![FlowDefinitionDocument::new("OrderFlow", "{}", generated_at)],
        false,
    );
    let sent = requests[0].definitions[0]
        .generated_at
        .expect("generated_at is sent");
    assert_eq!(123_000_000, sent.nanos);
    assert!(requests[0].uploader.starts_with("sdk-rust/"));

    let results = map_upload_results(UploadFlowDefinitionsResponse {
        results: vec![ProtoFlowDefinitionUploadResult {
            flow_type: "OrderFlow".to_string(),
            outcome: 99,
            stored_digest: String::new(),
            stored_generated_at: None,
        }],
    });
    assert_eq!(FlowDefinitionUploadOutcome::Unknown, results[0].outcome());
}
