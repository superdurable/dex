// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

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
        Some(value::Kind::ObjValue(object)) if object.encoding == "rawbytes" => {
            Ok(JsonValue::Array(
                object
                    .payload
                    .iter()
                    .copied()
                    .map(JsonValue::from)
                    .collect(),
            ))
        }
        Some(value::Kind::ObjValue(object)) => Err(value_error(format!(
            "unsupported object encoding {}",
            object.encoding
        ))),
        Some(value::Kind::InternalBlobIdForStringValue(_))
        | Some(value::Kind::InternalBlobIdForObjValue(_)) => {
            Err(value_error("blob-backed Value was not hydrated"))
        }
        Some(value::Kind::NullValue(_)) => {
            Err(value_error("attribute deletion marker cannot be decoded"))
        }
        None => Err(value_error("Value has no concrete kind")),
    }
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
    HandlerError::new(error.to_string())
}
