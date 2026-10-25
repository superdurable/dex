// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::time::{Duration, SystemTime};
use tokio::time::Instant;

pub(super) fn task_deadline(timestamp: &prost_types::Timestamp) -> Instant {
    let current = Instant::now();
    if timestamp.seconds < 0 || !(0..1_000_000_000).contains(&timestamp.nanos) {
        return current;
    }
    let Some(deadline) = SystemTime::UNIX_EPOCH
        .checked_add(Duration::from_secs(timestamp.seconds as u64))
        .and_then(|time| time.checked_add(Duration::from_nanos(timestamp.nanos as u64)))
    else {
        return current;
    };
    current
        .checked_add(
            deadline
                .duration_since(SystemTime::now())
                .unwrap_or_default(),
        )
        .unwrap_or(current)
}
