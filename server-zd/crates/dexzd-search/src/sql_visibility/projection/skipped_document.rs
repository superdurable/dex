// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dexzd_keyspace::{PartitionId, RunId, TenantId};

use super::ProjectionMetricRecorder;

pub(crate) struct SkippedSearchDocument {
    pub(crate) partition_id: PartitionId,
    pub(crate) tenant_id: TenantId,
    pub(crate) run_id: RunId,
    pub(crate) reason: String,
}

pub(crate) fn record_skipped_documents(
    skipped_documents: Vec<SkippedSearchDocument>,
    metric_recorder: Option<&dyn ProjectionMetricRecorder>,
) {
    for skipped in skipped_documents {
        tracing::error!(
            partition_id = skipped.partition_id.0,
            tenant_id = skipped.tenant_id.0,
            run_id = %skipped.run_id,
            reason = %skipped.reason,
            "committed search document omitted from SQL visibility projection"
        );
        if let Some(recorder) = metric_recorder {
            recorder.record_skipped_search_document(
                skipped.partition_id,
                skipped.tenant_id,
                skipped.run_id,
            );
        }
    }
}
