// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! History H, attempt checkpoints, the time travel window, and the TimeTravel command (plan
//! sections 7 and 8.4).

mod attempt_scope;
mod checkpoints;
pub mod diagnostics;
mod lineage;
mod replay_state;
mod reset_target;
mod state_rebuild;
mod time_travel;
mod window;

pub(crate) use checkpoints::{after_command, pending_step_methods};
pub use lineage::{HistoryRecords, HistorySource, StateHistorySource};
pub(crate) use lineage::{LocalHistorySource, genesis_seq};
pub use time_travel::{
    ComputedTimeTravel, SegmentedTimeTravelComputation, StagedEntryTooLarge, TimeTravelComputation,
    TimeTravelComputationRequest, TimeTravelComputationSources, compute_time_travel,
    compute_time_travel_from_segments,
};
#[cfg(feature = "test-util")]
pub use time_travel::{
    StagedTimeTravelForTest, apply_staged_time_travel_for_test, is_unstaged_time_travel_for_test,
    stage_computed_time_travel_for_test, stage_time_travel_for_test,
};
pub(crate) use time_travel::{apply_time_travel, apply_time_travel_stage, delete_idle_staging};
pub(crate) use window::{
    WindowAdvanceTarget, advance_window, metadata_identity, prune_window_history,
};
