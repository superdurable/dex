// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeMap;

use dexzd_protocol::dex::value::Kind;
use dexzd_protocol::dex::{EncodedObject, Value};
use serde_json::value::RawValue;

use crate::schema::{TableReference, has_same_columns};
use crate::settings::ColumnNaming;
use crate::value_conversion::{ColumnSchema, SqlArgument};

/// Stores an instance value that is not a JSON object.
pub(crate) const MAP_INSTANCE_VALUE_COLUMN: &str = "value";

/// The table whose rows are one AttributeMap's instances.
#[derive(Clone, Debug, PartialEq)]
pub(crate) struct MapTableSchema {
    pub(crate) reference: TableReference,
    pub(crate) key_columns: Vec<String>,
    /// The non-key, non-generated columns that every instance write sets.
    pub(crate) replaced_columns: Vec<String>,
    pub(crate) columns: BTreeMap<String, ColumnSchema>,
}

/// The column values of one instance and the fields that have no column.
#[derive(Debug, Default)]
pub(crate) struct InstanceColumns {
    pub(crate) values: BTreeMap<String, SqlArgument>,
    pub(crate) missing_column_fields: Vec<String>,
}

impl MapTableSchema {
    /// Go's `newMapTableSchema`; the error is Go's message.
    pub(crate) fn new(
        reference: TableReference,
        instance_key_column: &str,
        parent_key_columns: &[String],
        primary_keys: &[String],
        columns: BTreeMap<String, ColumnSchema>,
    ) -> Result<Self, String> {
        if parent_key_columns
            .iter()
            .any(|column| column == instance_key_column)
        {
            return Err(format!(
                "instanceKeyColumn {instance_key_column:?} is also a primary-key column of the store"
            ));
        }
        let mut key_columns = parent_key_columns.to_vec();
        key_columns.push(instance_key_column.to_owned());
        if !has_same_columns(primary_keys, &key_columns) {
            return Err(format!(
                "table primary key {} must be exactly the columns {}",
                go_list(primary_keys),
                go_list(&key_columns)
            ));
        }
        for name in &key_columns {
            if !columns.get(name).is_some_and(ColumnSchema::accepts_string) {
                return Err(format!("primary-key column {name:?} cannot store strings"));
            }
        }
        let mut replaced_columns = Vec::new();
        for (name, column) in &columns {
            if key_columns.contains(name) || column.is_generated {
                continue;
            }
            if !column.is_nullable && !column.has_default {
                return Err(format!("column {name:?} prevents partial row inserts"));
            }
            replaced_columns.push(name.clone());
        }
        Ok(Self {
            reference,
            key_columns,
            replaced_columns,
            columns,
        })
    }

    /// Go's `filterInstanceValue`: object fields map to columns; any other value uses the value column.
    pub(crate) fn filter_instance_value(
        &self,
        store: &str,
        key: &str,
        key_values: &[String],
        value: &Value,
        naming: ColumnNaming,
    ) -> InstanceColumns {
        let mut result = InstanceColumns::default();
        let Some(fields) = json_object_fields(value) else {
            let Some(column) = self.columns.get(MAP_INSTANCE_VALUE_COLUMN) else {
                result
                    .missing_column_fields
                    .push(MAP_INSTANCE_VALUE_COLUMN.to_owned());
                return result;
            };
            match column.convert(value) {
                Ok(converted) => {
                    result.values.insert(
                        MAP_INSTANCE_VALUE_COLUMN.to_owned(),
                        column.argument(converted),
                    );
                }
                Err(error) => tracing::error!(
                    store,
                    attribute = key,
                    error,
                    "skip incompatible Attribute Store map instance"
                ),
            }
            return result;
        };
        let mut field_names_by_column = BTreeMap::<String, Vec<&str>>::new();
        for field_name in fields.keys() {
            field_names_by_column
                .entry(naming.column_name(field_name))
                .or_default()
                .push(field_name);
        }
        for (name, field_names) in field_names_by_column {
            let field_key = format!("{key}.{}", field_names[0]);
            if field_names.len() > 1 {
                tracing::error!(
                    store,
                    attribute = format!("{key}.{}", field_names.join(", ")),
                    "skip Attribute Store map instance fields: field names share one column"
                );
                continue;
            }
            let raw_field = fields[field_names[0]];
            if let Some(key_index) = self.key_columns.iter().position(|column| *column == name) {
                let field_text = serde_json::from_str::<String>(raw_field.get()).ok();
                if field_text.as_deref() != Some(key_values[key_index].as_str()) {
                    tracing::error!(
                        store,
                        attribute = field_key,
                        "skip Attribute Store map instance field: value differs from its primary-key column"
                    );
                }
                continue;
            }
            let Some(column) = self.columns.get(&name) else {
                result.missing_column_fields.push(field_names[0].to_owned());
                continue;
            };
            match column.convert(&json_field_value(raw_field.get(), column)) {
                Ok(converted) => {
                    result.values.insert(name, column.argument(converted));
                }
                Err(error) => tracing::error!(
                    store,
                    attribute = field_key,
                    error,
                    "skip incompatible Attribute Store map instance field"
                ),
            }
        }
        result
    }

    /// Returns the columns an instance value writes that this snapshot lacks; a delete writes none.
    pub(crate) fn unknown_columns(&self, value: &Value, naming: ColumnNaming) -> Vec<String> {
        if is_null(value) {
            return Vec::new();
        }
        let Some(fields) = json_object_fields(value) else {
            if self.columns.contains_key(MAP_INSTANCE_VALUE_COLUMN) {
                return Vec::new();
            }
            return vec![MAP_INSTANCE_VALUE_COLUMN.to_owned()];
        };
        fields
            .keys()
            .map(|field_name| naming.column_name(field_name))
            .filter(|column| !self.columns.contains_key(column))
            .collect()
    }
}

pub(crate) fn is_null(value: &Value) -> bool {
    matches!(value.kind, Some(Kind::NullValue(_)))
}

/// Go's `bytes.TrimSpace` over ASCII whitespace.
pub(crate) fn trim_go_space(bytes: &[u8]) -> &[u8] {
    let is_space = |byte: &u8| matches!(byte, b'\t' | b'\n' | 0x0B | 0x0C | b'\r' | b' ');
    let start = bytes
        .iter()
        .position(|byte| !is_space(byte))
        .unwrap_or(bytes.len());
    let end = bytes
        .iter()
        .rposition(|byte| !is_space(byte))
        .map_or(start, |end| end + 1);
    &bytes[start..end]
}

/// Returns the top-level fields of a JSON object value, and None for any other value.
fn json_object_fields(value: &Value) -> Option<BTreeMap<String, &RawValue>> {
    let Some(Kind::ObjValue(object)) = &value.kind else {
        return None;
    };
    if object.encoding != "json" {
        return None;
    }
    let payload = trim_go_space(&object.payload);
    if payload.first() != Some(&b'{') {
        return None;
    }
    serde_json::from_slice(payload).ok()
}

/// Go's `jsonFieldValue`: integral numbers become int64 only where the column takes one.
fn json_field_value(raw: &str, column: &ColumnSchema) -> Value {
    let trimmed = raw.trim_matches([' ', '\t', '\n', '\r']);
    if trimmed == "null" {
        return Value {
            kind: Some(Kind::NullValue(0)),
        };
    }
    let encoded_json = || Value {
        kind: Some(Kind::ObjValue(EncodedObject {
            encoding: "json".to_owned(),
            payload: trimmed.as_bytes().to_vec(),
        })),
    };
    if column.accepts_json() || column.accepts_temporal() || trimmed.is_empty() {
        return encoded_json();
    }
    let kind = match trimmed.as_bytes()[0] {
        b'"' => match serde_json::from_str::<String>(trimmed) {
            Ok(text) => Kind::StringValue(text),
            Err(_) => return encoded_json(),
        },
        b't' | b'f' => Kind::BoolValue(trimmed.starts_with('t')),
        b'{' | b'[' => return encoded_json(),
        _ => match trimmed.parse::<i64>() {
            Ok(integer) if column.accepts_int(integer) => Kind::IntValue(integer),
            _ => match trimmed.parse::<f64>() {
                // Go's ParseFloat reports overflow to infinity as an error.
                Ok(double) if double.is_finite() => Kind::DoubleValue(double),
                _ => return encoded_json(),
            },
        },
    };
    Value { kind: Some(kind) }
}

/// Formats names like Go's `%v` of a string slice: `[a b]`.
pub(crate) fn go_list(names: &[String]) -> String {
    format!("[{}]", names.join(" "))
}
