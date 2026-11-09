// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::fmt;
use std::time::Duration;

/// Connection and namespace settings for the db9 SQL object provider.
#[derive(Clone)]
pub struct Db9ObjectStoreConfig {
    /// PostgreSQL connection string; never included in Debug output.
    pub connection_string: String,
    /// Logical bucket; isolates keys in the shared provider tables.
    pub bucket: String,
    /// Prefix prepended to keys inside the logical bucket.
    pub key_prefix: String,
    /// Maximum simultaneous SQL transactions. Defaults to eight.
    pub pool_size: usize,
    /// Deadline per SQL operation. Defaults to ten seconds.
    pub request_timeout: Duration,
}

impl Db9ObjectStoreConfig {
    /// Creates settings with eight connections and a ten-second SQL deadline.
    pub fn new(connection_string: String, bucket: String) -> Self {
        Self {
            connection_string,
            bucket,
            key_prefix: String::new(),
            pool_size: 8,
            request_timeout: Duration::from_secs(10),
        }
    }
}

impl fmt::Debug for Db9ObjectStoreConfig {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("Db9ObjectStoreConfig")
            .field("connection_string", &"[redacted]")
            .field("bucket", &self.bucket)
            .field("key_prefix", &self.key_prefix)
            .field("pool_size", &self.pool_size)
            .field("request_timeout", &self.request_timeout)
            .finish()
    }
}
