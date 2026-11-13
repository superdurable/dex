// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::Arc;

use prometheus_client::encoding::{EncodeLabelSet, EncodeMetric, MetricEncoder};
use prometheus_client::metrics::MetricType;
use prometheus_client::metrics::counter::Counter;

/// The four object storage operations exposed by the metric.
#[derive(Clone, Copy, Debug, Hash, PartialEq, Eq)]
pub enum ObjectOperation {
    /// Upload or conditional write.
    Put,
    /// Read, head, or download.
    Get,
    /// Key listing.
    List,
    /// Single or batch deletion.
    Delete,
}

label_value!(ObjectOperation { Put => "put", Get => "get", List => "list", Delete => "delete" });

impl ObjectOperation {
    /// Every operation in the fixed metric set.
    pub const ALL: [Self; 4] = [Self::Put, Self::Get, Self::List, Self::Delete];

    const fn index(self) -> usize {
        match self {
            Self::Put => 0,
            Self::Get => 1,
            Self::List => 2,
            Self::Delete => 3,
        }
    }
}

/// The fixed object storage layout class.
#[derive(Clone, Copy, Debug, Hash, PartialEq, Eq)]
pub enum ObjectKind {
    /// Commit log object.
    Log,
    /// Snapshot file or snapshot manifest.
    Snapshot,
    /// Cluster manifest.
    Manifest,
    /// Node lease.
    Lease,
    /// History segment object.
    History,
    /// Cluster metadata or value object.
    Other,
}

label_value!(ObjectKind { Log => "log", Snapshot => "snapshot", Manifest => "manifest", Lease => "lease", History => "history", Other => "other" });

impl ObjectKind {
    const ALL: [Self; 6] = [
        Self::Log,
        Self::Snapshot,
        Self::Manifest,
        Self::Lease,
        Self::History,
        Self::Other,
    ];

    const fn index(self) -> usize {
        match self {
            Self::Log => 0,
            Self::Snapshot => 1,
            Self::Manifest => 2,
            Self::Lease => 3,
            Self::History => 4,
            Self::Other => 5,
        }
    }
}

/// Classifies a key or listing prefix from the fixed object storage layout.
pub fn classify_object_kind(key: &str) -> ObjectKind {
    if key.starts_with("log/") || key == "log" {
        ObjectKind::Log
    } else if key.starts_with("snapshot/") || key == "snapshot" {
        ObjectKind::Snapshot
    } else if key == "manifest" {
        ObjectKind::Manifest
    } else if key.starts_with("lease/") || key == "lease" {
        ObjectKind::Lease
    } else if key.starts_with("history/") || key == "history" {
        ObjectKind::History
    } else {
        ObjectKind::Other
    }
}

#[derive(Clone, Debug, EncodeLabelSet)]
struct ObjectLabel {
    operation: ObjectOperation,
    object_kind: ObjectKind,
}

/// Fixed atomic object request counters, emitting only observed label pairs.
#[derive(Clone, Debug)]
pub struct ObjectRequests {
    counts: Arc<[[Counter; 6]; 4]>,
}

impl Default for ObjectRequests {
    fn default() -> Self {
        Self {
            counts: Arc::new(std::array::from_fn(|_| {
                std::array::from_fn(|_| Counter::default())
            })),
        }
    }
}

impl ObjectRequests {
    /// Counts one logical request without allocating or locking.
    pub fn record(&self, operation: ObjectOperation, object_kind: ObjectKind) {
        self.counts[operation.index()][object_kind.index()].inc();
    }
}

impl EncodeMetric for ObjectRequests {
    fn encode(&self, mut encoder: MetricEncoder) -> Result<(), std::fmt::Error> {
        for operation in ObjectOperation::ALL {
            for object_kind in ObjectKind::ALL {
                let count = self.counts[operation.index()][object_kind.index()].get();
                if count == 0
                    && !(operation == ObjectOperation::Put && object_kind == ObjectKind::Log)
                {
                    continue;
                }
                encoder
                    .encode_family(&ObjectLabel {
                        operation,
                        object_kind,
                    })?
                    .encode_counter::<prometheus_client::encoding::NoLabelSet, _, u64>(
                        &count, None,
                    )?;
            }
        }
        Ok(())
    }

    fn metric_type(&self) -> MetricType {
        MetricType::Counter
    }
}

/// Where a listing of a versioned mutable key missed a version that this process created.
#[derive(Clone, Copy, Debug, Hash, PartialEq, Eq)]
pub enum VersionedListingStaleSite {
    /// The listing right after a create did not show the created version as the newest.
    ConfirmCreatedVersion,
    /// The reclamation listing after a confirmed write did not start at the confirmed version.
    Reclamation,
    /// With HEAD-probing version discovery, HEAD requests found a newer version than the
    /// listing that gave their lower bound.
    NewestVersionLookup,
}

label_value!(VersionedListingStaleSite { ConfirmCreatedVersion => "confirm_created_version", Reclamation => "reclamation", NewestVersionLookup => "newest_version_lookup" });

impl VersionedListingStaleSite {
    const ALL: [Self; 3] = [
        Self::ConfirmCreatedVersion,
        Self::Reclamation,
        Self::NewestVersionLookup,
    ];

    const fn index(self) -> usize {
        match self {
            Self::ConfirmCreatedVersion => 0,
            Self::Reclamation => 1,
            Self::NewestVersionLookup => 2,
        }
    }
}

#[derive(Clone, Debug, Hash, PartialEq, Eq, EncodeLabelSet)]
struct VersionedListingStaleLabel {
    site: VersionedListingStaleSite,
}

/// Fixed per-site counters of listings that missed a version this process had just created.
#[derive(Clone, Debug)]
pub struct VersionedListingStaleCounters {
    values: Arc<[Counter; 3]>,
}

impl Default for VersionedListingStaleCounters {
    fn default() -> Self {
        Self {
            values: Arc::new(std::array::from_fn(|_| Counter::default())),
        }
    }
}

impl VersionedListingStaleCounters {
    /// Counts one listing at `site` that did not show the version this process created.
    pub fn record(&self, site: VersionedListingStaleSite) {
        self.values[site.index()].inc();
    }
}

impl EncodeMetric for VersionedListingStaleCounters {
    fn encode(&self, mut encoder: MetricEncoder) -> Result<(), std::fmt::Error> {
        for site in VersionedListingStaleSite::ALL {
            encoder
                .encode_family(&VersionedListingStaleLabel { site })?
                .encode_counter::<prometheus_client::encoding::NoLabelSet, _, u64>(
                    &self.values[site.index()].get(),
                    None,
                )?;
        }
        Ok(())
    }

    fn metric_type(&self) -> MetricType {
        MetricType::Counter
    }
}

#[cfg(test)]
mod tests {
    use super::{ObjectKind, classify_object_kind};

    #[test]
    fn real_layout_keys_have_closed_kinds() {
        for (key, expected) in [
            (
                "log/node-a/0000000001/00000000000000000042",
                ObjectKind::Log,
            ),
            (
                "snapshot/node-a/lineage/files/file.sst",
                ObjectKind::Snapshot,
            ),
            (
                "snapshot/node-a/lineage/manifest/00000000000000000001",
                ObjectKind::Snapshot,
            ),
            ("manifest", ObjectKind::Manifest),
            ("lease/node-a", ObjectKind::Lease),
            (
                "history/0000000000000000/00000000000000ab/0192f3a4-5b6c-7d8e-9fa0-b1c2d3e4f5a6/\
                 00000000000000000001-00000000000000000009",
                ObjectKind::History,
            ),
            ("cluster", ObjectKind::Other),
            ("hard-delete/tenant/value", ObjectKind::Other),
        ] {
            assert_eq!(classify_object_kind(key), expected, "{key}");
        }
    }
}
