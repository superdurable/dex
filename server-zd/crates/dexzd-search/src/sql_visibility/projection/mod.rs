// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

mod changes;
mod outbox_reader;
mod progress;
mod projector;
mod skipped_document;

pub(crate) use skipped_document::{SkippedSearchDocument, record_skipped_documents};

pub use changes::SearchDocumentChange;
pub use outbox_reader::{
    InitialLoadPage, OutboxEntry, read_initial_load_page, read_outbox_entries, read_run_change,
};
pub use projector::{
    PartitionProjectionBatch, ProjectionError, ProjectionMetricRecorder, ProjectionProgress,
    SqlVisibilityProjector,
};

#[cfg(test)]
mod integration;

#[cfg(test)]
pub(super) use integration::run_projector_scenarios_for_test;
