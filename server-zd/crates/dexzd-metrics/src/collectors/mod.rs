// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

#[cfg(target_os = "linux")]
pub(crate) mod process;

use std::path::PathBuf;

use prometheus_client::metrics::gauge::Gauge;

pub(crate) const LOCAL_STORAGE_FAMILY_NAME: &str = "dexzd_local_storage_bytes";

#[derive(Debug)]
pub(crate) struct LocalStorageCollector {
    pub(crate) directory: PathBuf,
    pub(crate) gauge: Gauge,
}

impl prometheus_client::collector::Collector for LocalStorageCollector {
    fn encode(
        &self,
        mut encoder: prometheus_client::encoding::DescriptorEncoder,
    ) -> Result<(), std::fmt::Error> {
        match local_storage_bytes(&self.directory) {
            Ok(bytes) => {
                self.gauge.set(i64::try_from(bytes).unwrap_or(i64::MAX));
            }
            Err(error) => tracing::warn!(%error, "could not sample local storage bytes"),
        }
        let metric_encoder = encoder.encode_descriptor(
            LOCAL_STORAGE_FAMILY_NAME,
            "Local SST and WAL bytes.",
            None,
            prometheus_client::metrics::MetricType::Gauge,
        )?;
        prometheus_client::encoding::EncodeMetric::encode(&self.gauge, metric_encoder)
    }
}

fn local_storage_bytes(directory: &std::path::Path) -> std::io::Result<u64> {
    let mut size = 0_u64;
    let mut pending = vec![directory.to_path_buf()];
    while let Some(path) = pending.pop() {
        for entry in std::fs::read_dir(path)? {
            let entry = entry?;
            if entry.file_type()?.is_dir() {
                pending.push(entry.path());
            } else if matches!(
                entry
                    .path()
                    .extension()
                    .and_then(|extension| extension.to_str()),
                Some("sst" | "log")
            ) {
                size = size.saturating_add(entry.metadata()?.len());
            }
        }
    }
    Ok(size)
}
