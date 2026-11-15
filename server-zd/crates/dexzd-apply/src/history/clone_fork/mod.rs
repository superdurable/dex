// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! CloneForkFlow: a new Flow whose first run starts from a copy of another run's state.
//!
//! The owner stages the copy on the new Flow's partition; apply never reads the source run.

mod computation;
mod copied_values;
mod run_creation;
mod staging;

pub use computation::{
    CloneForkComputation, CloneForkComputationRequest, CloneForkHardDeleteAttribute,
    ComputedCloneFork, SegmentedCloneForkComputation, compute_clone_fork,
    compute_clone_fork_from_segments,
};
pub(crate) use run_creation::apply_flow_clone_fork;
pub use staging::CloneForkStagedEntryTooLarge;
pub(crate) use staging::{apply_flow_clone_fork_stage, apply_flow_clone_fork_staging_expire};
