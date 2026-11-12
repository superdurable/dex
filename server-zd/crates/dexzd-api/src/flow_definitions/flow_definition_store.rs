// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use base64::Engine as _;
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use dexzd_keyspace::TenantId;
use dexzd_object_store::{
    ByteRange, FLOW_DEFINITION_OBJECT_KEY_PREFIX, ObjectStore, ObjectStoreError,
};
use dexzd_protocol::dexzd::v1::FlowDefinitionRecord;
use prost::Message;

use super::flow_definition_digest::FlowDefinitionDigest;
use super::upload_validation::MAX_UPLOADER_BYTES;
use crate::FlowOperationError;

/// Longest full object key, including the store prefix; S3 keys hold at most 1024 bytes.
const MAX_OBJECT_KEY_BYTES: usize = 1024;
/// Longest key segment a page token carries; above any key S3 (1 KiB) or db9 indexes (under 3 KiB) hold.
pub(super) const MAX_PAGE_TOKEN_SEGMENT_BYTES: usize = 64 * 1024;

/// One stored record, checked against its key, with the ETag that conditional replacement needs.
#[derive(Debug, Clone)]
pub(super) struct StoredFlowDefinition {
    pub(super) record: FlowDefinitionRecord,
    pub(super) digest: FlowDefinitionDigest,
    pub(super) etag: String,
}

/// What a size-capped read of a record object found.
#[derive(Debug)]
pub(super) enum FlowDefinitionRead {
    /// No definition is stored.
    Missing,
    /// The stored record.
    Stored(StoredFlowDefinition),
    /// The object is larger than the read cap; nothing was decoded.
    TooLarge {
        /// Size of the whole object.
        object_size: u64,
    },
}

/// Returns `registry/flow-definitions/<tenant_id>/types/`, the prefix of every record of a tenant.
pub(super) fn flow_definition_types_prefix(tenant_id: TenantId) -> String {
    format!("{FLOW_DEFINITION_OBJECT_KEY_PREFIX}{}/types/", tenant_id.0)
}

/// Returns the object key of the record of `flow_type`.
pub(super) fn flow_definition_key(tenant_id: TenantId, flow_type: &str) -> String {
    let mut key = flow_definition_types_prefix(tenant_id);
    key.push_str(&encode_flow_type_key_segment(flow_type));
    key
}

/// Reports whether the full object key of `flow_type`, with the store prefix, is too long.
pub(super) fn object_key_exceeds_limit(
    object_key_prefix_bytes: usize,
    tenant_id: TenantId,
    flow_type: &str,
) -> bool {
    object_key_prefix_bytes.saturating_add(flow_definition_key(tenant_id, flow_type).len())
        > MAX_OBJECT_KEY_BYTES
}

/// Decodes a key segment that this server wrote; `None` for a foreign or non-canonical segment.
///
/// A canonical segment is valid percent-encoding of non-empty UTF-8 that encodes back to itself.
pub(super) fn decode_canonical_key_segment(segment: &str) -> Option<String> {
    let flow_type = decode_percent_encoding(segment)?;
    (!flow_type.is_empty() && encode_flow_type_key_segment(&flow_type) == segment)
        .then_some(flow_type)
}

/// Encodes the key segment that the next listing page starts after as an opaque token.
pub(super) fn encode_page_token(start_after_segment: &str) -> String {
    URL_SAFE_NO_PAD.encode(start_after_segment)
}

/// Decodes an untrusted page token; `None` unless this server could have returned it.
pub(super) fn decode_page_token(token: &str) -> Option<String> {
    if token.len() > MAX_PAGE_TOKEN_SEGMENT_BYTES.div_ceil(3) * 4 {
        return None;
    }
    let bytes = URL_SAFE_NO_PAD.decode(token).ok()?;
    let segment = String::from_utf8(bytes).ok()?;
    (!segment.is_empty() && segment.len() <= MAX_PAGE_TOKEN_SEGMENT_BYTES).then_some(segment)
}

/// Percent-encodes every byte outside `[A-Za-z0-9._-]`, and the dots of `.` and `..`.
fn encode_flow_type_key_segment(flow_type: &str) -> String {
    if flow_type == "." || flow_type == ".." {
        return "%2E".repeat(flow_type.len());
    }
    let mut segment = String::with_capacity(flow_type.len());
    for byte in flow_type.bytes() {
        if is_unreserved_key_byte(byte) {
            segment.push(char::from(byte));
        } else {
            segment.push_str(&format!("%{byte:02X}"));
        }
    }
    segment
}

fn decode_percent_encoding(segment: &str) -> Option<String> {
    let bytes = segment.as_bytes();
    let mut decoded = Vec::with_capacity(bytes.len());
    let mut position = 0;
    while position < bytes.len() {
        if bytes[position] == b'%' {
            let hex = segment.get(position + 1..position + 3)?;
            decoded.push(u8::from_str_radix(hex, 16).ok()?);
            position += 3;
        } else {
            decoded.push(bytes[position]);
            position += 1;
        }
    }
    String::from_utf8(decoded).ok()
}

fn is_unreserved_key_byte(byte: u8) -> bool {
    byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'_' | b'-')
}

/// Reads at most `max_object_bytes` of the record of `flow_type` at `key` and checks it.
///
/// Returns `Unavailable` when object storage fails, and `Internal` when the record does not decode,
/// names another Flow type, or its digest does not match its content.
pub(super) async fn read_flow_definition(
    object_store: &dyn ObjectStore,
    key: &str,
    flow_type: &str,
    max_object_bytes: u64,
) -> Result<FlowDefinitionRead, FlowOperationError> {
    let range = ByteRange {
        start: 0,
        end_inclusive: max_object_bytes.saturating_sub(1),
    };
    let Some(object) = object_store
        .get_range(key, range)
        .await
        .map_err(|error| object_store_unavailable("read", key, &error))?
    else {
        return Ok(FlowDefinitionRead::Missing);
    };
    if object.object_size > max_object_bytes {
        return Ok(FlowDefinitionRead::TooLarge {
            object_size: object.object_size,
        });
    }
    let record = FlowDefinitionRecord::decode(object.bytes)
        .map_err(|error| corrupt_record(key, format!("does not decode: {error}")))?;
    if record.flow_type != flow_type {
        return Err(corrupt_record(
            key,
            format!("names Flow type {:?}", record.flow_type),
        ));
    }
    if record.uploader.len() > MAX_UPLOADER_BYTES {
        return Err(corrupt_record(
            key,
            format!("has a {}-byte uploader", record.uploader.len()),
        ));
    }
    let digest = FlowDefinitionDigest::from_stored_bytes(&record.digest)
        .ok_or_else(|| corrupt_record(key, format!("has a {}-byte digest", record.digest.len())))?;
    if FlowDefinitionDigest::of_content(&record.content) != digest {
        return Err(corrupt_record(
            key,
            "has a digest that does not match its content",
        ));
    }
    Ok(FlowDefinitionRead::Stored(StoredFlowDefinition {
        record,
        digest,
        etag: object.etag,
    }))
}

/// Maps an object storage failure on `key` to `Unavailable`.
pub(super) fn object_store_unavailable(
    operation: &str,
    key: &str,
    error: &ObjectStoreError,
) -> FlowOperationError {
    FlowOperationError::Unavailable {
        detail: format!("{operation} Flow definition object {key}: {error}"),
    }
}

fn corrupt_record(key: &str, detail: impl std::fmt::Display) -> FlowOperationError {
    FlowOperationError::Internal {
        detail: format!("Flow definition object {key} {detail}"),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn keys_keep_safe_bytes_and_percent_encode_the_rest() {
        assert_eq!(
            flow_definition_key(TenantId::DEFAULT, "Order_Flow-v1.2"),
            "registry/flow-definitions/0/types/Order_Flow-v1.2"
        );
        assert_eq!(encode_flow_type_key_segment("a/b c%"), "a%2Fb%20c%25");
        assert_eq!(encode_flow_type_key_segment("é"), "%C3%A9");
        assert_eq!(encode_flow_type_key_segment("."), "%2E");
        assert_eq!(encode_flow_type_key_segment(".."), "%2E%2E");
        assert_eq!(encode_flow_type_key_segment("..."), "...");
        for flow_type in ["Order_Flow-v1.2", "a/b c%", "é", ".", "..", "..."] {
            let segment = encode_flow_type_key_segment(flow_type);
            assert_eq!(
                decode_canonical_key_segment(&segment).as_deref(),
                Some(flow_type)
            );
        }
        for segment in ["", "%", "%2", "%FF", "foo/bar", "%41", "%2f", "a b"] {
            assert_eq!(decode_canonical_key_segment(segment), None, "{segment}");
        }
    }

    #[test]
    fn page_tokens_round_trip_any_listed_segment() {
        for segment in ["foo/bar", "%FF", "a%2Fb"] {
            assert_eq!(
                decode_page_token(&encode_page_token(segment)).as_deref(),
                Some(segment)
            );
        }
        let longest = "x".repeat(MAX_PAGE_TOKEN_SEGMENT_BYTES);
        assert_eq!(
            decode_page_token(&encode_page_token(&longest)).as_deref(),
            Some(longest.as_str())
        );
        let too_long = "x".repeat(MAX_PAGE_TOKEN_SEGMENT_BYTES + 1);
        assert_eq!(decode_page_token(&encode_page_token(&too_long)), None);
        assert_eq!(decode_page_token("not base64!"), None);
        assert_eq!(decode_page_token(&URL_SAFE_NO_PAD.encode([0xFF])), None);
    }
}
