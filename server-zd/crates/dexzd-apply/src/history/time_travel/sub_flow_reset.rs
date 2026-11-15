// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! SubFlow reset policies in apply: decides from apply's reads and the owner's staged findings.

use dexzd_keyspace::{ColumnFamily, RunId, RunTable};
use dexzd_protocol::dex::{
    ErrorSubStatus, FlowErrorType, FlowResult, FlowStatus, ResetFlowRequest, SubFlowResetPolicy,
};
use dexzd_protocol::dexzd::v1::{
    ChildFlowCompletedMessage, ChildFlowStartMessage, CommandReply, CrossFlowEndpoint,
    ParentFlowReference, StepExecutionPhase, StepExecutionRecord, SubFlowResultRecord,
    TimeTravelStagedChildFlow, time_travel_staged_child_flow::Finding,
};
use prost::Message;

use super::TimeTravelResult;
use crate::ApplyError;
use crate::command_handlers::CommandScope;
use crate::command_handlers::command_replies::{error_reply, grpc_code};
use crate::command_handlers::run_cursor::RunCursor;
use crate::cross_flow::{
    StoredSubFlowResult, pending_child_start_parents, restarted_child_condition,
    store_sub_flow_result, sub_flow_result_key, validate_child_start_parameters,
    write_child_terminate, write_restarted_child_start,
};
use crate::history::attempt_scope::{AttemptRow, hex};
use crate::state_access::{FlowTransaction, RunRows};
use crate::steps::wait_conditions::{
    set_sub_flow_registration_attempt, sub_flow_registration_attempt,
};

/// Error message of the FAILED result that completes an abandoned SubFlow condition.
const ABANDONED_CONDITION_MESSAGE: &str =
    "the parent's time travel abandoned this SubFlow condition";

/// One SubFlow condition that a restored Step waits on.
#[derive(Debug, Clone)]
pub(super) struct RestoredSubFlowCondition {
    pub(super) step_execution_id: String,
    pub(super) started_waiting_seq: u64,
    pub(super) sub_flow_index: i32,
    pub(super) child_flow_id: String,
    pub(super) condition_id: String,
    /// The attempt whose child results the condition accepts.
    pub(super) registration_attempt: u32,
}

impl RestoredSubFlowCondition {
    /// Returns the reference that a child run started by `run_attempt` reports to.
    pub(super) fn parent_reference(
        &self,
        tenant_id: u64,
        flow_id: &str,
        run_id: RunId,
        run_attempt: u32,
    ) -> ParentFlowReference {
        ParentFlowReference {
            parent: Some(CrossFlowEndpoint {
                tenant_id,
                flow_id: flow_id.to_owned(),
                run_id: run_id.as_bytes().to_vec(),
                run_attempt,
            }),
            step_execution_id: self.step_execution_id.clone(),
            sub_flow_index: self.sub_flow_index,
            condition_id: self.condition_id.clone(),
        }
    }

    /// Returns whether `staged` was staged for this condition.
    fn is_staged_for(&self, staged: &TimeTravelStagedChildFlow) -> bool {
        staged.step_execution_id == self.step_execution_id
            && staged.sub_flow_index == self.sub_flow_index
            && staged.child_flow_id == self.child_flow_id
    }

    fn describe(&self) -> String {
        format!(
            "Step execution {} waits on child Flow {}",
            self.step_execution_id, self.child_flow_id
        )
    }
}

/// What apply does with one restored SubFlow condition beyond keeping its wait.
#[derive(Debug)]
pub(super) struct RestoredSubFlowAction {
    condition: RestoredSubFlowCondition,
    kind: RestoredSubFlowActionKind,
}

#[derive(Debug)]
enum RestoredSubFlowActionKind {
    /// Store the final result of a child run that ended, as its report would.
    DeliverEndedResult(Box<ChildFlowCompletedMessage>),
    /// Restart the child from its original start under the new run attempt.
    Restart(Box<ChildFlowStartMessage>),
    /// Complete the condition with an abandoned result, terminating a child that may still run.
    Fail { should_terminate: bool },
}

/// Returns the policy that `request` selects; UNSPECIFIED selects ATTACH_ELSE_RESTART.
///
/// Returns the INVALID_ARGUMENT reply for an unknown policy.
pub(super) fn reset_policy(request: &ResetFlowRequest) -> Result<SubFlowResetPolicy, CommandReply> {
    match SubFlowResetPolicy::try_from(request.sub_flow_reset_policy) {
        Ok(SubFlowResetPolicy::Unspecified) => Ok(SubFlowResetPolicy::AttachElseRestart),
        Ok(policy) => Ok(policy),
        Err(_) => Err(error_reply(
            grpc_code::INVALID_ARGUMENT,
            ErrorSubStatus::Uncategorized,
            format!(
                "unknown SubFlow reset policy {}",
                request.sub_flow_reset_policy
            ),
        )),
    }
}

/// Returns the SubFlow conditions that the waiting Steps of `rows` wait on, ordered by when
/// each Step started waiting, then by Step execution ID and SubFlow index.
///
/// Returns an error when a Step row is corrupt.
pub(super) fn restored_sub_flow_conditions(
    rows: &[AttemptRow],
) -> Result<Vec<RestoredSubFlowCondition>, ApplyError> {
    let mut conditions = Vec::new();
    for row in rows
        .iter()
        .filter(|row| row.table == RunTable::StepExecution)
    {
        let step = StepExecutionRecord::decode(row.value.as_slice()).map_err(|error| {
            ApplyError::CorruptState {
                table: "step execution",
                key: hex(&row.key_suffix),
                detail: error.to_string(),
            }
        })?;
        let Some(wait) = step.wait.as_ref() else {
            continue;
        };
        if step.phase != StepExecutionPhase::Waiting as i32 {
            continue;
        }
        let sub_flow_conditions = wait
            .condition
            .as_ref()
            .map(|condition| condition.sub_flow_conditions.as_slice())
            .unwrap_or_default();
        for (index, child_flow_id) in wait.sub_flow_child_ids.iter().enumerate() {
            conditions.push(RestoredSubFlowCondition {
                step_execution_id: step.step_execution_id.clone(),
                started_waiting_seq: wait.started_waiting_seq,
                sub_flow_index: i32::try_from(index)
                    .expect("SubFlow condition count fits a signed index"),
                child_flow_id: child_flow_id.clone(),
                condition_id: sub_flow_conditions
                    .get(index)
                    .map(|condition| condition.condition_id.clone())
                    .unwrap_or_default(),
                registration_attempt: sub_flow_registration_attempt(wait, index),
            });
        }
    }
    conditions.sort_by(|left, right| {
        (
            left.started_waiting_seq,
            &left.step_execution_id,
            left.sub_flow_index,
        )
            .cmp(&(
                right.started_waiting_seq,
                &right.step_execution_id,
                right.sub_flow_index,
            ))
    });
    Ok(conditions)
}

/// Returns whether the run stores a child result that `condition` accepts.
pub(super) fn is_result_received(
    transaction: &FlowTransaction<'_>,
    rows: RunRows,
    condition: &RestoredSubFlowCondition,
) -> Result<bool, ApplyError> {
    let key = sub_flow_result_key(
        rows,
        &condition.child_flow_id,
        condition.registration_attempt,
    );
    Ok(transaction.get(ColumnFamily::Flow, &key)?.is_some())
}

/// Decides what the new attempt does with each restored SubFlow condition; results of older owners keep
/// every wait.
///
/// Returns ABORTED when the owner's findings no longer fit apply's reads, so the owner computes
/// again, and INVALID_ARGUMENT for a malformed finding or an unknown policy.
pub(super) fn plan_restored_sub_flow_actions(
    transaction: &FlowTransaction<'_>,
    cursor: &RunCursor,
    request: &ResetFlowRequest,
    result: &TimeTravelResult,
) -> Result<Result<Vec<RestoredSubFlowAction>, CommandReply>, ApplyError> {
    let Some(child_flows) = result.child_flows.as_ref() else {
        return Ok(Ok(Vec::new()));
    };
    let policy = match reset_policy(request) {
        Ok(policy) => policy,
        Err(reply) => return Ok(Err(reply)),
    };
    let conditions = restored_sub_flow_conditions(&result.rows)?;
    if conditions.is_empty() {
        return Ok(Ok(Vec::new()));
    }
    let pending_parents = pending_child_start_parents(transaction, cursor.rows)?;
    let mut actions = Vec::new();
    for condition in conditions {
        let is_received = is_result_received(transaction, cursor.rows, &condition)?;
        let finding = child_flows
            .iter()
            .find(|staged| condition.is_staged_for(staged))
            .and_then(|staged| staged.finding.as_ref());
        let kind = match staged_condition_action(policy, is_received, finding) {
            Ok(Some(kind)) => kind,
            Ok(None) => continue,
            Err(mismatch) => return Ok(Err(stale_finding_reply(&condition, mismatch))),
        };
        // The owner rejects these; a start still on its way could outlive the abandoned wait.
        let is_abandoning = matches!(
            kind,
            RestoredSubFlowActionKind::Restart(_) | RestoredSubFlowActionKind::Fail { .. }
        );
        let parent = condition.parent_reference(
            cursor.record.tenant_id,
            &cursor.record.flow_id,
            cursor.run_id(),
            condition.registration_attempt,
        );
        if is_abandoning && pending_parents.contains(&parent) {
            return Ok(Err(stale_finding_reply(
                &condition,
                "its child start has not reached the child yet",
            )));
        }
        if let Err(detail) = validate_staged_action(&condition, &kind) {
            return Ok(Err(error_reply(
                grpc_code::INVALID_ARGUMENT,
                ErrorSubStatus::Uncategorized,
                format!(
                    "the staged time travel result has an invalid SubFlow finding: {}: {detail}",
                    condition.describe()
                ),
            )));
        }
        actions.push(RestoredSubFlowAction { condition, kind });
    }
    Ok(Ok(actions))
}

/// Returns what `policy` does with a condition, given whether apply finds its result received
/// and the owner's finding; `None` keeps the wait.
///
/// Returns why the finding no longer fits, which the owner settles by computing again.
fn staged_condition_action(
    policy: SubFlowResetPolicy,
    is_received: bool,
    finding: Option<&Finding>,
) -> Result<Option<RestoredSubFlowActionKind>, &'static str> {
    match policy {
        SubFlowResetPolicy::AlwaysReject => Err("the policy rejects every restored SubFlow wait"),
        SubFlowResetPolicy::AlwaysFail => Ok(Some(RestoredSubFlowActionKind::Fail {
            should_terminate: !is_received,
        })),
        SubFlowResetPolicy::AlwaysRestart => match finding {
            Some(Finding::RestartStart(start)) => Ok(Some(RestoredSubFlowActionKind::Restart(
                Box::new(start.clone()),
            ))),
            _ => Err("no staged start restarts its child"),
        },
        SubFlowResetPolicy::Unspecified
        | SubFlowResetPolicy::AttachElseRestart
        | SubFlowResetPolicy::AttachElseFail
        | SubFlowResetPolicy::AttachElseReject => {
            if is_received {
                return Ok(None);
            }
            match finding {
                None => Err("the owner found its child result received"),
                Some(Finding::IsRunning(_)) => Ok(None),
                Some(Finding::EndedResult(completed)) => Ok(Some(
                    RestoredSubFlowActionKind::DeliverEndedResult(Box::new(completed.clone())),
                )),
                Some(Finding::RestartStart(start))
                    if policy == SubFlowResetPolicy::AttachElseRestart =>
                {
                    Ok(Some(RestoredSubFlowActionKind::Restart(Box::new(
                        start.clone(),
                    ))))
                }
                Some(Finding::IsUnattachable(_))
                    if policy == SubFlowResetPolicy::AttachElseFail =>
                {
                    Ok(Some(RestoredSubFlowActionKind::Fail {
                        should_terminate: false,
                    }))
                }
                Some(_) => Err("its staged finding does not fit the policy"),
            }
        }
    }
}

/// Checks that a staged start or result belongs to `condition` and that the restart is valid.
///
/// Returns what is wrong.
fn validate_staged_action(
    condition: &RestoredSubFlowCondition,
    kind: &RestoredSubFlowActionKind,
) -> Result<(), String> {
    match kind {
        RestoredSubFlowActionKind::DeliverEndedResult(completed) => {
            if completed.child_flow_id != condition.child_flow_id || completed.result.is_none() {
                return Err("the ended result names another child or has no result".to_owned());
            }
            Ok(())
        }
        RestoredSubFlowActionKind::Restart(start) => {
            if start.child_flow_id != condition.child_flow_id {
                return Err(format!(
                    "the restart start names child {}",
                    start.child_flow_id
                ));
            }
            validate_child_start_parameters(
                &restarted_child_condition(start),
                &start.flow_config.clone().unwrap_or_default(),
                condition.sub_flow_index,
            )
        }
        RestoredSubFlowActionKind::Fail { .. } => Ok(()),
    }
}

fn stale_finding_reply(condition: &RestoredSubFlowCondition, mismatch: &str) -> CommandReply {
    error_reply(
        grpc_code::ABORTED,
        ErrorSubStatus::Uncategorized,
        format!(
            "the staged time travel result is out of date: {}, but {mismatch}",
            condition.describe()
        ),
    )
}

/// Acts on the planned SubFlow conditions in the restored rows. Restarts and failures move a
/// condition to the new attempt.
pub(super) fn apply_restored_sub_flow_actions(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    scope: &CommandScope<'_>,
    actions: Vec<RestoredSubFlowAction>,
) -> Result<(), ApplyError> {
    let mut restart_index = 0_u64;
    let mut terminate_index = 0_u64;
    for RestoredSubFlowAction { condition, kind } in actions {
        match kind {
            RestoredSubFlowActionKind::DeliverEndedResult(completed) => {
                let completed = *completed;
                store_sub_flow_result(
                    transaction,
                    cursor,
                    StoredSubFlowResult {
                        child_flow_id: &condition.child_flow_id,
                        registration_attempt: condition.registration_attempt,
                        child_run_id: &completed.child_run_id,
                        child_run_attempt: completed.child_run_attempt,
                        result: completed.result.unwrap_or_default(),
                    },
                    None,
                )?;
            }
            RestoredSubFlowActionKind::Restart(start) => {
                move_condition_to_current_attempt(transaction, cursor, &condition)?;
                let parent = condition.parent_reference(
                    scope.tenant_id.0,
                    scope.flow_id(),
                    cursor.run_id(),
                    cursor.attempt(),
                );
                write_restarted_child_start(
                    transaction,
                    cursor,
                    scope,
                    restart_index,
                    parent,
                    &start,
                );
                restart_index += 1;
            }
            RestoredSubFlowActionKind::Fail { should_terminate } => {
                if should_terminate {
                    let abandoned = condition.parent_reference(
                        scope.tenant_id.0,
                        scope.flow_id(),
                        cursor.run_id(),
                        condition.registration_attempt,
                    );
                    write_child_terminate(
                        transaction,
                        cursor,
                        scope,
                        terminate_index,
                        &condition.child_flow_id,
                        abandoned,
                    );
                    terminate_index += 1;
                }
                move_condition_to_current_attempt(transaction, cursor, &condition)?;
                store_abandoned_result(transaction, cursor, &condition)?;
            }
        }
    }
    Ok(())
}

/// Makes the restored condition accept only child results of the current attempt.
fn move_condition_to_current_attempt(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    condition: &RestoredSubFlowCondition,
) -> Result<(), ApplyError> {
    let mut step = cursor
        .rows
        .step_execution(transaction, &condition.step_execution_id)?
        .ok_or_else(|| ApplyError::InvariantViolation {
            detail: format!(
                "restored Step execution {} disappeared before its SubFlow reset",
                condition.step_execution_id
            ),
        })?;
    let wait = step
        .wait
        .as_mut()
        .ok_or_else(|| ApplyError::InvariantViolation {
            detail: format!(
                "restored Step execution {} no longer waits before its SubFlow reset",
                condition.step_execution_id
            ),
        })?;
    let index = usize::try_from(condition.sub_flow_index)
        .expect("restored SubFlow indexes are non-negative");
    set_sub_flow_registration_attempt(wait, index, cursor.attempt());
    cursor.rows.put_step_execution(transaction, &step);
    Ok(())
}

/// Stores the FAILED result of an abandoned condition under the current attempt.
fn store_abandoned_result(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    condition: &RestoredSubFlowCondition,
) -> Result<(), ApplyError> {
    let attempt = cursor.attempt();
    let key = sub_flow_result_key(cursor.rows, &condition.child_flow_id, attempt);
    let previous: Option<SubFlowResultRecord> =
        transaction.get_message(ColumnFamily::Flow, &key, "SubFlow result")?;
    store_sub_flow_result(
        transaction,
        cursor,
        StoredSubFlowResult {
            child_flow_id: &condition.child_flow_id,
            registration_attempt: attempt,
            child_run_id: &[],
            child_run_attempt: 0,
            result: FlowResult {
                flow_status: FlowStatus::Failed as i32,
                results: Vec::new(),
                error_type: FlowErrorType::SubFlowAbandoned as i32,
                error_message: ABANDONED_CONDITION_MESSAGE.to_owned(),
            },
        },
        previous,
    )
}
