// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dexzd_keyspace::row_keys::{history_record_key, prefix_range};
use dexzd_keyspace::{ColumnFamily, FlowHash, RunId, RunTable, run_table_prefix};
use dexzd_protocol::dex::{
    AttributeMatch, AttributeMatchOperator, FlowStatus, Value, WaitForHistoryEventResponse,
    flow_history_event, value,
};
use dexzd_protocol::dexzd::v1::{
    HistoryRecord, HistoryRowMetadata, StepExecutionPhase, stored_value,
};
use prost::Message;

use crate::FlowStateReader;
use crate::history::metadata_identity;
use crate::read_model::history::lineage_cutoffs;
use crate::read_model::{ReadModelError, RunView};

/// Result of waiting for one Step execution in the current Flow run.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum StepCompletionState {
    /// The Step completed, or its cancellation counts as completion.
    Completed,
    /// The Step has not completed and the Flow is still running.
    NotCompleted,
    /// The Step failed or a terminated Flow ended it without completion.
    EndedWithoutCompletion,
}

/// Returns whether the current run attempt is closed.
pub fn is_flow_closed(
    reader: &dyn FlowStateReader,
    flow_hash: FlowHash,
    run_id: RunId,
) -> Result<bool, ReadModelError> {
    let view = RunView::open(reader, flow_hash, run_id)?;
    Ok(view.record.status != FlowStatus::Running as i32)
}

/// Reads whether a Step completed, is pending, or can no longer complete in this run.
pub fn step_completion_state(
    reader: &dyn FlowStateReader,
    flow_hash: FlowHash,
    run_id: RunId,
    step_type: &str,
    step_execution_number: u32,
) -> Result<StepCompletionState, ReadModelError> {
    if step_type.is_empty() || step_execution_number == 0 {
        return Err(ReadModelError::InvalidRequest {
            detail: "Step type and positive execution number are required".to_owned(),
        });
    }
    let view = RunView::open(reader, flow_hash, run_id)?;
    let counter = view.rows.step_counter(&view.transaction, step_type)?;
    if counter.last_step_number < step_execution_number {
        return Ok(if view.record.status == FlowStatus::Running as i32 {
            StepCompletionState::NotCompleted
        } else {
            StepCompletionState::EndedWithoutCompletion
        });
    }
    let step_execution_id = format!("{step_type}-{step_execution_number}");
    let Some(step) = view
        .rows
        .step_execution(&view.transaction, &step_execution_id)?
    else {
        return Ok(StepCompletionState::Completed);
    };
    match StepExecutionPhase::try_from(step.phase) {
        Ok(StepExecutionPhase::Completed) => Ok(StepCompletionState::Completed),
        Ok(StepExecutionPhase::Canceled) => {
            if view.record.status != FlowStatus::Terminated as i32 {
                return Ok(StepCompletionState::Completed);
            }
            let is_canceled_by_flow_close = match step.is_canceled_by_flow_close {
                Some(is_canceled_by_flow_close) => is_canceled_by_flow_close,
                None => was_legacy_cancel_recorded_with_flow_close(&view, step.completed_seq)?,
            };
            Ok(if is_canceled_by_flow_close {
                StepCompletionState::EndedWithoutCompletion
            } else {
                StepCompletionState::Completed
            })
        }
        Ok(StepExecutionPhase::Failed) => Ok(StepCompletionState::EndedWithoutCompletion),
        Ok(
            StepExecutionPhase::WaitFor
            | StepExecutionPhase::Waiting
            | StepExecutionPhase::Execute
            | StepExecutionPhase::Unspecified,
        )
        | Err(_) => Ok(StepCompletionState::NotCompleted),
    }
}

/// Reads whether the command at `completed_seq` recorded FlowClosed, for Step rows written
/// before `is_canceled_by_flow_close` existed.
///
/// Returns true when that record is gone: a terminated Flow most likely canceled the Step.
fn was_legacy_cancel_recorded_with_flow_close(
    view: &RunView<'_>,
    completed_seq: u64,
) -> Result<bool, ReadModelError> {
    // Bookkeeping commands can advance next_run_seq after the close.
    let key = history_record_key(view.rows.flow_hash, view.rows.run_id, completed_seq);
    let record: Option<HistoryRecord> =
        view.transaction
            .get_message(ColumnFamily::History, &key, "history record")?;
    Ok(record.is_none_or(|record| {
        record.events.iter().any(|event| {
            matches!(
                event.payload,
                Some(flow_history_event::Payload::FlowClosed(_))
            )
        })
    }))
}

/// Returns the matching Attribute value, or None while the wait predicate is false.
pub fn match_attribute(
    reader: &dyn FlowStateReader,
    flow_hash: FlowHash,
    run_id: RunId,
    condition: &AttributeMatch,
) -> Result<Option<Value>, ReadModelError> {
    let operator = AttributeMatchOperator::try_from(condition.operator).map_err(|_| {
        ReadModelError::InvalidRequest {
            detail: "attribute match operator is invalid".to_owned(),
        }
    })?;
    if operator == AttributeMatchOperator::Unspecified {
        return Err(ReadModelError::InvalidRequest {
            detail: "attribute match operator is invalid".to_owned(),
        });
    }
    let operand = condition
        .operand
        .as_ref()
        .ok_or_else(|| ReadModelError::InvalidRequest {
            detail: "attribute match operand is required".to_owned(),
        })?;
    validate_operand(operand, operator)?;
    let view = RunView::open(reader, flow_hash, run_id)?;
    let Some(attribute) = view.rows.attribute(&view.transaction, &condition.key)? else {
        return Ok(None);
    };
    let Some(stored) = attribute.value.as_ref() else {
        return Ok(None);
    };
    let Some(stored_value::Kind::InlineValue(current)) = stored.kind.as_ref() else {
        return Err(ReadModelError::UnsupportedAttributeValue {
            key: condition.key.clone(),
        });
    };
    if current.encoded_len() > 100 {
        return Err(ReadModelError::UnsupportedAttributeValue {
            key: condition.key.clone(),
        });
    }
    if matches!(current.kind.as_ref(), Some(value::Kind::DoubleValue(number)) if !number.is_finite())
    {
        return Err(ReadModelError::UnsupportedAttributeValue {
            key: condition.key.clone(),
        });
    }
    let is_match = match (current.kind.as_ref(), operand.kind.as_ref()) {
        (Some(value::Kind::StringValue(left)), Some(value::Kind::StringValue(right))) => {
            compare_equal(left == right, operator)
        }
        (Some(value::Kind::BoolValue(left)), Some(value::Kind::BoolValue(right))) => {
            compare_equal(left == right, operator)
        }
        (Some(value::Kind::IntValue(left)), Some(value::Kind::IntValue(right))) => {
            compare_order(*left, *right, operator)
        }
        (Some(value::Kind::DoubleValue(left)), Some(value::Kind::DoubleValue(right))) => {
            compare_order(*left, *right, operator)
        }
        (
            Some(
                value::Kind::StringValue(_)
                | value::Kind::BoolValue(_)
                | value::Kind::IntValue(_)
                | value::Kind::DoubleValue(_),
            ),
            _,
        ) => false,
        _ => {
            return Err(ReadModelError::UnsupportedAttributeValue {
                key: condition.key.clone(),
            });
        }
    };
    Ok(is_match.then(|| current.clone()))
}

/// Most history metadata rows read by one scan call.
const HISTORY_METADATA_SCAN_ROWS: usize = 256;

/// Returns whether an event in the current lineage reaches the requested event ID.
///
/// Reads no History rows. Metadata from before the event ID fields makes the ID a lower bound.
pub fn is_history_event_available(
    reader: &dyn FlowStateReader,
    flow_hash: FlowHash,
    run_id: RunId,
    next_event_id: i64,
) -> Result<WaitForHistoryEventResponse, ReadModelError> {
    if next_event_id <= 0 {
        return Err(ReadModelError::InvalidRequest {
            detail: "next event ID must be positive".to_owned(),
        });
    }
    let view = RunView::open(reader, flow_hash, run_id)?;
    let available_id = first_lineage_event_id_from(&view, next_event_id)?;
    Ok(WaitForHistoryEventResponse {
        event_available: available_id > 0,
        available_internal_event_id: available_id,
        flow_status: view.record.status,
    })
}

/// Returns the first event ID at or after `next_event_id` in the current attempt's lineage, or
/// zero when the lineage has none.
fn first_lineage_event_id_from(
    view: &RunView<'_>,
    next_event_id: i64,
) -> Result<i64, ReadModelError> {
    if next_event_id >= view.record.next_event_id {
        return Ok(0);
    }
    let lineage = lineage_cutoffs(view, view.record.current_attempt)?;
    let prefix = run_table_prefix(
        view.rows.flow_hash,
        view.rows.run_id,
        RunTable::HistoryRowMetadata,
    );
    let (mut scan_start, scan_end) = prefix_range(&prefix);
    let mut previous_last_event_id = 0_i64;
    let mut has_seen_record = false;
    loop {
        let page: Vec<(Vec<u8>, HistoryRowMetadata)> = view.transaction.scan_messages(
            ColumnFamily::Flow,
            &scan_start,
            &scan_end,
            HISTORY_METADATA_SCAN_ROWS,
            "history metadata",
        )?;
        let is_last_page = page.len() < HISTORY_METADATA_SCAN_ROWS;
        for (key, metadata) in page {
            let (seq, history_table, _) = metadata_identity(&key, &prefix)?;
            scan_start = key;
            scan_start.push(0);
            if history_table != RunTable::HistoryRecord {
                continue;
            }
            // Pruning keeps the FlowStart record, so pruned records only follow the first one.
            let events_after = if has_seen_record {
                previous_last_event_id.max(view.record.max_pruned_event_id)
            } else {
                previous_last_event_id
            };
            has_seen_record = true;
            let is_in_lineage = lineage
                .get(&metadata.attempt)
                .is_some_and(|cutoff| seq <= *cutoff);
            let last_event_id = metadata.last_assigned_event_id;
            let is_unknown_range = last_event_id == 0;
            if is_in_lineage
                && (is_unknown_range
                    || (last_event_id >= next_event_id && last_event_id > events_after))
            {
                return Ok(next_event_id.max(events_after.saturating_add(1)));
            }
            previous_last_event_id = previous_last_event_id.max(last_event_id);
        }
        if is_last_page {
            return Ok(0);
        }
    }
}

/// Test oracle: answers like [`is_history_event_available`] from the lineage's History rows.
#[cfg(feature = "test-util")]
pub fn is_history_event_available_from_records_for_test(
    reader: &dyn FlowStateReader,
    flow_hash: FlowHash,
    run_id: RunId,
    next_event_id: i64,
) -> Result<WaitForHistoryEventResponse, ReadModelError> {
    if next_event_id <= 0 {
        return Err(ReadModelError::InvalidRequest {
            detail: "next event ID must be positive".to_owned(),
        });
    }
    let view = RunView::open(reader, flow_hash, run_id)?;
    let page = crate::read_model::history::get_history_events(
        reader,
        flow_hash,
        run_id,
        next_event_id,
        1,
        &[],
        0,
    )?;
    let available_id = page.events.first().map_or(0, |event| event.event_id);
    Ok(WaitForHistoryEventResponse {
        event_available: available_id > 0,
        available_internal_event_id: available_id,
        flow_status: view.record.status,
    })
}

fn validate_operand(
    operand: &Value,
    operator: AttributeMatchOperator,
) -> Result<(), ReadModelError> {
    let is_ordering = !matches!(
        operator,
        AttributeMatchOperator::Equal | AttributeMatchOperator::NotEqual
    );
    let is_valid = match operand.kind.as_ref() {
        Some(value::Kind::StringValue(_) | value::Kind::BoolValue(_)) => !is_ordering,
        Some(value::Kind::IntValue(_)) => true,
        Some(value::Kind::DoubleValue(number)) => number.is_finite(),
        _ => false,
    };
    if is_valid {
        Ok(())
    } else {
        Err(ReadModelError::InvalidRequest {
            detail: "attribute match operand or operator is unsupported".to_owned(),
        })
    }
}

fn compare_equal(is_equal: bool, operator: AttributeMatchOperator) -> bool {
    match operator {
        AttributeMatchOperator::Equal => is_equal,
        AttributeMatchOperator::NotEqual => !is_equal,
        _ => false,
    }
}

fn compare_order<T: PartialOrd + PartialEq>(
    left: T,
    right: T,
    operator: AttributeMatchOperator,
) -> bool {
    match operator {
        AttributeMatchOperator::Equal => left == right,
        AttributeMatchOperator::NotEqual => left != right,
        AttributeMatchOperator::GreaterThan => left > right,
        AttributeMatchOperator::GreaterThanOrEqual => left >= right,
        AttributeMatchOperator::LessThan => left < right,
        AttributeMatchOperator::LessThanOrEqual => left <= right,
        AttributeMatchOperator::Unspecified => false,
    }
}
