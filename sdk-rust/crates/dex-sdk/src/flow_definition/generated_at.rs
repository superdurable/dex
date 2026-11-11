// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::time::SystemTime;

use prost_types::Timestamp;

const NANOS_PER_MILLI: i32 = 1_000_000;

/// Parses a strict RFC 3339 `generatedAt` and truncates it to whole milliseconds.
///
/// Returns an error message for anything other than `YYYY-MM-DDTHH:MM:SS[.fraction]` followed by
/// `Z` or `±HH:MM`, with `T` and `Z` in either case.
pub(super) fn parse_generated_at(value: &str) -> Result<SystemTime, String> {
    let invalid = || format!("generatedAt {value} is not an RFC 3339 time");
    if !is_strict_rfc_3339(value.as_bytes()) {
        return Err(invalid());
    }
    let timestamp = value
        .to_ascii_uppercase()
        .parse::<Timestamp>()
        .map_err(|_| invalid())?;
    SystemTime::try_from(truncate_timestamp_to_millis(timestamp)).map_err(|_| invalid())
}

/// Returns the time truncated to whole milliseconds.
pub(super) fn truncate_to_millis(time: SystemTime) -> SystemTime {
    let truncated = truncate_timestamp_to_millis(Timestamp::from(time));
    SystemTime::try_from(truncated).expect("truncating a SystemTime keeps it in range")
}

pub(super) fn truncate_timestamp_to_millis(mut timestamp: Timestamp) -> Timestamp {
    timestamp.normalize();
    timestamp.nanos -= timestamp.nanos.rem_euclid(NANOS_PER_MILLI);
    timestamp
}

fn is_strict_rfc_3339(bytes: &[u8]) -> bool {
    let is_digits = |range: std::ops::Range<usize>| {
        bytes
            .get(range)
            .is_some_and(|digits| digits.iter().all(u8::is_ascii_digit))
    };
    let has = |index: usize, expected: &[u8]| {
        bytes.get(index).is_some_and(|byte| expected.contains(byte))
    };
    if !(is_digits(0..4)
        && has(4, b"-")
        && is_digits(5..7)
        && has(7, b"-")
        && is_digits(8..10)
        && has(10, b"Tt")
        && is_digits(11..13)
        && has(13, b":")
        && is_digits(14..16)
        && has(16, b":")
        && is_digits(17..19))
    {
        return false;
    }
    let mut index = 19;
    if has(index, b".") {
        let fraction_start = index + 1;
        index = fraction_start;
        while bytes.get(index).is_some_and(u8::is_ascii_digit) {
            index += 1;
        }
        if index == fraction_start {
            return false;
        }
    }
    let offset = &bytes[index..];
    match offset {
        [b'Z' | b'z'] => true,
        [b'+' | b'-', ..] => {
            offset.len() == 6
                && offset[1..3].iter().all(u8::is_ascii_digit)
                && offset[3] == b':'
                && offset[4..6].iter().all(u8::is_ascii_digit)
        }
        _ => false,
    }
}
