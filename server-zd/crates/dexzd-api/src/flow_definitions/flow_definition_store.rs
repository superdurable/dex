// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dexzd_keyspace::TenantId;
use dexzd_object_store::{ObjectStore, ObjectStoreError};
use dexzd_protocol::dexzd::v1::FlowDefinitionRecord;
use prost::Message;

use super::flow_definition_digest::FlowDefinitionDigest;
use crate::FlowOperationError;

/// Longest percent-encoded Flow type in an object key; S3 keys hold at most 1024 bytes.
const MAX_ENCODED_FLOW_TYPE_BYTES: usize = 768;

/// One stored record with the ETag that conditional replacement needs.
#[derive(Debug, Clone)]
pub(super) struct StoredFlowDefinition {
    pub(super) record: FlowDefinitionRecord,
    pub(super) digest: FlowDefinitionDigest,
    pub(super) etag: String,
}

/// Returns `registry/flow-definitions/<tenant_id>/types/`, the prefix of every record of a tenant.
pub(super) fn flow_definition_types_prefix(tenant_id: TenantId) -> String {
    format!("registry/flow-definitions/{}/types/", tenant_id.0)
}

/// Returns the object key of the record of `flow_type`.
pub(super) fn flow_definition_key(tenant_id: TenantId, flow_type: &str) -> String {
    let mut key = flow_definition_types_prefix(tenant_id);
    key.push_str(&encode_flow_type_key_segment(flow_type));
    key
}

/// Reports whether the encoded `flow_type` would make an object key too long.
pub(super) fn encoded_flow_type_exceeds_key_limit(flow_type: &str) -> bool {
    encode_flow_type_key_segment(flow_type).len() > MAX_ENCODED_FLOW_TYPE_BYTES
}

/// Reports whether an untrusted page token can be an encoded Flow type key segment.
pub(super) fn is_valid_encoded_key_segment(segment: &str) -> bool {
    segment.len() <= MAX_ENCODED_FLOW_TYPE_BYTES
        && segment
            .bytes()
            .all(|byte| byte == b'%' || is_unreserved_key_byte(byte))
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

/// Decodes a stored key segment back to its Flow type; `None` when it is not valid percent-encoded UTF-8.
pub(super) fn decode_flow_type_key_segment(segment: &str) -> Option<String> {
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

/// Reads the record at `key`; `None` when no definition is stored.
///
/// Returns `Unavailable` when object storage fails and `Internal` for a corrupt record.
pub(super) async fn read_flow_definition(
    object_store: &dyn ObjectStore,
    key: &str,
) -> Result<Option<StoredFlowDefinition>, FlowOperationError> {
    let Some(object) = object_store
        .get(key)
        .await
        .map_err(|error| object_store_unavailable("read", key, &error))?
    else {
        return Ok(None);
    };
    let record = FlowDefinitionRecord::decode(object.bytes).map_err(|error| {
        FlowOperationError::Internal {
            detail: format!("Flow definition object {key} does not decode: {error}"),
        }
    })?;
    let digest = FlowDefinitionDigest::from_stored_bytes(&record.digest).ok_or_else(|| {
        FlowOperationError::Internal {
            detail: format!(
                "Flow definition object {key} has a {}-byte digest",
                record.digest.len()
            ),
        }
    })?;
    Ok(Some(StoredFlowDefinition {
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
        assert!(!encoded_flow_type_exceeds_key_limit(&"/".repeat(256)));
        assert!(encoded_flow_type_exceeds_key_limit(&"/".repeat(257)));
        for flow_type in ["Order_Flow-v1.2", "a/b c%", "é", ".", "..", "..."] {
            assert_eq!(
                decode_flow_type_key_segment(&encode_flow_type_key_segment(flow_type)).as_deref(),
                Some(flow_type)
            );
        }
        assert_eq!(decode_flow_type_key_segment("%2"), None);
        assert_eq!(decode_flow_type_key_segment("%FF"), None);
        assert!(is_valid_encoded_key_segment("a%2Fb"));
        assert!(!is_valid_encoded_key_segment("a/b"));
    }
}
