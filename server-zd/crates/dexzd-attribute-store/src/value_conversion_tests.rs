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

use crate::error::redact_secret;
use crate::map_table::MapTableSchema;
use crate::schema::TableReference;
use crate::settings::ColumnNaming;
use crate::value_conversion::{ColumnSchema, ConvertedValue, SqlArgument, parse_datetime};

const NANOSECONDS_PER_SECOND: i128 = 1_000_000_000;

pub(crate) fn column(name: &str, data_type: &str) -> ColumnSchema {
    ColumnSchema {
        name: name.to_owned(),
        data_type: data_type.to_owned(),
        column_type: String::new(),
        is_nullable: true,
        character_maximum: None,
        numeric_precision: None,
        numeric_scale: None,
        has_default: false,
        is_generated: false,
    }
}

pub(crate) fn string_value(text: &str) -> Value {
    Value {
        kind: Some(Kind::StringValue(text.to_owned())),
    }
}

pub(crate) fn int_value(integer: i64) -> Value {
    Value {
        kind: Some(Kind::IntValue(integer)),
    }
}

pub(crate) fn double_value(double: f64) -> Value {
    Value {
        kind: Some(Kind::DoubleValue(double)),
    }
}

pub(crate) fn bool_value(boolean: bool) -> Value {
    Value {
        kind: Some(Kind::BoolValue(boolean)),
    }
}

pub(crate) fn object_value(encoding: &str, payload: &str) -> Value {
    Value {
        kind: Some(Kind::ObjValue(EncodedObject {
            encoding: encoding.to_owned(),
            payload: payload.as_bytes().to_vec(),
        })),
    }
}

pub(crate) fn null_value() -> Value {
    Value {
        kind: Some(Kind::NullValue(0)),
    }
}

fn unix_nanoseconds(seconds: i64, nanoseconds: i64) -> i128 {
    i128::from(seconds) * NANOSECONDS_PER_SECOND + i128::from(nanoseconds)
}

#[test]
fn column_conversion_boundaries_match_go() {
    let text = ColumnSchema {
        character_maximum: Some(3),
        ..column("name", "character varying")
    };
    assert_eq!(
        text.convert(&string_value("三字好")),
        Ok(ConvertedValue::Text("三字好".to_owned()))
    );
    assert!(
        text.convert(&string_value("four"))
            .expect_err("too long")
            .contains("length")
    );

    let small = column("count", "smallint");
    assert_eq!(
        small.convert(&int_value(32_767)),
        Ok(ConvertedValue::Int(32_767))
    );
    assert!(
        small
            .convert(&int_value(32_768))
            .expect_err("out of range")
            .contains("int64")
    );
    let integer = column("count", "integer");
    assert!(integer.convert(&int_value(i64::from(i32::MIN))).is_ok());
    assert!(
        integer
            .convert(&int_value(i64::from(i32::MAX) + 1))
            .is_err()
    );
    assert!(
        column("count", "bigint")
            .convert(&int_value(i64::MIN))
            .is_ok()
    );
    assert!(
        column("count", "double precision")
            .convert(&int_value(1))
            .is_err()
    );

    let numeric = ColumnSchema {
        numeric_precision: Some(3),
        numeric_scale: Some(0),
        ..column("amount", "numeric")
    };
    assert!(numeric.convert(&int_value(-999)).is_ok());
    assert!(
        numeric
            .convert(&int_value(1_000))
            .expect_err("too many digits")
            .contains("int64")
    );
    let scaled = ColumnSchema {
        numeric_precision: Some(10),
        numeric_scale: Some(2),
        ..column("amount", "numeric")
    };
    assert!(scaled.convert(&int_value(1)).is_err());
    assert_eq!(
        scaled.convert(&double_value(2.5)),
        Ok(ConvertedValue::Double(2.5))
    );
    assert!(
        column("amount", "numeric")
            .convert(&int_value(i64::MAX))
            .is_ok()
    );

    let double = column("ratio", "double precision");
    assert!(
        double
            .convert(&double_value(f64::INFINITY))
            .expect_err("infinite")
            .contains("double")
    );
    assert!(double.convert(&double_value(f64::NAN)).is_err());

    let tiny_boolean = ColumnSchema {
        column_type: "tinyint(1)".to_owned(),
        ..column("active", "tinyint")
    };
    assert!(
        tiny_boolean
            .convert(&bool_value(true))
            .expect_err("not boolean")
            .contains("bool")
    );
    assert_eq!(
        column("active", "boolean").convert(&bool_value(true)),
        Ok(ConvertedValue::Bool(true))
    );

    let json = column("profile", "jsonb");
    assert_eq!(
        json.convert(&object_value("json", r#"{"valid":true}"#)),
        Ok(ConvertedValue::Json(r#"{"valid":true}"#.to_owned()))
    );
    assert!(
        json.convert(&object_value("json", "{invalid"))
            .expect_err("invalid")
            .contains("invalid")
    );
    assert!(
        json.convert(&object_value("raw", "bytes"))
            .expect_err("binary")
            .contains("binary")
    );
    assert!(
        column("name", "text")
            .convert(&object_value("json", "{}"))
            .expect_err("json")
            .contains("JSON")
    );

    let bytes = column("payload", "bytea");
    assert_eq!(
        bytes.convert(&object_value("proto", "bytes")),
        Ok(ConvertedValue::Bytes(b"bytes".to_vec()))
    );

    let timestamp = column("logged_at", "timestamp with time zone");
    assert_eq!(
        timestamp.convert(&string_value("2026-08-11T15:30:00.123456789Z")),
        Ok(ConvertedValue::Timestamp(unix_nanoseconds(
            1_786_462_200,
            123_456_789
        )))
    );
    assert_eq!(
        timestamp.convert(&object_value("json", r#""2026-08-11T15:30:00Z""#)),
        Ok(ConvertedValue::Timestamp(unix_nanoseconds(
            1_786_462_200,
            0
        )))
    );
    assert_eq!(
        timestamp.convert(&object_value("json", "1786462200.123456789")),
        Ok(ConvertedValue::Timestamp(unix_nanoseconds(
            1_786_462_200,
            123_456_789
        )))
    );
    assert!(
        timestamp
            .convert(&string_value("not-a-datetime"))
            .expect_err("bad")
            .contains("RFC3339")
    );
    assert!(timestamp.convert(&object_value("json", "true")).is_err());
    assert!(
        column("logged_at", "timestamp without time zone")
            .convert(&string_value("x"))
            .is_err()
    );

    let required = ColumnSchema {
        is_nullable: false,
        ..column("name", "text")
    };
    assert!(
        required
            .convert(&null_value())
            .expect_err("required")
            .contains("nullable")
    );
    assert_eq!(
        column("name", "text").convert(&null_value()),
        Ok(ConvertedValue::Null)
    );
    assert!(
        column("name", "text")
            .convert(&Value { kind: None })
            .expect_err("missing")
            .contains("missing")
    );
    let blob = Value {
        kind: Some(Kind::InternalBlobIdForStringValue("blob".to_owned())),
    };
    assert!(
        column("name", "text")
            .convert(&blob)
            .expect_err("blob")
            .contains("hydrated")
    );
}

#[test]
fn datetime_parsing_matches_go_rfc3339() {
    let base = unix_nanoseconds(1_786_462_200, 0);
    for (text, expected) in [
        ("2026-08-11T15:30:00Z", Some(base)),
        ("2026-08-11T08:30:00-07:00", Some(base)),
        ("2026-08-11T17:00:00+01:30", Some(base)),
        ("2026-08-11T15:30:00.5Z", Some(base + 500_000_000)),
        ("2026-08-11T15:30:00,25Z", Some(base + 250_000_000)),
        ("2026-08-11T15:30:00.1234567891Z", Some(base + 123_456_789)),
        ("2026-08-12T15:30:00+24:00", Some(base)),
        (
            "2024-02-29T00:00:00Z",
            Some(unix_nanoseconds(1_709_164_800, 0)),
        ),
        (
            "0000-01-01T00:00:00Z",
            Some(unix_nanoseconds(-62_167_219_200, 0)),
        ),
        ("2026-02-29T00:00:00Z", None),
        ("2026-08-11T24:00:00Z", None),
        ("2026-08-11T15:60:00Z", None),
        ("2026-08-11t15:30:00Z", None),
        ("2026-08-11 15:30:00Z", None),
        ("2026-08-11T15:30:00z", None),
        ("2026-08-11T15:30:00", None),
        ("2026-08-11T15:30:00.Z", None),
        ("2026-08-11T15:30:00+0700", None),
        ("2026-13-11T15:30:00Z", None),
    ] {
        assert_eq!(parse_datetime(text).ok(), expected, "{text}");
    }
}

/// Expected values are the output of Go 1.26's `time.Parse(time.RFC3339Nano, text).UnixNano()`.
#[test]
fn datetime_parsing_accepts_go_single_digit_hours() {
    for (text, expected) in [
        ("2006-01-02T5:04:05Z", Some(1_136_178_245_000_000_000)),
        (
            "2006-01-02T5:04:05.123+05:30",
            Some(1_136_158_445_123_000_000),
        ),
        ("2006-01-02T05:04:05Z", Some(1_136_178_245_000_000_000)),
        ("2006-01-02T9:04:05,5Z", Some(1_136_192_645_500_000_000)),
        ("2006-01-02T0:00:00-00:30", Some(1_136_161_800_000_000_000)),
        ("2006-01-02T15:4:05Z", None),
        ("2006-1-02T15:04:05Z", None),
        ("2006-01-02T:04:05Z", None),
        ("2006-01-02T123:04:05Z", None),
        ("2006-01-02T24:04:05Z", None),
        ("2006-01-02T5:04:0", None),
        ("2006-01-02T5", None),
        ("2006-01-02T", None),
        ("2006-01-02", None),
    ] {
        assert_eq!(parse_datetime(text).ok(), expected, "{text}");
    }
}

#[test]
fn epoch_seconds_match_go() {
    let timestamp = column("logged_at", "timestamp with time zone");
    for (payload, expected) in [
        ("0", Some(0)),
        ("-1.5", Some(-1_500_000_000)),
        ("1.000000001", Some(1_000_000_001)),
        (" 7 ", Some(7 * NANOSECONDS_PER_SECOND)),
        ("1e9", None),
        ("1.5e3", None),
        ("1.1234567890", None),
        ("9223372036854775808", None),
    ] {
        let converted = timestamp.convert(&object_value("json", payload)).ok();
        assert_eq!(
            converted,
            expected.map(ConvertedValue::Timestamp),
            "{payload}"
        );
    }
}

#[test]
fn arguments_encode_like_pgx() {
    let numeric = column("amount", "numeric");
    assert_eq!(
        numeric.argument(ConvertedValue::Double(0.1 + 0.2)),
        SqlArgument::Text("0.30000000000000004".to_owned())
    );
    assert_eq!(
        numeric.argument(ConvertedValue::Double(1e21)),
        SqlArgument::Text("1000000000000000000000".to_owned())
    );
    assert_eq!(
        numeric.argument(ConvertedValue::Int(-42)),
        SqlArgument::Text("-42".to_owned())
    );
    let real = column("ratio", "real");
    assert_eq!(
        real.argument(ConvertedValue::Double(0.1)),
        SqlArgument::Text("0.1".to_owned())
    );
    assert_eq!(
        real.argument(ConvertedValue::Double(1e300)),
        SqlArgument::Text("Infinity".to_owned())
    );
    let double = column("ratio", "double precision");
    assert_eq!(
        double.argument(ConvertedValue::Double(2.5)),
        SqlArgument::Text("2.5".to_owned())
    );
    assert_eq!(
        double.argument(ConvertedValue::Double(-0.0)),
        SqlArgument::Text("-0".to_owned())
    );
    let text = column("name", "text");
    assert_eq!(
        text.argument(ConvertedValue::Bool(true)),
        SqlArgument::Text("true".to_owned())
    );
    assert_eq!(text.argument(ConvertedValue::Null), SqlArgument::Null);
    assert_eq!(
        text.argument(ConvertedValue::Bytes(vec![0x01, 0xab, 0x00])),
        SqlArgument::Text("\\x01ab00".to_owned())
    );
    let microseconds_to_y2k = 946_684_800_000_000_i64;
    assert_eq!(
        text.argument(ConvertedValue::Timestamp(unix_nanoseconds(
            1_786_462_200,
            123_456_789
        ))),
        SqlArgument::TimestampMicrosecondsSinceY2k(1_786_462_200_123_456 - microseconds_to_y2k)
    );
    assert_eq!(
        text.argument(ConvertedValue::Timestamp(-1_500_000_001)),
        SqlArgument::TimestampMicrosecondsSinceY2k(-1_500_001 - microseconds_to_y2k)
    );
}

#[test]
fn map_instance_fields_convert_like_go() {
    let columns = BTreeMap::from(
        [
            column("group_id", "text"),
            column("account_id", "text"),
            column("role", "text"),
            column("score", "double precision"),
            column("item_count", "bigint"),
            column("profile", "jsonb"),
            column("added_at", "timestamp with time zone"),
            column("is_active", "boolean"),
            column("note", "text"),
        ]
        .map(|column| (column.name.clone(), column)),
    );
    let reference = TableReference {
        namespace: "public".to_owned(),
        table: "members".to_owned(),
    };
    let table = MapTableSchema::new(
        reference,
        "account_id",
        &["group_id".to_owned()],
        &["account_id".to_owned(), "group_id".to_owned()],
        columns,
    )
    .expect("valid map table");
    assert_eq!(
        table.replaced_columns,
        [
            "added_at",
            "is_active",
            "item_count",
            "note",
            "profile",
            "role",
            "score"
        ]
    );
    let value = object_value(
        "json",
        r#" {"Role":"ADMIN","Score":3,"ItemCount":3,"Profile":{"tags":["a"]},"AddedAt":"2026-08-11T15:30:00Z",
        "IsActive":true,"Note":null,"Unknown":"skipped","GroupID":"g1","AccountID":"other"} "#,
    );
    let key_values = ["g1".to_owned(), "account-1".to_owned()];
    let columns = table.filter_instance_value(
        "store",
        "Members/account-1",
        &key_values,
        &value,
        ColumnNaming::SnakeCase,
    );
    assert_eq!(columns.missing_column_fields, ["Unknown"]);
    assert_eq!(
        columns.values,
        BTreeMap::from([
            (
                "added_at".to_owned(),
                SqlArgument::TimestampMicrosecondsSinceY2k(
                    1_786_462_200_000_000 - 946_684_800_000_000
                )
            ),
            ("is_active".to_owned(), SqlArgument::Text("true".to_owned())),
            ("item_count".to_owned(), SqlArgument::Text("3".to_owned())),
            ("note".to_owned(), SqlArgument::Null),
            (
                "profile".to_owned(),
                SqlArgument::Text(r#"{"tags":["a"]}"#.to_owned())
            ),
            ("role".to_owned(), SqlArgument::Text("ADMIN".to_owned())),
            ("score".to_owned(), SqlArgument::Text("3".to_owned())),
        ])
    );
    assert_eq!(
        table.unknown_columns(&value, ColumnNaming::SnakeCase),
        ["unknown"]
    );
    assert!(
        table
            .unknown_columns(&null_value(), ColumnNaming::SnakeCase)
            .is_empty()
    );
    assert_eq!(
        table.unknown_columns(&string_value("plain"), ColumnNaming::SnakeCase),
        ["value"]
    );

    let plain = table.filter_instance_value(
        "store",
        "Members/a",
        &key_values,
        &string_value("x"),
        ColumnNaming::AsIs,
    );
    assert!(plain.values.is_empty());
    assert_eq!(plain.missing_column_fields, ["value"]);

    let failures = MapTableSchema::new(
        TableReference {
            namespace: "public".to_owned(),
            table: "members".to_owned(),
        },
        "group_id",
        &["group_id".to_owned()],
        &["group_id".to_owned()],
        BTreeMap::new(),
    )
    .expect_err("instance key is a store key");
    assert!(failures.contains("also a primary-key column"), "{failures}");
}

#[test]
fn redaction_replaces_every_dsn_occurrence() {
    let dsn = "postgres://dex:secret@db/dex";
    let redacted = redact_secret(format!("connect {dsn}: refused by {dsn}"), dsn);
    assert_eq!(redacted, "connect [REDACTED]: refused by [REDACTED]");
    assert_eq!(redact_secret("plain".to_owned(), ""), "plain");
}
