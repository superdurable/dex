// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeSet;

use dexzd_protocol::dex::{ErrorSubStatus, FlowDefinitionUpload, UploadFlowDefinitionsRequest};
use serde::Deserialize;
use tonic::Code;

use super::FlowDefinitionsConfig;
use super::flow_definition_digest::FlowDefinitionDigest;
use super::flow_definition_store::encoded_flow_type_exceeds_key_limit;
use crate::FlowOperationError;

/// Most items in one Upload or Get request.
pub(super) const MAX_REQUEST_ITEMS: usize = 256;
/// Most bytes of `UploadFlowDefinitionsRequest.uploader`.
pub(super) const MAX_UPLOADER_BYTES: usize = 128;
/// Deepest accepted nesting of JSON objects and arrays in a definition.
pub(super) const MAX_JSON_NESTING_DEPTH: usize = 128;
/// How far after the server clock an uploaded `generated_at` may be.
const MAX_FUTURE_GENERATED_AT_MICROS: i64 = 24 * 60 * 60 * 1_000_000;
const MICROS_PER_SECOND: i64 = 1_000_000;
const NANOS_PER_MICRO: i32 = 1_000;
const NANOS_PER_SECOND: i32 = 1_000_000_000;

/// One upload item that passed validation.
#[derive(Debug, Clone)]
pub(super) struct ValidatedUpload {
    pub(super) flow_type: String,
    pub(super) content: Vec<u8>,
    pub(super) digest: FlowDefinitionDigest,
    pub(super) generated_at_unix_micros: i64,
}

/// Upload items that passed validation and the uploader that stores them.
#[derive(Debug, Clone)]
pub(super) struct ValidatedUploadRequest {
    pub(super) uploads: Vec<ValidatedUpload>,
    pub(super) uploader: String,
    /// Replaces a different stored document whatever its generated_at.
    pub(super) is_forced: bool,
}

/// The fields of an FDG document that the server checks; serde skips every other field.
#[derive(Deserialize)]
struct FlowDefinitionHeader {
    #[serde(rename = "schemaVersion")]
    schema_version: String,
    flow: FlowDefinitionFlowHeader,
}

#[derive(Deserialize)]
struct FlowDefinitionFlowHeader {
    name: String,
}

/// Validates every item of an untrusted upload request; any failure rejects the whole request.
pub(super) fn validate_upload_request(
    config: &FlowDefinitionsConfig,
    request: UploadFlowDefinitionsRequest,
    now_unix_micros: i64,
) -> Result<ValidatedUploadRequest, FlowOperationError> {
    if request.definitions.len() > MAX_REQUEST_ITEMS {
        return Err(invalid_flow_definition(format!(
            "an upload holds {} Flow definitions, above the limit of {MAX_REQUEST_ITEMS}",
            request.definitions.len()
        )));
    }
    if request.uploader.len() > MAX_UPLOADER_BYTES {
        return Err(invalid_flow_definition(format!(
            "uploader is {} bytes, above the limit of {MAX_UPLOADER_BYTES}",
            request.uploader.len()
        )));
    }
    let mut flow_types = BTreeSet::new();
    let mut uploads = Vec::with_capacity(request.definitions.len());
    for definition in request.definitions {
        let upload = validate_upload(config, definition, now_unix_micros)?;
        if !flow_types.insert(upload.flow_type.clone()) {
            return Err(invalid_flow_definition(format!(
                "Flow type {:?} appears more than once in the upload",
                upload.flow_type
            )));
        }
        uploads.push(upload);
    }
    Ok(ValidatedUploadRequest {
        uploads,
        uploader: request.uploader,
        is_forced: request.force,
    })
}

/// Validates the Flow types of a Get request.
pub(super) fn validate_requested_flow_types(
    config: &FlowDefinitionsConfig,
    flow_types: &[String],
) -> Result<(), FlowOperationError> {
    if flow_types.is_empty() {
        return Err(invalid_flow_definition("the request names no Flow types"));
    }
    if flow_types.len() > MAX_REQUEST_ITEMS {
        return Err(invalid_flow_definition(format!(
            "the request names {} Flow types, above the limit of {MAX_REQUEST_ITEMS}",
            flow_types.len()
        )));
    }
    for flow_type in flow_types {
        validate_flow_type(config, flow_type)?;
    }
    Ok(())
}

/// Returns `InvalidArgument` + `FLOW_DEFINITION_INVALID`.
pub(super) fn invalid_flow_definition(message: impl Into<String>) -> FlowOperationError {
    FlowOperationError::rejected(
        Code::InvalidArgument,
        ErrorSubStatus::FlowDefinitionInvalid,
        message,
    )
}

fn validate_upload(
    config: &FlowDefinitionsConfig,
    definition: FlowDefinitionUpload,
    now_unix_micros: i64,
) -> Result<ValidatedUpload, FlowOperationError> {
    let flow_type = definition.flow_type;
    validate_flow_type(config, &flow_type)?;
    let Some(generated_at) = definition.generated_at else {
        return Err(invalid_flow_definition(format!(
            "Flow definition {flow_type:?} has no generated_at"
        )));
    };
    let generated_at_unix_micros = timestamp_unix_micros(&generated_at).ok_or_else(|| {
        invalid_flow_definition(format!(
            "Flow definition {flow_type:?} has an out-of-range generated_at"
        ))
    })?;
    if generated_at_unix_micros > now_unix_micros.saturating_add(MAX_FUTURE_GENERATED_AT_MICROS) {
        return Err(invalid_flow_definition(format!(
            "Flow definition {flow_type:?} has a generated_at more than 24 hours after the server clock"
        )));
    }
    let content = definition.content;
    if content.len() > config.max_definition_bytes {
        return Err(invalid_flow_definition(format!(
            "Flow definition {flow_type:?} is {} bytes, above the limit of {}",
            content.len(),
            config.max_definition_bytes
        )));
    }
    validate_content(&flow_type, &content)?;
    Ok(ValidatedUpload {
        digest: FlowDefinitionDigest::of_content(&content),
        flow_type,
        content,
        generated_at_unix_micros,
    })
}

fn validate_flow_type(
    config: &FlowDefinitionsConfig,
    flow_type: &str,
) -> Result<(), FlowOperationError> {
    if flow_type.is_empty() {
        return Err(invalid_flow_definition("Flow type must not be empty"));
    }
    if flow_type.len() > config.max_flow_type_length {
        return Err(invalid_flow_definition(format!(
            "Flow type {flow_type:?} is {} bytes, above the limit of {}",
            flow_type.len(),
            config.max_flow_type_length
        )));
    }
    if encoded_flow_type_exceeds_key_limit(flow_type) {
        return Err(invalid_flow_definition(format!(
            "Flow type {flow_type:?} is too long for an object key once percent-encoded"
        )));
    }
    Ok(())
}

fn validate_content(flow_type: &str, content: &[u8]) -> Result<(), FlowOperationError> {
    let first_byte = content
        .iter()
        .find(|byte| !is_json_whitespace(**byte))
        .copied();
    if first_byte != Some(b'{') {
        return Err(invalid_flow_definition(format!(
            "Flow definition {flow_type:?} is not a JSON object"
        )));
    }
    if json_nesting_depth_exceeds(content, MAX_JSON_NESTING_DEPTH) {
        return Err(invalid_flow_definition(format!(
            "Flow definition {flow_type:?} nests JSON deeper than {MAX_JSON_NESTING_DEPTH} levels"
        )));
    }
    let header: FlowDefinitionHeader = serde_json::from_slice(content).map_err(|error| {
        invalid_flow_definition(format!(
            "Flow definition {flow_type:?} is not a valid FDG JSON document: {error}"
        ))
    })?;
    if !header.schema_version.starts_with("2.") {
        return Err(invalid_flow_definition(format!(
            "Flow definition {flow_type:?} has schemaVersion {:?}; only 2.x is accepted",
            header.schema_version
        )));
    }
    if header.flow.name != flow_type {
        return Err(invalid_flow_definition(format!(
            "Flow definition {flow_type:?} names flow {:?}; flow.name must equal the Flow type",
            header.flow.name
        )));
    }
    Ok(())
}

/// Reports whether objects and arrays nest deeper than `max_depth`, ignoring brackets in strings.
fn json_nesting_depth_exceeds(content: &[u8], max_depth: usize) -> bool {
    let mut depth = 0_usize;
    let mut is_in_string = false;
    let mut is_escaped = false;
    for &byte in content {
        if is_in_string {
            if is_escaped {
                is_escaped = false;
            } else if byte == b'\\' {
                is_escaped = true;
            } else if byte == b'"' {
                is_in_string = false;
            }
            continue;
        }
        match byte {
            b'"' => is_in_string = true,
            b'{' | b'[' => {
                depth += 1;
                if depth > max_depth {
                    return true;
                }
            }
            b'}' | b']' => depth = depth.saturating_sub(1),
            _ => {}
        }
    }
    false
}

fn is_json_whitespace(byte: u8) -> bool {
    matches!(byte, b' ' | b'\t' | b'\n' | b'\r')
}

/// Converts an untrusted timestamp to microseconds, truncating nanoseconds; `None` when invalid.
fn timestamp_unix_micros(timestamp: &prost_types::Timestamp) -> Option<i64> {
    if !(0..NANOS_PER_SECOND).contains(&timestamp.nanos) {
        return None;
    }
    timestamp
        .seconds
        .checked_mul(MICROS_PER_SECOND)?
        .checked_add(i64::from(timestamp.nanos / NANOS_PER_MICRO))
}

/// Converts stored microseconds since the Unix epoch to a timestamp.
pub(super) fn unix_micros_timestamp(unix_micros: i64) -> prost_types::Timestamp {
    let nanos = i32::try_from(unix_micros.rem_euclid(MICROS_PER_SECOND))
        .expect("a remainder of one second in microseconds fits in i32")
        * NANOS_PER_MICRO;
    prost_types::Timestamp {
        seconds: unix_micros.div_euclid(MICROS_PER_SECOND),
        nanos,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn nested(depth: usize) -> Vec<u8> {
        let mut content = String::from(r#"{"schemaVersion":"2.0","flow":{"name":"F"},"x":"#);
        for _ in 1..depth {
            content.push('[');
        }
        for _ in 1..depth {
            content.push(']');
        }
        content.push('}');
        content.into_bytes()
    }

    #[test]
    fn nesting_depth_counts_objects_and_arrays_outside_strings() {
        assert!(!json_nesting_depth_exceeds(&nested(128), 128));
        assert!(json_nesting_depth_exceeds(&nested(129), 128));
        assert!(!json_nesting_depth_exceeds(br#"{"a":"[[[[\"[[["}"#, 1));
    }

    #[test]
    fn content_rules_reject_arrays_wrong_schema_and_name_mismatch() {
        assert!(validate_content("F", br#"{"schemaVersion":"2.0","flow":{"name":"F"}}"#).is_ok());
        assert!(validate_content("F", br#" [{"schemaVersion":"2.0"}]"#).is_err());
        assert!(validate_content("F", br#"{"schemaVersion":"1.0","flow":{"name":"F"}}"#).is_err());
        assert!(validate_content("F", br#"{"schemaVersion":"2.0","flow":{"name":"G"}}"#).is_err());
        assert!(validate_content("F", br#"{"schemaVersion":"2.0","flow":{"name":"F"}"#).is_err());
        assert!(validate_content("F", &nested(129)).is_err());
    }

    #[test]
    fn timestamps_convert_at_microsecond_precision() {
        let timestamp = prost_types::Timestamp {
            seconds: -2,
            nanos: 999_999_999,
        };
        let micros = timestamp_unix_micros(&timestamp).unwrap();
        assert_eq!(micros, -1_000_001);
        assert_eq!(
            unix_micros_timestamp(micros),
            prost_types::Timestamp {
                seconds: -2,
                nanos: 999_999_000
            }
        );
        assert!(
            timestamp_unix_micros(&prost_types::Timestamp {
                seconds: 0,
                nanos: -1
            })
            .is_none()
        );
        assert!(
            timestamp_unix_micros(&prost_types::Timestamp {
                seconds: i64::MAX,
                nanos: 0
            })
            .is_none()
        );
    }
}
