// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//! Settings shared by the Worker, the start command, the service, and the tests.

use std::path::Path;
use std::sync::Arc;
use std::time::Duration;

use dex_sdk::{BlobCache, BlobCacheConfig};

use crate::shop_api::ShopApi;

/// Where the Worker and the Client reach Dex, and how the demo behaves.
pub struct QuickStartConfig {
    pub server_address: String,
    /// The service's port, from `QUICK_START_HTTP_PORT`; 8080 by default.
    pub http_port: u16,
    /// The pause after a new charge, from `QUICK_START_CHARGE_DELAY_SECONDS`; none by default.
    pub charge_delay: Duration,
}

impl QuickStartConfig {
    /// Reads `DEX_FLOW_SERVICE_ADDRESS`, defaulting to the `dexcli dev` address, and the demo settings.
    pub fn from_environment() -> Self {
        let server_address = std::env::var("DEX_FLOW_SERVICE_ADDRESS")
            .unwrap_or_else(|_| "127.0.0.1:8801".to_string());
        let http_port = std::env::var("QUICK_START_HTTP_PORT")
            .ok()
            .and_then(|port| port.parse().ok())
            .unwrap_or(8080);
        let charge_delay_seconds: f64 = std::env::var("QUICK_START_CHARGE_DELAY_SECONDS")
            .ok()
            .and_then(|seconds| seconds.parse().ok())
            .unwrap_or(0.0);
        Self {
            server_address,
            http_port,
            charge_delay: Duration::from_secs_f64(charge_delay_seconds),
        }
    }

    /// Returns the mock API that the Worker calls, with its keys in a local file.
    pub fn new_shop_api(&self) -> ShopApi {
        ShopApi::with_keys_file(".shop-api-keys", self.charge_delay)
    }
}

/// How long Ship waits, from `QUICK_START_SHIP_DELAY_SECONDS`; 60 seconds by default.
pub fn ship_delay_from_environment() -> Duration {
    let seconds = std::env::var("QUICK_START_SHIP_DELAY_SECONDS")
        .ok()
        .and_then(|seconds| seconds.parse().ok())
        .unwrap_or(60);
    Duration::from_secs(seconds)
}

/// Opens a local cache for large Flow values, up to 1 GiB.
pub fn open_blob_cache(directory: impl AsRef<Path>) -> std::io::Result<Arc<BlobCache>> {
    let directory = directory.as_ref();
    let open_error = |error| {
        std::io::Error::other(format!(
            "open the blob cache at {}: {error}",
            directory.display()
        ))
    };
    let config = BlobCacheConfig::new(directory, 1 << 30, 0).map_err(open_error)?;
    Ok(Arc::new(BlobCache::open(config).map_err(open_error)?))
}
