// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

mod changes;
mod checkpoints;
pub use checkpoints::PartitionCheckpoint;
mod progress;
mod projector;
mod repair;
mod skipped_document;

pub(crate) use skipped_document::{SkippedSearchDocument, record_skipped_documents};

pub use changes::{
    SearchDocumentChange, extract_batch_search_document_changes, extract_search_document_changes,
};
pub use projector::{ProjectionError, ProjectionMetricRecorder, SqlVisibilityProjector};
pub use repair::{PartitionDocumentRepairs, ProjectedRunVersion};

#[cfg(test)]
mod integration;

#[cfg(test)]
pub(super) use integration::run_projector_scenarios_for_test;
