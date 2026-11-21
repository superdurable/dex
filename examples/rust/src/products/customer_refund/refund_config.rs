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

//! Settings shared by the refund Worker, the start and decide commands, and the tests.

use std::fmt::{self, Display, Formatter};
use std::path::Path;
use std::str::FromStr;
use std::sync::Arc;

use dex_sdk::{BlobCache, BlobCacheConfig};

/// Chooses whether `IssueRefund` reaches billing.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum BillingMode {
    #[default]
    Normal,
    /// Stands in for a release whose Issue refund Step forgot the billing call.
    CallMissing,
}

impl FromStr for BillingMode {
    type Err = String;

    fn from_str(value: &str) -> Result<Self, Self::Err> {
        match value {
            "normal" => Ok(Self::Normal),
            "call-missing" => Ok(Self::CallMissing),
            other => Err(format!(
                "REFUND_BILLING_MODE must be normal or call-missing, not {other:?}"
            )),
        }
    }
}

impl Display for BillingMode {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> fmt::Result {
        formatter.write_str(match self {
            Self::Normal => "normal",
            Self::CallMissing => "call-missing",
        })
    }
}

pub struct RefundConfig {
    /// The Dex address, from `DEX_FLOW_SERVICE_ADDRESS`; the `dexcli dev` address by default.
    pub server_address: String,
}

impl RefundConfig {
    pub fn from_environment() -> Self {
        let server_address = std::env::var("DEX_FLOW_SERVICE_ADDRESS")
            .unwrap_or_else(|_| "127.0.0.1:8801".to_string());
        Self { server_address }
    }
}

/// The Worker's settings: the shared ones plus the billing mode.
pub struct RefundWorkerConfig {
    pub refund: RefundConfig,
    /// From `REFUND_BILLING_MODE`: `normal` (default) or `call-missing`.
    pub billing_mode: BillingMode,
}

impl RefundWorkerConfig {
    /// Reads the environment, or returns an error for an unknown billing mode.
    pub fn from_environment() -> Result<Self, String> {
        let billing_mode = match std::env::var("REFUND_BILLING_MODE") {
            Ok(value) => value.parse()?,
            Err(_) => BillingMode::Normal,
        };
        Ok(Self {
            refund: RefundConfig::from_environment(),
            billing_mode,
        })
    }
}

/// Returns a Worker ID without the host name; it changes on every start.
pub fn new_refund_worker_id() -> String {
    format!("refund-worker-{:06x}", fastrand::u32(..0x0100_0000))
}

/// Opens a local cache for large Flow values, up to 1 GiB.
pub fn open_refund_blob_cache(directory: impl AsRef<Path>) -> std::io::Result<Arc<BlobCache>> {
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
