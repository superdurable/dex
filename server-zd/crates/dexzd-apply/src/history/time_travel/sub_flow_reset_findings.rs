// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! SubFlow reset policies at the owner: reads child Flows, then stages findings or rejects.

use std::cell::RefCell;
use std::collections::BTreeSet;

use dexzd_keyspace::{ColumnFamily, TenantId};
use dexzd_protocol::dex::{
    ErrorSubStatus, FlowResult, ResetFlowRequest, StepCompletionOutput, SubFlowResetPolicy,
};
use dexzd_protocol::dexzd::v1::{
    ChildFlowCompletedMessage, CommandReply, ParentFlowReference, RunRecord, SubFlowResultRecord,
    TimeTravelStagedChildFlow, time_travel_staged_child_flow::Finding,
};

use super::TimeTravelResult;
use super::child_flow_runs::{
    ChildFlowRead, ChildFlowReadMemory, ChildFlowRun, ChildFlowRunQuery, ChildFlowRuns,
    ChildFlowStartRead,
};
use super::sub_flow_reset::{
    RestoredSubFlowCondition, SubFlowResetOperation, is_result_received, reset_policy,
    restored_sub_flow_conditions,
};
use crate::ApplyError;
use crate::command_handlers::command_replies::{error_reply, grpc_code};
use crate::cross_flow::{
    child_start_request_id, pending_child_start_parents, restarted_child_condition,
    sub_flow_result_key, validate_child_start_parameters,
};
use crate::history::attempt_scope::AttemptRow;
use crate::history::state_rebuild::{
    HistoryPoint, ReplayMemoryBudget, replay_memory_exceeded_reply,
};
use crate::state_access::{FlowTransaction, RunRows};
use crate::values::{load_stored_value, stored_value_size};

const GONE_CHILD_REASON: &str = "its child run no longer exists, for example because it was purged";

/// A child whose start is still on its way could start after the time travel abandons it.
const PENDING_START_REASON: &str = "the child start has not reached the child yet, so it can be neither restarted nor failed; \
     retry the time travel later";

/// The parent run whose restored SubFlow conditions the owner reads, and the policy it applies.
pub(in crate::history) struct SubFlowFindingRequest<'request> {
    pub(in crate::history) transaction: &'request FlowTransaction<'request>,
    pub(in crate::history) rows: RunRows,
    pub(in crate::history) record: &'request RunRecord,
    pub(in crate::history) policy: SubFlowResetPolicy,
    pub(in crate::history) operation: SubFlowResetOperation,
    /// Attempt-scoped rows of the restored or copied state.
    pub(in crate::history) restored_rows: &'request [AttemptRow],
    /// The point the rows hold, which a memory rejection names.
    pub(in crate::history) point: HistoryPoint,
    pub(in crate::history) sources: ChildFlowSources<'request>,
}

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
    /// A child read would exceed the memory budget.
    MemoryExceeded,
}

impl OwnerDecision {
    fn stage(finding: Finding) -> Self {
        Self::Stage(Box::new(finding))
    }
}

/// Names a restored SubFlow condition whose child run looked gone in one computation.
///
/// The conditions a time travel restores are the same in every computation of one request, so
/// owners compare these across computations.
#[derive(Debug, Clone, PartialEq, Eq, PartialOrd, Ord)]
pub struct GoneChildFlowCondition {
    /// The Step execution that waits on the condition.
    pub step_execution_id: String,
    /// The condition's index among the Step's SubFlow conditions.
    pub sub_flow_index: i32,
    /// The condition's child Flow ID.
    pub child_flow_id: String,
}

impl GoneChildFlowCondition {
    fn of(condition: &RestoredSubFlowCondition) -> Self {
        Self {
            step_execution_id: condition.step_execution_id.clone(),
            sub_flow_index: condition.sub_flow_index,
            child_flow_id: condition.child_flow_id.clone(),
        }
    }
}

/// What the owner found for the restored SubFlow conditions of one computation.
pub(in crate::history) struct ChildFlowFindings {
    /// The findings to stage, or the reply that rejects the request.
    pub(in crate::history) outcome: Result<Vec<TimeTravelStagedChildFlow>, CommandReply>,
    /// The conditions whose child run was missing while the parent had neither its result nor
    /// its pending start.
    pub(in crate::history) gone_child_flows: BTreeSet<GoneChildFlowCondition>,
}

/// Reads what the restored SubFlow conditions of `result` need from their child Flows. Each read
/// reserves what it loads in `memory_budget`.
///
/// Returns the findings to stage, or the FAILED_PRECONDITION reply that names each condition
/// the policy rejects, or RESOURCE_EXHAUSTED once a read exceeds the memory budget, or
/// INVALID_ARGUMENT for an unknown policy. Returns an error when stored state is corrupt.
pub(super) fn child_flow_findings(
    transaction: &FlowTransaction<'_>,
    rows: RunRows,
    record: &RunRecord,
    request: &ResetFlowRequest,
    result: &TimeTravelResult,
    sources: ChildFlowSources<'_>,
) -> Result<ChildFlowFindings, ApplyError> {
    let policy = match reset_policy(request.sub_flow_reset_policy) {
        Ok(policy) => policy,
        Err(reply) => return Ok(ChildFlowFindings::settled(Err(reply))),
    };
    sub_flow_findings(&SubFlowFindingRequest {
        transaction,
        rows,
        record,
        policy,
        operation: SubFlowResetOperation::TimeTravel,
        restored_rows: &result.rows,
        point: result.target.point,
        sources,
    })
}

/// Reads what the restored SubFlow conditions of `request.restored_rows` need from their child
/// Flows under `request.policy` and `request.operation`, as [`child_flow_findings`] does.
///
/// Returns what [`child_flow_findings`] returns, except for the unknown policy reply.
pub(in crate::history) fn sub_flow_findings(
    request: &SubFlowFindingRequest<'_>,
) -> Result<ChildFlowFindings, ApplyError> {
    let conditions = restored_sub_flow_conditions(request.restored_rows)?;
    if conditions.is_empty() {
        return Ok(ChildFlowFindings::settled(Ok(Vec::new())));
    }
    let reads = ChildFlowReads {
        transaction: request.transaction,
        rows: request.rows,
        record: request.record,
        sources: request.sources,
        operation: request.operation,
        pending_parents: pending_child_start_parents(request.transaction, request.rows)?,
        gone_child_flows: RefCell::new(BTreeSet::new()),
    };
    let outcome = reads.findings(request.policy, conditions, request.point)?;
    Ok(ChildFlowFindings {
        outcome,
        gone_child_flows: reads.gone_child_flows.into_inner(),
    })
}

impl ChildFlowFindings {
    fn settled(outcome: Result<Vec<TimeTravelStagedChildFlow>, CommandReply>) -> Self {
        Self {
            outcome,
            gone_child_flows: BTreeSet::new(),
        }
    }
}

impl ChildFlowReads<'_> {
    /// Decides every condition; see [`child_flow_findings`].
    fn findings(
        &self,
        policy: SubFlowResetPolicy,
        conditions: Vec<RestoredSubFlowCondition>,
        point: HistoryPoint,
    ) -> Result<Result<Vec<TimeTravelStagedChildFlow>, CommandReply>, ApplyError> {
        let reads = self;
        let record = self.record;
        let sources = self.sources;
        let mut findings = Vec::new();
        let mut rejections = Vec::new();
        for condition in conditions {
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
                OwnerDecision::MemoryExceeded => {
                    return Ok(Err(replay_memory_exceeded_reply(
                        point,
                        sources.memory_budget.limit_bytes(),
                        "the child Flow results and starts its SubFlow reset policy reads",
                    )));
                }
            }
        }
        if !rejections.is_empty() {
            return Ok(Err(error_reply(
                grpc_code::FAILED_PRECONDITION,
                ErrorSubStatus::Uncategorized,
                format!(
                    "{} of Flow {} with SubFlow reset policy {} is rejected; {}",
                    self.operation.describe(),
                    record.flow_id,
                    policy.as_str_name(),
                    rejections.join("; ")
                ),
            )));
        }
        Ok(Ok(findings))
    }
}

/// Where the owner reads child Flows, and the memory budget the reads reserve in.
#[derive(Clone, Copy)]
pub(in crate::history) struct ChildFlowSources<'sources> {
    pub(in crate::history) child_flow_runs: &'sources dyn ChildFlowRuns,
    pub(in crate::history) memory_budget: &'sources ReplayMemoryBudget,
}

/// What the owner reads for one computation: the parent's state and the child Flows.
struct ChildFlowReads<'reads> {
    transaction: &'reads FlowTransaction<'reads>,
    rows: RunRows,
    record: &'reads RunRecord,
    sources: ChildFlowSources<'reads>,
    operation: SubFlowResetOperation,
    /// Parent references of the parent's unacknowledged child starts.
    pending_parents: Vec<ParentFlowReference>,
    /// The conditions whose child a decision found gone.
    gone_child_flows: RefCell<BTreeSet<GoneChildFlowCondition>>,
}

impl ChildFlowReads<'_> {
    /// Returns what `policy` makes the owner do for `condition`.
    fn decide(
        &self,
        policy: SubFlowResetPolicy,
        condition: &RestoredSubFlowCondition,
    ) -> Result<OwnerDecision, ApplyError> {
        let does_pending_start_block = self.operation.does_pending_start_block_abandoning();
        match policy {
            SubFlowResetPolicy::AlwaysReject => Ok(OwnerDecision::Reject(
                "the policy rejects every restored SubFlow wait".to_owned(),
            )),
            SubFlowResetPolicy::AlwaysFail => Ok(
                if does_pending_start_block && self.is_child_start_pending(condition) {
                    OwnerDecision::Reject(PENDING_START_REASON.to_owned())
                } else {
                    OwnerDecision::Nothing
                },
            ),
            SubFlowResetPolicy::AlwaysRestart => {
                let is_start_pending = self.is_child_start_pending(condition);
                if does_pending_start_block && is_start_pending {
                    return Ok(OwnerDecision::Reject(PENDING_START_REASON.to_owned()));
                }
                Ok(match self.child_run(condition, true)? {
                    ChildFlowRead::Found(run) => restart_decision(condition, Some(*run)),
                    // A restart repeats the child's start, which no child holds yet.
                    ChildFlowRead::Missing if is_start_pending => OwnerDecision::Reject(format!(
                        "its child start has not reached the child yet, so it cannot be \
                         restarted; retry the {}",
                        self.operation.describe()
                    )),
                    ChildFlowRead::Missing => {
                        self.note_gone_child_flow(condition);
                        restart_decision(condition, None)
                    }
                    ChildFlowRead::MemoryExceeded => OwnerDecision::MemoryExceeded,
                })
            }
            SubFlowResetPolicy::Unspecified
            | SubFlowResetPolicy::AttachElseRestart
            | SubFlowResetPolicy::AttachElseFail
            | SubFlowResetPolicy::AttachElseReject => {
                let does_running_child_attach = self.operation.does_running_child_attach();
                let should_read_start =
                    policy == SubFlowResetPolicy::AttachElseRestart && !does_running_child_attach;
                match self.attachment(condition, should_read_start)? {
                    Some(ChildAttachment::Received)
                        if self.operation == SubFlowResetOperation::CloneFork =>
                    {
                        self.received_result_decision(condition)
                    }
                    Some(attachment) => Ok(attach_decision(
                        policy,
                        condition,
                        attachment,
                        self.operation,
                    )),
                    None => Ok(OwnerDecision::MemoryExceeded),
                }
            }
        }
    }

    /// Stages the result the parent received for `condition`, with complete values.
    fn received_result_decision(
        &self,
        condition: &RestoredSubFlowCondition,
    ) -> Result<OwnerDecision, ApplyError> {
        let key = sub_flow_result_key(
            self.rows,
            &condition.child_flow_id,
            condition.registration_attempt,
        );
        let Some(received): Option<SubFlowResultRecord> =
            self.transaction
                .get_message(ColumnFamily::Flow, &key, "SubFlow result")?
        else {
            return Err(ApplyError::InvariantViolation {
                detail: format!(
                    "the received result of child Flow {} disappeared",
                    condition.child_flow_id
                ),
            });
        };
        let stored = received.result.unwrap_or_default();
        // Output sizes are known before their values load.
        let output_bytes = stored
            .results
            .iter()
            .filter_map(|output| output.completed_step_output.as_ref())
            .map(stored_value_size)
            .fold(0, u64::saturating_add);
        if !self.sources.memory_budget.reserve(output_bytes) {
            return Ok(OwnerDecision::MemoryExceeded);
        }
        let mut results = Vec::with_capacity(stored.results.len());
        for output in stored.results {
            let completed_step_output = match &output.completed_step_output {
                Some(value) => match load_stored_value(self.transaction, self.rows, value)? {
                    Some(complete) => Some(complete),
                    None => {
                        return Ok(OwnerDecision::Reject(
                            "the parent no longer stores the result it received from its child"
                                .to_owned(),
                        ));
                    }
                },
                None => None,
            };
            results.push(StepCompletionOutput {
                completed_step_type: output.completed_step_type,
                completed_step_execution_id: output.completed_step_execution_id,
                completed_step_output,
            });
        }
        Ok(OwnerDecision::stage(Finding::EndedResult(
            ChildFlowCompletedMessage {
                child_flow_id: condition.child_flow_id.clone(),
                child_run_id: received.child_run_id,
                child_run_attempt: received.child_run_attempt,
                result: Some(FlowResult {
                    flow_status: stored.flow_status,
                    results,
                    error_type: stored.error_type,
                    error_message: stored.error_message,
                }),
                parent: None,
            },
        )))
    }

    /// Returns how `condition` relates to its child, or `None` when the read exceeded the
    /// memory budget.
    fn attachment(
        &self,
        condition: &RestoredSubFlowCondition,
        should_read_start: bool,
    ) -> Result<Option<ChildAttachment>, ApplyError> {
        if is_result_received(self.transaction, self.rows, condition)? {
            return Ok(Some(ChildAttachment::Received));
        }
        let attachment = match self.child_run(condition, should_read_start)? {
            ChildFlowRead::MemoryExceeded => return Ok(None),
            ChildFlowRead::Found(run) if run.ended_result.is_some() => ChildAttachment::Ended(*run),
            ChildFlowRead::Found(run) => ChildAttachment::Running(*run),
            // A start on its way attaches: the child it starts reports to this condition.
            ChildFlowRead::Missing if self.is_child_start_pending(condition) => {
                ChildAttachment::StartPending
            }
            ChildFlowRead::Missing => {
                self.note_gone_child_flow(condition);
                ChildAttachment::Gone
            }
        };
        Ok(Some(attachment))
    }

    fn note_gone_child_flow(&self, condition: &RestoredSubFlowCondition) {
        self.gone_child_flows
            .borrow_mut()
            .insert(GoneChildFlowCondition::of(condition));
    }

    fn child_run(
        &self,
        condition: &RestoredSubFlowCondition,
        should_read_start: bool,
    ) -> Result<ChildFlowRead, ApplyError> {
        let parent = self.parent_reference(condition);
        let start_request_id = child_start_request_id(
            self.rows.run_id,
            condition.registration_attempt,
            &condition.step_execution_id,
            condition.sub_flow_index,
        );
        self.sources.child_flow_runs.child_flow_run(
            &ChildFlowRunQuery {
                tenant_id: TenantId(self.record.tenant_id),
                child_flow_id: &condition.child_flow_id,
                start_request_id: &start_request_id,
                parent: &parent,
                should_read_start,
            },
            self.sources.memory_budget,
        )
    }

    /// Returns whether the parent's outbox still holds the unacknowledged start of the child.
    fn is_child_start_pending(&self, condition: &RestoredSubFlowCondition) -> bool {
        self.pending_parents
            .contains(&self.parent_reference(condition))
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

impl ChildFlowReadMemory for ReplayMemoryBudget {
    fn reserve_bytes(&self, bytes: u64) -> bool {
        self.reserve(bytes)
    }

    fn release_bytes(&self, bytes: u64) {
        self.release(bytes);
    }
}

/// Decides an ATTACH_ELSE policy: attach when possible, else its second action.
fn attach_decision(
    policy: SubFlowResetPolicy,
    condition: &RestoredSubFlowCondition,
    attachment: ChildAttachment,
    operation: SubFlowResetOperation,
) -> OwnerDecision {
    let does_running_child_attach = operation.does_running_child_attach();
    let unattached_run = match attachment {
        ChildAttachment::Received => return OwnerDecision::Nothing,
        ChildAttachment::Ended(run) => {
            return OwnerDecision::stage(Finding::EndedResult(ended_result(condition, run)));
        }
        ChildAttachment::StartPending if does_running_child_attach => {
            return OwnerDecision::stage(Finding::IsRunning(true));
        }
        ChildAttachment::StartPending => {
            return match policy {
                SubFlowResetPolicy::AttachElseFail => {
                    OwnerDecision::stage(Finding::IsUnattachable(true))
                }
                SubFlowResetPolicy::AttachElseReject => OwnerDecision::Reject(
                    "its child start has not reached the child yet, so it cannot be attached"
                        .to_owned(),
                ),
                _ => OwnerDecision::Reject(format!(
                    "its child start has not reached the child yet, so it can be neither \
                     attached nor restarted; retry the {}",
                    operation.describe()
                )),
            };
        }
        ChildAttachment::Running(_) if does_running_child_attach => {
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
