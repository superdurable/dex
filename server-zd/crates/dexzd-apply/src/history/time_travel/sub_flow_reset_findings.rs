// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! SubFlow reset policies at the owner: reads child Flows, then stages findings or rejects.

use dexzd_keyspace::row_keys::prefix_range;
use dexzd_keyspace::{ColumnFamily, RunTable, TenantId, run_table_prefix};
use dexzd_protocol::dex::{ErrorSubStatus, ResetFlowRequest, SubFlowResetPolicy};
use dexzd_protocol::dexzd::v1::{
    ChildFlowCompletedMessage, CommandReply, OutboxRecord, ParentFlowReference, RunRecord,
    TimeTravelStagedChildFlow, outbox_record, time_travel_staged_child_flow::Finding,
};

use super::TimeTravelResult;
use super::child_flow_runs::{ChildFlowRun, ChildFlowRunQuery, ChildFlowRuns, ChildFlowStartRead};
use super::sub_flow_reset::{
    RestoredSubFlowCondition, is_result_received, reset_policy, restored_sub_flow_conditions,
};
use crate::ApplyError;
use crate::command_handlers::command_replies::{error_reply, grpc_code};
use crate::cross_flow::{
    child_start_request_id, restarted_child_condition, validate_child_start_parameters,
};
use crate::state_access::{FlowTransaction, RunRows};

/// A running child keeps its condition attached under ResetFlow; another operation may not.
const DOES_RUNNING_CHILD_ATTACH: bool = true;

const GONE_CHILD_REASON: &str = "its child run no longer exists, for example because it was purged";

/// How a restored condition relates to its child, as the owner reads it.
enum ChildAttachment {
    /// The parent stores a result that the condition accepts.
    Received,
    /// The child run ended; the parent has not received its result.
    Ended(ChildFlowRun),
    /// The child run still runs.
    Running(ChildFlowRun),
    /// The child start still waits in the parent's outbox.
    StartPending,
    /// No child run exists.
    Gone,
}

/// What the owner does for one restored condition.
enum OwnerDecision {
    /// Stage nothing; apply decides from its own reads.
    Nothing,
    Stage(Box<Finding>),
    /// Reject the ResetFlow for this reason.
    Reject(String),
}

impl OwnerDecision {
    fn stage(finding: Finding) -> Self {
        Self::Stage(Box::new(finding))
    }
}

/// Reads what the restored SubFlow conditions of `result` need from their child Flows.
///
/// Returns the findings to stage, or the FAILED_PRECONDITION reply that names each condition
/// the policy rejects, or INVALID_ARGUMENT for an unknown policy. Returns an error when stored
/// state is corrupt.
pub(super) fn child_flow_findings(
    transaction: &FlowTransaction<'_>,
    rows: RunRows,
    record: &RunRecord,
    request: &ResetFlowRequest,
    result: &TimeTravelResult,
    child_flow_runs: &dyn ChildFlowRuns,
) -> Result<Result<Vec<TimeTravelStagedChildFlow>, CommandReply>, ApplyError> {
    let policy = match reset_policy(request) {
        Ok(policy) => policy,
        Err(reply) => return Ok(Err(reply)),
    };
    let reads = ChildFlowReads {
        transaction,
        rows,
        record,
        child_flow_runs,
    };
    let mut findings = Vec::new();
    let mut rejections = Vec::new();
    for condition in restored_sub_flow_conditions(&result.rows)? {
        match reads.decide(policy, &condition)? {
            OwnerDecision::Nothing => {}
            OwnerDecision::Stage(finding) => findings.push(TimeTravelStagedChildFlow {
                step_execution_id: condition.step_execution_id.clone(),
                sub_flow_index: condition.sub_flow_index,
                child_flow_id: condition.child_flow_id.clone(),
                finding: Some(*finding),
            }),
            OwnerDecision::Reject(reason) => rejections.push(format!(
                "Step execution {} waits on child Flow {}: {reason}",
                condition.step_execution_id, condition.child_flow_id
            )),
        }
    }
    if !rejections.is_empty() {
        return Ok(Err(error_reply(
            grpc_code::FAILED_PRECONDITION,
            ErrorSubStatus::Uncategorized,
            format!(
                "time travel of Flow {} with SubFlow reset policy {} is rejected; {}",
                record.flow_id,
                policy.as_str_name(),
                rejections.join("; ")
            ),
        )));
    }
    Ok(Ok(findings))
}

/// What the owner reads for one computation: the parent's state and the child Flows.
struct ChildFlowReads<'reads> {
    transaction: &'reads FlowTransaction<'reads>,
    rows: RunRows,
    record: &'reads RunRecord,
    child_flow_runs: &'reads dyn ChildFlowRuns,
}

impl ChildFlowReads<'_> {
    /// Returns what `policy` makes the owner do for `condition`.
    fn decide(
        &self,
        policy: SubFlowResetPolicy,
        condition: &RestoredSubFlowCondition,
    ) -> Result<OwnerDecision, ApplyError> {
        match policy {
            SubFlowResetPolicy::AlwaysReject => Ok(OwnerDecision::Reject(
                "the policy rejects every restored SubFlow wait".to_owned(),
            )),
            SubFlowResetPolicy::AlwaysFail => Ok(OwnerDecision::Nothing),
            SubFlowResetPolicy::AlwaysRestart => {
                let run = self.child_run(condition, true)?;
                Ok(restart_decision(condition, run))
            }
            SubFlowResetPolicy::Unspecified
            | SubFlowResetPolicy::AttachElseRestart
            | SubFlowResetPolicy::AttachElseFail
            | SubFlowResetPolicy::AttachElseReject => {
                let should_read_start =
                    policy == SubFlowResetPolicy::AttachElseRestart && !DOES_RUNNING_CHILD_ATTACH;
                let attachment = self.attachment(condition, should_read_start)?;
                Ok(attach_decision(policy, condition, attachment))
            }
        }
    }

    fn attachment(
        &self,
        condition: &RestoredSubFlowCondition,
        should_read_start: bool,
    ) -> Result<ChildAttachment, ApplyError> {
        if is_result_received(self.transaction, self.rows, condition)? {
            return Ok(ChildAttachment::Received);
        }
        if let Some(run) = self.child_run(condition, should_read_start)? {
            return Ok(if run.ended_result.is_some() {
                ChildAttachment::Ended(run)
            } else {
                ChildAttachment::Running(run)
            });
        }
        if self.is_child_start_pending(condition)? {
            return Ok(ChildAttachment::StartPending);
        }
        Ok(ChildAttachment::Gone)
    }

    fn child_run(
        &self,
        condition: &RestoredSubFlowCondition,
        should_read_start: bool,
    ) -> Result<Option<ChildFlowRun>, ApplyError> {
        let parent = self.parent_reference(condition);
        let start_request_id = child_start_request_id(
            self.rows.run_id,
            condition.registration_attempt,
            &condition.step_execution_id,
            condition.sub_flow_index,
        );
        self.child_flow_runs.child_flow_run(&ChildFlowRunQuery {
            tenant_id: TenantId(self.record.tenant_id),
            child_flow_id: &condition.child_flow_id,
            start_request_id: &start_request_id,
            parent: &parent,
            should_read_start,
        })
    }

    /// Returns whether the parent's outbox still holds the unacknowledged start of the child.
    fn is_child_start_pending(
        &self,
        condition: &RestoredSubFlowCondition,
    ) -> Result<bool, ApplyError> {
        let parent = self.parent_reference(condition);
        let prefix = run_table_prefix(self.rows.flow_hash, self.rows.run_id, RunTable::Outbox);
        let (start, end) = prefix_range(&prefix);
        let records = self.transaction.scan_messages::<OutboxRecord>(
            ColumnFamily::Flow,
            &start,
            &end,
            usize::MAX,
            "outbox",
        )?;
        Ok(records.iter().any(|(_, record)| {
            record.acknowledged_at_micros == 0
                && matches!(
                    &record.payload,
                    Some(outbox_record::Payload::ChildStart(start))
                        if start.parent.as_ref() == Some(&parent)
                )
        }))
    }

    fn parent_reference(&self, condition: &RestoredSubFlowCondition) -> ParentFlowReference {
        condition.parent_reference(
            self.record.tenant_id,
            &self.record.flow_id,
            self.rows.run_id,
            condition.registration_attempt,
        )
    }
}

/// Decides an ATTACH_ELSE policy: attach when possible, else its second action.
fn attach_decision(
    policy: SubFlowResetPolicy,
    condition: &RestoredSubFlowCondition,
    attachment: ChildAttachment,
) -> OwnerDecision {
    let unattached_run = match attachment {
        ChildAttachment::Received => return OwnerDecision::Nothing,
        ChildAttachment::Ended(run) => {
            return OwnerDecision::stage(Finding::EndedResult(ended_result(condition, run)));
        }
        ChildAttachment::StartPending => return OwnerDecision::stage(Finding::IsRunning(true)),
        ChildAttachment::Running(_) if DOES_RUNNING_CHILD_ATTACH => {
            return OwnerDecision::stage(Finding::IsRunning(true));
        }
        ChildAttachment::Running(run) => Some(run),
        ChildAttachment::Gone => None,
    };
    match policy {
        SubFlowResetPolicy::AttachElseFail => OwnerDecision::stage(Finding::IsUnattachable(true)),
        SubFlowResetPolicy::AttachElseReject => OwnerDecision::Reject(match unattached_run {
            Some(_) => "its child run still runs, so it cannot be attached".to_owned(),
            None => format!("{GONE_CHILD_REASON}, so it cannot be attached"),
        }),
        _ => match unattached_run {
            Some(run) => restart_decision(condition, Some(run)),
            None => OwnerDecision::Reject(format!(
                "{GONE_CHILD_REASON}, so it can be neither attached nor restarted"
            )),
        },
    }
}

/// Stages the child's original start for a restart, or rejects when it cannot be repeated.
fn restart_decision(
    condition: &RestoredSubFlowCondition,
    run: Option<ChildFlowRun>,
) -> OwnerDecision {
    let Some(run) = run else {
        return OwnerDecision::Reject(format!("{GONE_CHILD_REASON}, so it cannot be restarted"));
    };
    let start = match run.start {
        Some(ChildFlowStartRead::Available(start)) => start,
        Some(ChildFlowStartRead::Unavailable(reason)) => {
            return OwnerDecision::Reject(format!("it cannot be restarted: {reason}"));
        }
        None => {
            return OwnerDecision::Reject(
                "it cannot be restarted: its start was not read".to_owned(),
            );
        }
    };
    if start.child_flow_id != condition.child_flow_id {
        return OwnerDecision::Reject(format!(
            "it cannot be restarted: its child run was started for child Flow {}",
            start.child_flow_id
        ));
    }
    if let Err(detail) = validate_child_start_parameters(
        &restarted_child_condition(&start),
        &start.flow_config.clone().unwrap_or_default(),
        condition.sub_flow_index,
    ) {
        return OwnerDecision::Reject(format!("it cannot be restarted: {detail}"));
    }
    OwnerDecision::stage(Finding::RestartStart(*start))
}

/// Returns the report the ended child run would deliver for `condition`.
fn ended_result(
    condition: &RestoredSubFlowCondition,
    run: ChildFlowRun,
) -> ChildFlowCompletedMessage {
    ChildFlowCompletedMessage {
        child_flow_id: condition.child_flow_id.clone(),
        child_run_id: run.run_id.as_bytes().to_vec(),
        child_run_attempt: run.run_attempt,
        result: run.ended_result,
        parent: None,
    }
}
