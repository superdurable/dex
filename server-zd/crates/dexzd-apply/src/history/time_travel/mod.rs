// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! TimeTravel (plan section 8.4): forks attempt n+1 from a point and reapplies later input.
//!
//! The owner computes and stages the result (plan section 6.3); apply checks it, adds later
//! input, and forks.

mod attempt_fork;
mod reapplied_input_values;
mod result_computation;
mod staged_result;
mod staging;

use dexzd_protocol::dexzd::v1::{
    CheckpointRunStatus, TimeTravelCommand, TimeTravelStagedInput, TimeTravelStagedValue,
};

pub use result_computation::{
    ComputedTimeTravel, SegmentedTimeTravelComputation, StagedEntryTooLarge, TimeTravelComputation,
    TimeTravelComputationRequest, TimeTravelComputationSources, compute_time_travel,
    compute_time_travel_from_segments,
};
pub(crate) use staging::{apply_time_travel_stage, delete_idle_staging, staged_attempt_state};

use crate::ApplyError;
use crate::command_handlers::run_cursor::RunCursor;
use crate::command_handlers::{CommandScope, HandlerResult};
use crate::history::attempt_scope::AttemptRow;
use crate::history::lineage::LocalHistorySource;
use crate::history::reset_target::TravelTarget;
use crate::history::state_rebuild::{ReplayMemoryBudget, ReplayResources};
use crate::state_access::FlowTransaction;
use crate::values::NoOffloadedValues;

/// What a TimeTravel restores at its target point and the input it reapplies there.
#[derive(Debug)]
pub(crate) struct TimeTravelResult {
    /// Attempt whose lineage the request selected.
    pub(crate) selected_attempt: u32,
    pub(crate) target: TravelTarget,
    pub(crate) run_status: CheckpointRunStatus,
    pub(crate) rows: Vec<AttemptRow>,
    /// Values `rows` hold that the run's store lacked when the result was computed.
    pub(crate) unstored_values: Vec<TimeTravelStagedValue>,
    /// External input after the target point, in (run seq, index in record) order.
    pub(crate) reapplied_inputs: Vec<TimeTravelStagedInput>,
}

pub(crate) fn apply_time_travel(
    transaction: &mut FlowTransaction<'_>,
    mut cursor: RunCursor,
    scope: &CommandScope<'_>,
    command: &TimeTravelCommand,
) -> Result<HandlerResult, ApplyError> {
    let request = command.request.clone().unwrap_or_default();
    let result = if command.staging_id.is_empty() {
        // Binaries before staging logged TimeTravel without a result; their history is local.
        let history = LocalHistorySource::new(transaction, cursor.rows);
        let memory_budget = ReplayMemoryBudget::unlimited();
        result_computation::compute_result(
            transaction,
            &history,
            cursor.rows,
            &cursor.record,
            &request,
            scope.now_micros,
            cursor.seq.saturating_sub(1),
            ReplayResources {
                offloaded_values: &NoOffloadedValues,
                memory_budget: &memory_budget,
            },
        )?
    } else {
        staged_result::read_staged_result(transaction, &cursor, scope, command, &request)?
    };
    let result = match result {
        Ok(result) => result,
        Err(reply) => return Ok(HandlerResult::Rejected(reply)),
    };
    if let Err(reply) = staged_result::restore_held_values(transaction, &cursor, &result)? {
        return Ok(HandlerResult::Rejected(reply));
    }
    staging::delete_staging(transaction, &mut cursor);
    attempt_fork::fork_attempt(transaction, cursor, scope, command, &request, result)
}

#[cfg(feature = "test-util")]
pub use owner_staging::{
    StagedTimeTravelForTest, apply_staged_time_travel_for_test, is_unstaged_time_travel_for_test,
    stage_computed_time_travel_for_test, stage_time_travel_for_test,
};

/// Test entry point that stages a TimeTravel the way an owner does.
#[cfg(feature = "test-util")]
mod owner_staging {
    use dexzd_keyspace::RunId;
    use dexzd_protocol::dexzd::v1::{CommandReply, FlowCommand, flow_command::Command};

    use super::{
        ComputedTimeTravel, TimeTravelComputation, TimeTravelComputationRequest,
        TimeTravelComputationSources, compute_time_travel,
    };
    use crate::history::lineage::StateHistorySource;
    use crate::values::NoOffloadedValues;
    use crate::{ApplyContext, ApplyError, FlowStateReader};

    /// The commands an owner proposes for one time travel, and the result they stage.
    #[derive(Debug)]
    pub struct StagedTimeTravelForTest {
        /// Stage commands in chunk order, then the TimeTravel that names them, in the unstaged
        /// command's envelope.
        pub commands: Vec<FlowCommand>,
        /// The computed result.
        pub computed: ComputedTimeTravel,
    }

    /// Applies the commands of `staged` with `apply` in order until one is rejected.
    ///
    /// Returns the last outcome, or an outcome with the computation's rejection when it
    /// proposed nothing.
    pub fn apply_staged_time_travel_for_test(
        staged: Result<StagedTimeTravelForTest, CommandReply>,
        mut apply: impl FnMut(FlowCommand) -> crate::ApplyOutcome,
    ) -> crate::ApplyOutcome {
        let staged = match staged {
            Ok(staged) => staged,
            Err(reply) => return crate::ApplyOutcome::rejected(reply),
        };
        let mut last = None;
        for command in staged.commands {
            let outcome = apply(command);
            let is_rejected = matches!(
                outcome.reply.reply,
                Some(dexzd_protocol::dexzd::v1::command_reply::Reply::Error(_))
            );
            last = Some(outcome);
            if is_rejected {
                break;
            }
        }
        last.expect("staging proposes at least the TimeTravel command")
    }

    /// Whether `command` is a TimeTravel that names no staged result, as an owner never proposes.
    pub fn is_unstaged_time_travel_for_test(command: &FlowCommand) -> bool {
        matches!(
            &command.command,
            Some(Command::TimeTravel(time_travel)) if time_travel.staging_id.is_empty()
        )
    }

    /// Computes the unstaged TimeTravel `command` on `state` as an owner does, without a
    /// replay memory budget.
    ///
    /// Returns the commands that stage the result and then apply it, under a staging ID made
    /// from the command ID; they expect the computed run attempt unless `command` expected
    /// another. Returns the reply that rejects the request, which an owner returns without
    /// proposing anything. Returns an error when `command` is not a TimeTravel or state is
    /// corrupt.
    pub fn stage_time_travel_for_test(
        context: &ApplyContext,
        state: &dyn FlowStateReader,
        command: &FlowCommand,
    ) -> Result<Result<StagedTimeTravelForTest, CommandReply>, ApplyError> {
        let run_id =
            RunId::from_slice(&command.run_id).map_err(|error| ApplyError::InvariantViolation {
                detail: format!("a TimeTravel command has an invalid run ID: {error}"),
            })?;
        let Some(Command::TimeTravel(time_travel)) = &command.command else {
            return Err(ApplyError::InvariantViolation {
                detail: "only a TimeTravel command can be staged".to_owned(),
            });
        };
        let reset = time_travel.request.clone().unwrap_or_default();
        let history = StateHistorySource::new(state, *context, run_id);
        let computation = compute_time_travel(
            context,
            &TimeTravelComputationSources {
                state,
                history: &history,
                offloaded_values: &NoOffloadedValues,
            },
            &TimeTravelComputationRequest {
                run_id,
                reset: &reset,
                now_micros: command.proposed_at_micros,
                replay_memory_budget_bytes: u64::MAX,
            },
        )?;
        stage_computed_time_travel_for_test(command, computation)
    }

    /// Stages `computation`, which an owner computed for the unstaged TimeTravel `command`, as
    /// [`stage_time_travel_for_test`] does.
    ///
    /// Returns the reply that rejects the request, or an error when `command` is not a
    /// TimeTravel or the result cannot be staged.
    pub fn stage_computed_time_travel_for_test(
        command: &FlowCommand,
        computation: TimeTravelComputation,
    ) -> Result<Result<StagedTimeTravelForTest, CommandReply>, ApplyError> {
        let Some(Command::TimeTravel(time_travel)) = &command.command else {
            return Err(ApplyError::InvariantViolation {
                detail: "only a TimeTravel command can be staged".to_owned(),
            });
        };
        let computed = match computation {
            TimeTravelComputation::Computed(computed) => *computed,
            TimeTravelComputation::Rejected(reply) => return Ok(Err(reply)),
        };
        let mut staging_id = b"staging-".to_vec();
        staging_id.extend_from_slice(&command.command_id);
        let mut bodies: Vec<Command> = computed
            .stage_commands(&staging_id)
            .map_err(|error| ApplyError::InvariantViolation {
                detail: error.to_string(),
            })?
            .into_iter()
            .map(Command::TimeTravelStage)
            .collect();
        bodies.push(Command::TimeTravel(computed.time_travel_command(
            &staging_id,
            time_travel.should_restore_running_as_pending,
        )));
        let expected_run_attempt = if command.expected_run_attempt == 0 {
            computed.run_attempt()
        } else {
            command.expected_run_attempt
        };
        let commands = bodies
            .into_iter()
            .map(|body| FlowCommand {
                expected_run_attempt,
                command: Some(body),
                ..command.clone()
            })
            .collect();
        Ok(Ok(StagedTimeTravelForTest { commands, computed }))
    }
}
