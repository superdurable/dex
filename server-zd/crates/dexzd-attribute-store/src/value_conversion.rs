// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::error::Error;

use bytes::{BufMut, BytesMut};
use dexzd_protocol::dex::value::Kind;
use dexzd_protocol::dex::{EncodedObject, Value};
use serde_json::value::RawValue;
use time::{Date, Month};
use tokio_postgres::types::{Format, IsNull, ToSql, Type, to_sql_checked};

/// Microseconds from the Unix epoch to 2000-01-01, the Postgres timestamp epoch.
const MICROSECONDS_FROM_UNIX_EPOCH_TO_Y2K: i128 = 946_684_800_000_000;

/// Julian day number of 1970-01-01.
const UNIX_EPOCH_JULIAN_DAY: i64 = 2_440_588;

/// One column of a described table, as Go's `columnSchema`.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) struct ColumnSchema {
    pub(crate) name: String,
    /// Lower-cased `information_schema.columns.data_type`.
    pub(crate) data_type: String,
    /// Lower-cased `udt_name`.
    pub(crate) column_type: String,
    pub(crate) is_nullable: bool,
    pub(crate) character_maximum: Option<i64>,
    pub(crate) numeric_precision: Option<i64>,
    pub(crate) numeric_scale: Option<i64>,
    pub(crate) has_default: bool,
    pub(crate) is_generated: bool,
}

/// A value that passed Go's column conversion, before Postgres encoding.
#[derive(Clone, Debug, PartialEq)]
pub(crate) enum ConvertedValue {
    Null,
    Text(String),
    Int(i64),
    Double(f64),
    Bool(bool),
    /// Nanoseconds since the Unix epoch, as Go's `time.Time`.
    Timestamp(i128),
    Json(String),
    Bytes(Vec<u8>),
}

/// A statement parameter. Text parameters let Postgres infer and parse the column type, like pgx does.
#[derive(Clone, Debug, PartialEq)]
pub(crate) enum SqlArgument {
    Null,
    Text(String),
    /// Binary timestamptz truncates to microseconds like pgx; text input would round instead.
    TimestampMicrosecondsSinceY2k(i64),
}

impl ColumnSchema {
    /// Go's `columnSchema.convert` for Postgres; the error is Go's message.
    pub(crate) fn convert(&self, value: &Value) -> Result<ConvertedValue, String> {
        let Some(kind) = &value.kind else {
            return Err("value is missing".to_owned());
        };
        match kind {
            Kind::NullValue(_) => {
                if !self.is_nullable {
                    return Err("column is not nullable".to_owned());
                }
                Ok(ConvertedValue::Null)
            }
            Kind::StringValue(text) => {
                if self.accepts_temporal() {
                    return parse_datetime(text).map(ConvertedValue::Timestamp);
                }
                if !self.accepts_string() {
                    return Err("column does not accept strings".to_owned());
                }
                if self
                    .character_maximum
                    .is_some_and(|maximum| text.chars().count() as i64 > maximum)
                {
                    return Err("string exceeds column length".to_owned());
                }
                Ok(ConvertedValue::Text(text.clone()))
            }
            Kind::IntValue(integer) => {
                if !self.accepts_int(*integer) {
                    return Err("column cannot store int64 value".to_owned());
                }
                Ok(ConvertedValue::Int(*integer))
            }
            Kind::DoubleValue(double) => {
                if !self.accepts_double() || !double.is_finite() {
                    return Err("column cannot store double value".to_owned());
                }
                Ok(ConvertedValue::Double(*double))
            }
            Kind::BoolValue(boolean) => {
                if !self.accepts_bool() {
                    return Err("column cannot store bool value".to_owned());
                }
                Ok(ConvertedValue::Bool(*boolean))
            }
            Kind::ObjValue(object) => self.convert_object(object),
            Kind::InternalBlobIdForStringValue(_) | Kind::InternalBlobIdForObjValue(_) => {
                Err("blob-backed value was not hydrated".to_owned())
            }
        }
    }

    pub(crate) fn accepts_string(&self) -> bool {
        matches!(
            self.data_type.as_str(),
            "character"
                | "character varying"
                | "text"
                | "char"
                | "varchar"
                | "tinytext"
                | "mediumtext"
                | "longtext"
                | "string"
        )
    }

    pub(crate) fn accepts_int(&self, value: i64) -> bool {
        match self.data_type.as_str() {
            "tinyint" | "byteint" => i64::from(i8::MIN) <= value && value <= i64::from(i8::MAX),
            "smallint" => i64::from(i16::MIN) <= value && value <= i64::from(i16::MAX),
            "mediumint" => (-8_388_608..=8_388_607).contains(&value),
            "integer" | "int" => i64::from(i32::MIN) <= value && value <= i64::from(i32::MAX),
            "bigint" => true,
            "number" | "numeric" | "decimal" | "fixed" => {
                if self.numeric_scale.is_some_and(|scale| scale != 0) {
                    return false;
                }
                let Some(precision) = self.numeric_precision else {
                    return true;
                };
                let mut digits = value.to_string().len() as i64;
                if value < 0 {
                    digits -= 1;
                }
                digits <= precision
            }
            _ => false,
        }
    }

    fn accepts_double(&self) -> bool {
        matches!(
            self.data_type.as_str(),
            "real"
                | "double precision"
                | "float"
                | "float4"
                | "float8"
                | "double"
                | "number"
                | "numeric"
                | "decimal"
                | "fixed"
        )
    }

    fn accepts_bool(&self) -> bool {
        self.data_type == "boolean" || self.data_type == "bool"
    }

    pub(crate) fn accepts_temporal(&self) -> bool {
        self.data_type == "timestamp with time zone"
    }

    pub(crate) fn accepts_json(&self) -> bool {
        self.data_type == "json" || self.data_type == "jsonb"
    }

    fn accepts_binary(&self) -> bool {
        self.data_type == "bytea"
    }

    fn convert_object(&self, object: &EncodedObject) -> Result<ConvertedValue, String> {
        if object.encoding == "json" {
            if serde_json::from_slice::<&RawValue>(&object.payload).is_err() {
                return Err("JSON payload is invalid".to_owned());
            }
            if self.accepts_temporal() {
                return parse_json_datetime(&object.payload).map(ConvertedValue::Timestamp);
            }
            if !self.accepts_json() {
                return Err("column does not accept JSON".to_owned());
            }
            let text = String::from_utf8(object.payload.clone())
                .map_err(|_| "JSON payload is invalid".to_owned())?;
            return Ok(ConvertedValue::Json(text));
        }
        if !self.accepts_binary() {
            return Err("column does not accept binary objects".to_owned());
        }
        if self
            .character_maximum
            .is_some_and(|maximum| object.payload.len() as i64 > maximum)
        {
            return Err("object exceeds column length".to_owned());
        }
        Ok(ConvertedValue::Bytes(object.payload.clone()))
    }

    /// Returns the parameter pgx would send for this column: a float4 column receives the value as float32.
    pub(crate) fn argument(&self, value: ConvertedValue) -> SqlArgument {
        match value {
            ConvertedValue::Null => SqlArgument::Null,
            ConvertedValue::Text(text) | ConvertedValue::Json(text) => SqlArgument::Text(text),
            ConvertedValue::Int(integer) => SqlArgument::Text(integer.to_string()),
            ConvertedValue::Double(double)
                if matches!(self.data_type.as_str(), "real" | "float4") =>
            {
                SqlArgument::Text(float4_text(double as f32))
            }
            ConvertedValue::Double(double) => SqlArgument::Text(double.to_string()),
            ConvertedValue::Bool(boolean) => SqlArgument::Text(boolean.to_string()),
            ConvertedValue::Timestamp(nanoseconds) => {
                let microseconds =
                    nanoseconds.div_euclid(1_000) - MICROSECONDS_FROM_UNIX_EPOCH_TO_Y2K;
                // Go's int64 arithmetic wraps; keeping the low 64 bits sends the same value.
                SqlArgument::TimestampMicrosecondsSinceY2k(microseconds as i64)
            }
            ConvertedValue::Bytes(payload) => {
                const HEX_DIGITS: &[u8; 16] = b"0123456789abcdef";
                let mut text = String::with_capacity(2 + payload.len() * 2);
                text.push_str("\\x");
                for byte in payload {
                    text.push(char::from(HEX_DIGITS[usize::from(byte >> 4)]));
                    text.push(char::from(HEX_DIGITS[usize::from(byte & 0x0F)]));
                }
                SqlArgument::Text(text)
            }
        }
    }
}

fn float4_text(value: f32) -> String {
    if value.is_infinite() {
        return if value > 0.0 { "Infinity" } else { "-Infinity" }.to_owned();
    }
    value.to_string()
}

impl SqlArgument {
    /// Returns the declared parameter type; UNKNOWN lets Postgres infer the column type.
    pub(crate) fn declared_type(&self) -> Type {
        match self {
            Self::TimestampMicrosecondsSinceY2k(_) => Type::TIMESTAMPTZ,
            Self::Null | Self::Text(_) => Type::UNKNOWN,
        }
    }
}

impl ToSql for SqlArgument {
    fn to_sql(
        &self,
        _type: &Type,
        out: &mut BytesMut,
    ) -> Result<IsNull, Box<dyn Error + Sync + Send>> {
        match self {
            Self::Null => Ok(IsNull::Yes),
            Self::Text(text) => {
                out.put_slice(text.as_bytes());
                Ok(IsNull::No)
            }
            Self::TimestampMicrosecondsSinceY2k(microseconds) => {
                out.put_i64(*microseconds);
                Ok(IsNull::No)
            }
        }
    }

    fn accepts(_type: &Type) -> bool {
        true
    }

    fn encode_format(&self, _type: &Type) -> Format {
        match self {
            Self::TimestampMicrosecondsSinceY2k(_) => Format::Binary,
            Self::Null | Self::Text(_) => Format::Text,
        }
    }

    to_sql_checked!();
}

/// Go's `time.Parse(time.RFC3339Nano, value)`, returning nanoseconds since the Unix epoch.
pub(crate) fn parse_datetime(value: &str) -> Result<i128, String> {
    parse_rfc3339(value).ok_or_else(|| format!("datetime must use RFC3339: cannot parse {value:?}"))
}

fn parse_rfc3339(value: &str) -> Option<i128> {
    let bytes = value.as_bytes();
    if bytes.len() < "2006-01-02T15:04:05".len()
        || bytes[4] != b'-'
        || bytes[7] != b'-'
        || bytes[10] != b'T'
        || bytes[13] != b':'
        || bytes[16] != b':'
    {
        return None;
    }
    let year = parse_digits(&bytes[0..4])?;
    let month = Month::try_from(u8::try_from(parse_digits(&bytes[5..7])?).ok()?).ok()?;
    let day = u8::try_from(parse_digits(&bytes[8..10])?).ok()?;
    let date = Date::from_calendar_date(i32::try_from(year).ok()?, month, day).ok()?;
    let hour = bounded(parse_digits(&bytes[11..13])?, 23)?;
    let minute = bounded(parse_digits(&bytes[14..16])?, 59)?;
    let second = bounded(parse_digits(&bytes[17..19])?, 59)?;
    let mut rest = &bytes[19..];
    let mut nanoseconds = 0;
    if rest.len() >= 2 && matches!(rest[0], b'.' | b',') && rest[1].is_ascii_digit() {
        let digit_count = rest[1..]
            .iter()
            .take_while(|byte| byte.is_ascii_digit())
            .count();
        let mut fraction = rest[1..1 + digit_count.min(9)].to_vec();
        fraction.resize(9, b'0');
        nanoseconds = parse_digits(&fraction)?;
        rest = &rest[1 + digit_count..];
    }
    let offset_seconds = match rest {
        [b'Z'] => 0,
        [
            sign @ (b'+' | b'-'),
            hour_tens,
            hour_ones,
            b':',
            minute_tens,
            minute_ones,
        ] => {
            // Go's general parser accepts offsets of 24 hours or 60 minutes.
            let offset_hour = bounded(parse_digits(&[*hour_tens, *hour_ones])?, 24)?;
            let offset_minute = bounded(parse_digits(&[*minute_tens, *minute_ones])?, 60)?;
            let magnitude = (offset_hour * 60 + offset_minute) * 60;
            if *sign == b'-' { -magnitude } else { magnitude }
        }
        _ => return None,
    };
    let days = i64::from(date.to_julian_day()) - UNIX_EPOCH_JULIAN_DAY;
    let seconds = days * 86_400 + hour * 3_600 + minute * 60 + second - offset_seconds;
    Some(i128::from(seconds) * 1_000_000_000 + i128::from(nanoseconds))
}

fn parse_digits(digits: &[u8]) -> Option<i64> {
    if digits.is_empty() || !digits.iter().all(u8::is_ascii_digit) {
        return None;
    }
    std::str::from_utf8(digits).ok()?.parse().ok()
}

fn bounded(value: i64, maximum: i64) -> Option<i64> {
    (value <= maximum).then_some(value)
}

/// Go's `parseJSONDatetime`: a JSON string in RFC3339 or a JSON number of epoch seconds.
fn parse_json_datetime(payload: &[u8]) -> Result<i128, String> {
    let text =
        std::str::from_utf8(payload).map_err(|error| format!("decode JSON datetime: {error}"))?;
    let token = text.trim_matches([' ', '\t', '\n', '\r']);
    match token.as_bytes().first() {
        Some(b'"') => {
            let datetime = serde_json::from_str::<String>(token)
                .map_err(|error| format!("decode JSON datetime: {error}"))?;
            parse_datetime(&datetime)
        }
        Some(b'-' | b'0'..=b'9') => parse_unix_datetime(token),
        _ => Err("datetime payload must be a JSON string or epoch-seconds number".to_owned()),
    }
}

/// Go's `parseUnixDatetime` over the JSON number's literal text.
fn parse_unix_datetime(value: &str) -> Result<i128, String> {
    let is_negative = value.starts_with('-');
    let unsigned = value.strip_prefix('-').unwrap_or(value);
    let parts = unsigned.split('.').collect::<Vec<_>>();
    if parts.len() > 2 || parts[0].is_empty() {
        return Err("datetime epoch seconds are invalid".to_owned());
    }
    let mut seconds = parts[0]
        .parse::<i64>()
        .map_err(|error| format!("datetime epoch seconds are invalid: {error}"))?;
    let mut nanoseconds = 0_i64;
    if let Some(fraction) = parts.get(1) {
        if fraction.is_empty() || fraction.len() > 9 {
            return Err("datetime epoch fraction must contain one to nine digits".to_owned());
        }
        nanoseconds = format!("{fraction:0<9}")
            .parse::<i64>()
            .map_err(|error| format!("datetime epoch fraction is invalid: {error}"))?;
    }
    if is_negative {
        seconds = -seconds;
        nanoseconds = -nanoseconds;
    }
    Ok(i128::from(seconds) * 1_000_000_000 + i128::from(nanoseconds))
}
