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
mod clone_fork;
pub mod diagnostics;
mod lineage;
mod local_cleanup;
mod replay_state;
mod reset_target;
mod state_rebuild;
mod time_travel;
mod window;

pub(crate) use checkpoints::{after_command, pending_step_methods};
pub use clone_fork::{
    CloneForkComputation, CloneForkComputationRequest, CloneForkHardDeleteAttribute,
    CloneForkStagedEntryTooLarge, ComputedCloneFork, SegmentedCloneForkComputation,
    compute_clone_fork, compute_clone_fork_from_segments,
};
pub(crate) use clone_fork::{
    apply_flow_clone_fork, apply_flow_clone_fork_stage, apply_flow_clone_fork_staging_expire,
};
pub use lineage::{HistoryRecords, HistorySource, StateHistorySource};
pub(crate) use lineage::{LocalHistorySource, genesis_seq};
pub(crate) use local_cleanup::{
    LocalCleanupBacklog, LocalCleanupCaller, apply_history_local_cleanup, clean_up_local_history,
    schedule_local_cleanup,
};
pub use time_travel::{
    ChildFlowRead, ChildFlowReadMemory, ChildFlowRun, ChildFlowRunQuery, ChildFlowRuns,
    ChildFlowStartRead, ComputedTimeTravel, GoneChildFlowCondition, NoChildFlowRuns,
    SegmentedTimeTravelComputation, StagedEntryTooLarge, StateChildFlowRuns, TimeTravelComputation,
    TimeTravelComputationRequest, TimeTravelComputationSources, compute_time_travel,
    compute_time_travel_from_segments,
};
#[cfg(feature = "test-util")]
pub use time_travel::{
    StagedTimeTravelForTest, apply_staged_time_travel_for_test, is_unstaged_time_travel_for_test,
    stage_computed_time_travel_for_test, stage_time_travel_for_test,
    stage_time_travel_reading_child_flows_for_test,
};
pub(crate) use time_travel::{apply_time_travel, apply_time_travel_stage, delete_idle_staging};
pub(crate) use window::{
    HISTORY_ROWS_PER_COMMAND, WindowAdvanceTarget, advance_window, metadata_identity,
    prune_window_history,
};
