// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::{BTreeMap, BTreeSet};

use dexzd_keyspace::{ColumnFamily, PartitionId};
use dexzd_metrics::ProjectionRebuildReason;
use dexzd_storage::{ScanDirection, WriteBatch};

use super::{PendingRunStore, PendingStoreError};

const REASONS: [ProjectionRebuildReason; 5] = [
    ProjectionRebuildReason::StartupBehind,
    ProjectionRebuildReason::RepairOverflow,
    ProjectionRebuildReason::DeleteOverflow,
    ProjectionRebuildReason::Divergence,
    ProjectionRebuildReason::Unreadable,
];

impl PendingRunStore {
    pub(crate) fn load_rebuild_markers(
        &self,
    ) -> Result<BTreeMap<PartitionId, BTreeSet<ProjectionRebuildReason>>, PendingStoreError> {
        self.local
            .scan_prefix(
                ColumnFamily::NodeMeta,
                &self.prefix(b'm'),
                ScanDirection::Forward,
                usize::MAX,
            )?
            .into_iter()
            .map(|(key, value)| {
                let bytes = key.get(self.namespace.len() + 1..).ok_or_else(|| {
                    PendingStoreError::Invalid("truncated rebuild marker key".into())
                })?;
                let partition =
                    PartitionId(u16::from_be_bytes(bytes.try_into().map_err(|_| {
                        PendingStoreError::Invalid("invalid rebuild marker partition".into())
                    })?));
                let [mask] = value.as_slice() else {
                    return Err(PendingStoreError::Invalid(
                        "invalid rebuild reason mask".into(),
                    ));
                };
                if mask & !31 != 0 {
                    return Err(PendingStoreError::Invalid(
                        "unknown rebuild reason mask".into(),
                    ));
                }
                Ok((
                    partition,
                    REASONS
                        .into_iter()
                        .filter(|reason| mask & (1 << (*reason as u8)) != 0)
                        .collect(),
                ))
            })
            .collect()
    }

    pub(crate) fn store_rebuild_markers(
        &self,
        markers: &BTreeMap<PartitionId, BTreeSet<ProjectionRebuildReason>>,
    ) -> Result<(), PendingStoreError> {
        let previous = self.load_rebuild_markers()?;
        if &previous == markers {
            return Ok(());
        }
        let mut batch = WriteBatch::new();
        for partition in previous.keys().chain(markers.keys()) {
            let mut key = self.prefix(b'm');
            key.extend_from_slice(&partition.0.to_be_bytes());
            match markers.get(partition) {
                Some(reasons) => {
                    let mask = reasons
                        .iter()
                        .fold(0_u8, |mask, reason| mask | (1 << (*reason as u8)));
                    batch.put(ColumnFamily::NodeMeta, key, vec![mask]);
                }
                None => batch.delete(ColumnFamily::NodeMeta, key),
            }
        }
        self.local.write(batch)?;
        Ok(())
    }
}
