// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::time::{Duration, SystemTime};
use tokio::time::Instant;

// The protobuf Timestamp range: 0001-01-01T00:00:00Z through 9999-12-31T23:59:59Z.
const MINIMUM_TIMESTAMP_SECONDS: i64 = -62_135_596_800;
const MAXIMUM_TIMESTAMP_SECONDS: i64 = 253_402_300_799;

/// Returns the local instant for a valid task deadline; a deadline in the past returns now.
/// Returns an error naming the invalid field when nanoseconds fall outside `0..1_000_000_000`,
/// seconds fall outside the protobuf Timestamp range, or the instant cannot be represented.
pub(super) fn task_deadline(timestamp: &prost_types::Timestamp) -> Result<Instant, String> {
    if !(0..1_000_000_000).contains(&timestamp.nanos) {
        return Err(format!(
            "task deadline nanos {} out of range",
            timestamp.nanos
        ));
    }
    if !(MINIMUM_TIMESTAMP_SECONDS..=MAXIMUM_TIMESTAMP_SECONDS).contains(&timestamp.seconds) {
        return Err(format!(
            "task deadline seconds {} out of range",
            timestamp.seconds
        ));
    }
    let current = Instant::now();
    if timestamp.seconds < 0 {
        return Ok(current);
    }
    let deadline = SystemTime::UNIX_EPOCH
        .checked_add(Duration::from_secs(timestamp.seconds as u64))
        .and_then(|time| time.checked_add(Duration::from_nanos(timestamp.nanos as u64)))
        .ok_or_else(|| {
            format!(
                "task deadline seconds {} not representable",
                timestamp.seconds
            )
        })?;
    current
        .checked_add(
            deadline
                .duration_since(SystemTime::now())
                .unwrap_or_default(),
        )
        .ok_or_else(|| {
            format!(
                "task deadline seconds {} not representable",
                timestamp.seconds
            )
        })
}
