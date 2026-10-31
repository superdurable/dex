// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dexzd_protocol::attribute_index::inferred_index_type;
use dexzd_protocol::dex::{AttributeWrite, IndexType, value};

use crate::shared::{ValidationConfig, ValidationError, invalid};

/// Maximum UTF-8 bytes in an indexed Keyword or each KeywordArray element.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct KeywordValueLimit {
    maximum_bytes: usize,
}

impl KeywordValueLimit {
    /// Uses the PostgreSQL search backend's 1024-byte Keyword limit.
    pub const fn postgres() -> Self {
        Self {
            maximum_bytes: 1024,
        }
    }
}

pub(crate) fn validate_indexed_keyword(
    path: &str,
    write: &AttributeWrite,
    config: &ValidationConfig,
) -> Result<(), ValidationError> {
    let Some(limit) = config.keyword_value_limit else {
        return Ok(());
    };
    let Some(index_config) = write.index_config.as_ref().filter(|index| index.enable) else {
        return Ok(());
    };
    let Some(value) = write.value.as_ref() else {
        return Ok(());
    };
    let index_type = if index_config.r#type == IndexType::Unspecified as i32 {
        inferred_index_type(value)
    } else {
        IndexType::try_from(index_config.r#type).unwrap_or(IndexType::Unspecified)
    };
    let bytes = match value.kind.as_ref() {
        Some(value::Kind::StringValue(text)) => text.as_bytes(),
        Some(value::Kind::ObjValue(object)) => object.payload.as_slice(),
        _ => return Ok(()),
    };
    let largest_bytes = match index_type {
        IndexType::Keyword => bytes.len(),
        IndexType::KeywordArray => {
            let Ok(elements) = serde_json::from_slice::<Vec<String>>(bytes) else {
                return Ok(());
            };
            elements.iter().map(String::len).max().unwrap_or(0)
        }
        _ => return Ok(()),
    };
    if largest_bytes > limit.maximum_bytes {
        let mut error = invalid(
            path,
            format!(
                "indexed Attribute {:?} has a Keyword value or KeywordArray element of {largest_bytes} bytes; maximum is {} bytes",
                write.key, limit.maximum_bytes
            ),
        );
        error.sub_status = dexzd_protocol::dex::ErrorSubStatus::IndexedKeywordTooLong;
        return Err(error);
    }
    Ok(())
}
