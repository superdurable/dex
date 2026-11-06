// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::time::Duration;

/// Configuration for one in-memory Stream Store.
#[derive(Clone, Debug)]
pub struct StreamStoreConfig {
    /// Enables writes and reads. Default false, matching the production Go Server.
    pub enabled: bool,
    /// Supplies capacity for internal writes without one. Default 0 rejects missing capacity.
    pub capacity_per_stream_bytes: u64,
    /// Limits the serialized Value size. Default 100 KiB.
    pub max_message_bytes: usize,
    /// Limits one reverse-list page. Default 1000.
    pub max_read_messages: usize,
    /// Starts background trimming at this percentage. Default 90.
    pub trim_trigger_percent: u8,
    /// Stops background trimming at this percentage. Default 80.
    pub trim_target_percent: u8,
    /// Bounds the bytes retained across every Stream in the process. Default 1 GiB.
    pub max_total_bytes: u64,
    /// Clamps a caller-supplied Stream capacity to this many bytes. Default 64 MiB.
    pub max_stream_capacity_bytes: u64,
    /// Accounting added to each message beyond its Value, Flow ID, and source. Default 512.
    pub estimated_message_overhead_bytes: u64,
    /// Limits the messages removed in one atomic trim batch. Default 256.
    pub background_trim_batch_size: usize,
    /// Pauses between trim batches of one job. Default 1 ms.
    pub trim_batch_yield_time: Duration,
    /// Limits the concurrent background trim jobs. Default 4.
    pub trim_workers: usize,
}

impl Default for StreamStoreConfig {
    fn default() -> Self {
        Self {
            enabled: false,
            capacity_per_stream_bytes: 0,
            max_message_bytes: 100 * 1024,
            max_read_messages: 1000,
            trim_trigger_percent: 90,
            trim_target_percent: 80,
            max_total_bytes: 1 << 30,
            max_stream_capacity_bytes: 64 << 20,
            estimated_message_overhead_bytes: 512,
            background_trim_batch_size: 256,
            trim_batch_yield_time: Duration::from_millis(1),
            trim_workers: 4,
        }
    }
}

impl StreamStoreConfig {
    pub(crate) fn validate(&self) -> Result<(), &'static str> {
        if self.max_message_bytes == 0 {
            return Err("max_message_bytes must be positive");
        }
        if !(1..=1000).contains(&self.max_read_messages) {
            return Err("max_read_messages must be between 1 and 1000");
        }
        if !(1..=99).contains(&self.trim_trigger_percent) {
            return Err("trim_trigger_percent must be between 1 and 99");
        }
        if self.trim_target_percent == 0 || self.trim_target_percent >= self.trim_trigger_percent {
            return Err("trim_target_percent must be positive and below trim_trigger_percent");
        }
        if self.max_total_bytes == 0 {
            return Err("max_total_bytes must be positive");
        }
        if self.max_stream_capacity_bytes == 0
            || self.max_stream_capacity_bytes > self.max_total_bytes
        {
            return Err("max_stream_capacity_bytes must be positive and at most max_total_bytes");
        }
        if self.background_trim_batch_size == 0 {
            return Err("background_trim_batch_size must be positive");
        }
        if self.trim_batch_yield_time.is_zero() {
            return Err("trim_batch_yield_time must be positive");
        }
        if self.trim_workers == 0 {
            return Err("trim_workers must be positive");
        }
        Ok(())
    }
}
