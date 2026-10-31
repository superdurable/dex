// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dex_protocol::dex::{EncodedObject, Value as ProtoValue, value};
use prost_types::NullValue;
use serde_json::Value as JsonValue;

use crate::{HandlerError, HandlerResult, SdkError, SdkResult, Value};

pub(crate) fn encode<T: Value>(input: &T) -> SdkResult<ProtoValue> {
    let json = serde_json::to_value(input).map_err(mapping_error)?;
    encode_json(json)
}

pub(crate) fn encode_handler<T: Value>(input: &T) -> HandlerResult<ProtoValue> {
    encode(input).map_err(handler_mapping_error)
}

pub(crate) fn decode<T: Value>(input: &ProtoValue) -> SdkResult<T> {
    let json = decode_json(input)?;
    serde_json::from_value(json).map_err(mapping_error)
}

/// Returns the decoded value, or a [`SdkError::ValueMapping`] whose message never quotes it.
pub(crate) fn decode_without_value_detail<T: Value>(input: &ProtoValue) -> SdkResult<T> {
    let json = match input.kind.as_ref() {
        Some(value::Kind::ObjValue(object)) if object.encoding == "json" => {
            serde_json::from_slice(&object.payload).map_err(value_free_mapping_error)?
        }
        _ => decode_json(input)?,
    };
    serde_json::from_value(json).map_err(value_free_mapping_error)
}

pub(crate) fn decode_handler<T: Value>(input: &ProtoValue) -> HandlerResult<T> {
    decode(input).map_err(handler_mapping_error)
}

pub(crate) fn decode_untyped(input: &ProtoValue) -> SdkResult<JsonValue> {
    decode_json(input)
}

pub(crate) fn deletion() -> ProtoValue {
    ProtoValue {
        kind: Some(value::Kind::NullValue(NullValue::NullValue.into())),
    }
}

fn encode_json(json: JsonValue) -> SdkResult<ProtoValue> {
    let kind = match json {
        JsonValue::Null => value::Kind::NullValue(NullValue::NullValue.into()),
        JsonValue::String(text) => value::Kind::StringValue(text),
        JsonValue::Bool(value) => value::Kind::BoolValue(value),
        JsonValue::Number(number) => {
            if let Some(value) = number.as_i64() {
                value::Kind::IntValue(value)
            } else if let Some(value) = number.as_u64() {
                let value = i64::try_from(value).map_err(|_| SdkError::ValueMapping {
                    message: format!("unsigned integer {value} exceeds int64"),
                })?;
                value::Kind::IntValue(value)
            } else {
                let value = number.as_f64().ok_or_else(|| SdkError::ValueMapping {
                    message: format!("unsupported JSON number {number}"),
                })?;
                if !value.is_finite() {
                    return Err(SdkError::ValueMapping {
                        message: "non-finite numbers are unsupported".to_string(),
                    });
                }
                value::Kind::DoubleValue(value)
            }
        }
        other => value::Kind::ObjValue(EncodedObject {
            encoding: "json".to_string(),
            payload: serde_json::to_vec(&other).map_err(mapping_error)?,
        }),
    };
    Ok(ProtoValue { kind: Some(kind) })
}

fn decode_json(input: &ProtoValue) -> SdkResult<JsonValue> {
    match input.kind.as_ref() {
        Some(value::Kind::StringValue(text)) => Ok(JsonValue::String(text.clone())),
        Some(value::Kind::IntValue(number)) => Ok(JsonValue::from(*number)),
        Some(value::Kind::DoubleValue(number)) if number.is_finite() => {
            Ok(JsonValue::from(*number))
        }
        Some(value::Kind::DoubleValue(_)) => Err(value_error("non-finite numbers are unsupported")),
        Some(value::Kind::BoolValue(value)) => Ok(JsonValue::Bool(*value)),
        Some(value::Kind::ObjValue(object)) if object.encoding == "json" => {
            serde_json::from_slice(&object.payload).map_err(mapping_error)
        }
        Some(value::Kind::ObjValue(object)) if object.encoding == "raw" => Ok(JsonValue::Array(
            object
                .payload
                .iter()
                .copied()
                .map(JsonValue::from)
                .collect(),
        )),
        Some(value::Kind::ObjValue(object)) => Err(value_error(format!(
            "unsupported object encoding {}",
            object.encoding
        ))),
        Some(value::Kind::InternalBlobIdForStringValue(_))
        | Some(value::Kind::InternalBlobIdForObjValue(_)) => {
            Err(value_error("blob-backed Value was not hydrated"))
        }
        Some(value::Kind::NullValue(_)) => Ok(JsonValue::Null),
        None => Err(value_error("Value has no concrete kind")),
    }
}

fn value_free_mapping_error(error: serde_json::Error) -> SdkError {
    let category = match error.classify() {
        serde_json::error::Category::Io => "I/O error",
        serde_json::error::Category::Syntax => "syntax error",
        serde_json::error::Category::Data => "data error",
        serde_json::error::Category::Eof => "unexpected end of input",
    };
    let message = error.to_string();
    let mut detail = match field_name_detail(&message) {
        Some(field_detail) => format!("{category}: {field_detail}"),
        // An unknown field name comes from the stored object, so it is omitted.
        None if message.starts_with("unknown field `") => format!("{category}: unknown field"),
        None => category.to_string(),
    };
    if error.line() != 0 {
        detail.push_str(&format!(
            " at line {} column {}",
            error.line(),
            error.column()
        ));
    }
    value_error(detail)
}

/// Returns the leading declared-field clause of a serde message, such as "missing field `id`".
fn field_name_detail(message: &str) -> Option<&str> {
    ["missing field `", "duplicate field `"]
        .into_iter()
        .find_map(|prefix| {
            let field_name_length = message.strip_prefix(prefix)?.find('`')?;
            Some(&message[..prefix.len() + field_name_length + 1])
        })
}

fn mapping_error(error: impl std::fmt::Display) -> SdkError {
    value_error(error.to_string())
}

fn value_error(message: impl Into<String>) -> SdkError {
    SdkError::ValueMapping {
        message: message.into(),
    }
}

fn handler_mapping_error(error: SdkError) -> HandlerError {
    HandlerError::from_error(error)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn uses_compact_object_encoding_wire_values() {
        let encoded_json = encode(&vec![1, 2, 3]).expect("encode JSON");
        let Some(value::Kind::ObjValue(encoded_json)) = encoded_json.kind else {
            panic!("expected encoded object");
        };
        assert_eq!(encoded_json.encoding, "json");

        let encoded_bytes = ProtoValue {
            kind: Some(value::Kind::ObjValue(EncodedObject {
                encoding: "raw".to_string(),
                payload: vec![1, 2, 3],
            })),
        };
        assert_eq!(
            decode::<Vec<u8>>(&encoded_bytes).expect("decode bytes"),
            vec![1, 2, 3]
        );

        let encoded_null = encode(&Option::<String>::None).expect("encode null");
        assert!(matches!(encoded_null.kind, Some(value::Kind::NullValue(_))));
        assert_eq!(
            decode::<Option<String>>(&encoded_null).expect("decode null"),
            None
        );
    }
}
