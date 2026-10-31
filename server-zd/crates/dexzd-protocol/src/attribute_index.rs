// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use crate::dex::{IndexType, Value, value};

/// Infers the index type used when an Attribute's IndexConfig type is unspecified.
pub fn inferred_index_type(value: &Value) -> IndexType {
    match value.kind {
        Some(value::Kind::StringValue(_) | value::Kind::ObjValue(_)) => IndexType::Keyword,
        Some(value::Kind::IntValue(_)) => IndexType::Int,
        Some(value::Kind::DoubleValue(_)) => IndexType::Double,
        Some(value::Kind::BoolValue(_)) => IndexType::Bool,
        _ => IndexType::Unspecified,
    }
}

/// Maximum payload bytes included in a search document's indexed Attribute value.
pub const MAX_SEARCH_VALUE_BYTES: usize = 256 * 1024;

/// Returns the payload size used to decide whether an Attribute value can be indexed.
pub fn search_value_payload_size(value: &Value) -> usize {
    match value.kind.as_ref() {
        Some(value::Kind::StringValue(text)) => text.len(),
        Some(value::Kind::ObjValue(object)) => object.payload.len(),
        _ => prost::Message::encoded_len(value),
    }
}

/// Returns the strings indexed for Keyword or KeywordArray, or None when the value is not indexed.
pub fn indexed_keyword_strings(value: &Value, index_type: IndexType) -> Option<Vec<String>> {
    if search_value_payload_size(value) > MAX_SEARCH_VALUE_BYTES {
        return None;
    }
    use value::Kind;

    match (index_type, value.kind.as_ref()?) {
        (IndexType::Keyword, Kind::StringValue(text)) => Some(vec![text.clone()]),
        (IndexType::Keyword, Kind::ObjValue(object)) => {
            Some(vec![String::from_utf8(object.payload.clone()).ok()?])
        }
        (IndexType::Keyword, Kind::IntValue(number)) => Some(vec![number.to_string()]),
        (IndexType::Keyword, Kind::DoubleValue(number)) => Some(vec![format_go_float(*number)]),
        (IndexType::Keyword, Kind::BoolValue(boolean)) => Some(vec![boolean.to_string()]),
        (IndexType::KeywordArray, Kind::StringValue(text)) => {
            serde_json::from_slice(text.as_bytes()).ok()
        }
        (IndexType::KeywordArray, Kind::ObjValue(object)) => {
            serde_json::from_slice(&object.payload).ok()
        }
        _ => None,
    }
}

fn format_go_float(number: f64) -> String {
    if number.is_nan() {
        return "NaN".to_owned();
    }
    if number == f64::INFINITY {
        return "+Inf".to_owned();
    }
    if number == f64::NEG_INFINITY {
        return "-Inf".to_owned();
    }
    let magnitude = number.abs();
    if magnitude >= 1_000_000.0 || (magnitude > 0.0 && magnitude < 0.0001) {
        let scientific = format!("{number:e}");
        let (mantissa, exponent) = scientific
            .split_once('e')
            .expect("Rust scientific formatting always includes an exponent");
        let exponent: i32 = exponent
            .parse()
            .expect("Rust scientific formatting always uses an integer exponent");
        return format!("{mantissa}e{exponent:+03}");
    }
    number.to_string()
}
