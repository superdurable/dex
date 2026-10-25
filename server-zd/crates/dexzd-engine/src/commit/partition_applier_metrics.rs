// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use super::*;

#[derive(Clone, PartialEq, Message)]
struct HistoryFailureFields {
    #[prost(message, optional, tag = "6")]
    command: Option<HistoryFailureCommand>,
}

#[derive(Clone, PartialEq, Message)]
struct HistoryFailureCommand {
    #[prost(message, optional, tag = "32")]
    step_attempt_failed: Option<StepAttemptFailedCommand>,
}

#[derive(Clone, PartialEq, Message)]
struct RunCloseFields {
    #[prost(int64, tag = "6")]
    started_at_micros: i64,
    #[prost(int32, tag = "8")]
    status: i32,
    #[prost(int64, tag = "9")]
    closed_at_micros: i64,
}

impl FrameApplier {
    pub(super) fn record_committed_metrics(&self, command: &FlowCommand, outcome: &ApplyOutcome) {
        self.record_closed_flow(command, outcome);
        if matches!(
            command.command,
            Some(
                Command::StepAttemptFailed(_)
                    | Command::StepAttemptHeartbeat(_)
                    | Command::StepMethodCompleted(_)
            )
        ) {
            self.record_step_failure(command, outcome);
        }
    }

    fn record_closed_flow(&self, command: &FlowCommand, outcome: &ApplyOutcome) {
        for effect in &outcome.effects {
            let ApplyEffect::WakeWaiters {
                run_id,
                change: WaitableChange::FlowClosed,
            } = effect
            else {
                continue;
            };
            let run_value = outcome.writes.iter().find_map(|write| {
                let StateWrite::Put {
                    column_family: ColumnFamily::Flow,
                    key,
                    value,
                } = write
                else {
                    return None;
                };
                (parse_run_key(key).is_ok_and(|parsed| {
                    parsed.table == RunTable::RunRecord && parsed.run_id == *run_id
                }))
                .then_some(value.as_slice())
            });
            let Some(run_value) = run_value else {
                metric_mapping_gap(&command.command_id, "Flow close has no run record");
                continue;
            };
            let Ok(run) = RunCloseFields::decode(run_value) else {
                metric_mapping_gap(&command.command_id, "Flow close run record is invalid");
                continue;
            };
            let status = match FlowStatus::try_from(run.status) {
                Ok(FlowStatus::Completed) => FlowCloseStatus::Completed,
                Ok(FlowStatus::Failed) => FlowCloseStatus::Failed,
                Ok(FlowStatus::Terminated) => FlowCloseStatus::Terminated,
                Ok(FlowStatus::Canceled) => FlowCloseStatus::Canceled,
                _ => {
                    metric_mapping_gap(&command.command_id, "Flow close has an unmapped status");
                    continue;
                }
            };
            self.metrics.flow_closes.record(status);
            self.metrics.flow_end_to_end_latency.observe(
                run.closed_at_micros
                    .saturating_sub(run.started_at_micros)
                    .max(0) as f64
                    / 1_000_000.0,
            );
        }
    }

    fn record_step_failure(&self, command: &FlowCommand, outcome: &ApplyOutcome) {
        let history_value = outcome.writes.iter().find_map(|write| {
            let StateWrite::Put {
                column_family: ColumnFamily::History,
                key,
                value,
            } = write
            else {
                return None;
            };
            (parse_run_key(key).is_ok_and(|parsed| parsed.table == RunTable::HistoryRecord))
                .then_some(value.as_slice())
        });
        let Some(history_value) = history_value else {
            return;
        };
        let direct_failure = match command.command.as_ref() {
            Some(Command::StepAttemptFailed(failure)) => Some(failure),
            _ => None,
        };
        let projected_failure = if direct_failure.is_none() {
            match projected_step_failure(history_value) {
                Ok(failure) => failure,
                Err(_) => {
                    metric_mapping_gap(&command.command_id, "Step history projection is invalid");
                    return;
                }
            }
        } else {
            None
        };
        let Some(failure) = direct_failure.or(projected_failure.as_ref()) else {
            return;
        };
        let Ok(history) = HistoryRecord::decode(history_value) else {
            metric_mapping_gap(&command.command_id, "Step failure history is invalid");
            return;
        };
        let final_durability = history
            .events
            .iter()
            .find_map(|event| match &event.payload {
                Some(flow_history_event::Payload::StepWaitForFailed(failed)) => {
                    failed.context.as_ref().map(|context| context.durability)
                }
                Some(flow_history_event::Payload::StepExecuteFailed(failed)) => {
                    failed.context.as_ref().map(|context| context.durability)
                }
                _ => None,
            });
        let Some(durability) =
            final_durability.or_else(|| self.failed_step_durability(outcome, failure))
        else {
            metric_mapping_gap(
                &command.command_id,
                "Step failure has no effective durability",
            );
            return;
        };
        let durability = match StepDurability::try_from(durability) {
            Ok(StepDurability::Sync) => MetricStepDurability::Sync,
            Ok(StepDurability::Async) => MetricStepDurability::Async,
            _ => {
                metric_mapping_gap(&command.command_id, "Step failure has unmapped durability");
                return;
            }
        };
        let failure_kind = match StepAttemptFailureKind::try_from(failure.kind) {
            Ok(StepAttemptFailureKind::WorkerError) => StepFailureKind::WorkerError,
            Ok(StepAttemptFailureKind::MethodTimeout) => StepFailureKind::MethodTimeout,
            Ok(StepAttemptFailureKind::HeartbeatTimeout) => StepFailureKind::HeartbeatTimeout,
            Ok(StepAttemptFailureKind::DispatchError) => StepFailureKind::DispatchError,
            Ok(StepAttemptFailureKind::InvalidResponse) => StepFailureKind::InvalidResponse,
            Ok(StepAttemptFailureKind::UnsupportedFeature) => StepFailureKind::UnsupportedFeature,
            Ok(StepAttemptFailureKind::ValueTooLarge) => StepFailureKind::ValueTooLarge,
            Ok(StepAttemptFailureKind::RequestTooLarge) => StepFailureKind::RequestTooLarge,
            Ok(StepAttemptFailureKind::RetryDeadline) => StepFailureKind::RetryDeadline,
            _ => {
                metric_mapping_gap(&command.command_id, "Step failure has unmapped kind");
                return;
            }
        };
        self.metrics
            .step_attempt_failures
            .record(durability, failure_kind);
        if final_durability.is_some() {
            self.metrics.step_retries_exhausted.record(durability);
        }
    }

    fn failed_step_durability(
        &self,
        outcome: &ApplyOutcome,
        failure: &StepAttemptFailedCommand,
    ) -> Option<i32> {
        let step_execution_id = &failure.invocation.as_ref()?.step_execution_id;
        outcome.writes.iter().find_map(|write| {
            let StateWrite::Put {
                column_family: ColumnFamily::Flow,
                key,
                value,
            } = write
            else {
                return None;
            };
            let parsed = parse_run_key(key).ok()?;
            if parsed.table != RunTable::StepExecution
                || parsed.suffix != step_execution_id.as_bytes()
            {
                return None;
            }
            StepExecutionRecord::decode(value.as_slice())
                .ok()?
                .invocation
                .map(|invocation| invocation.effective_durability)
        })
    }
}

fn projected_step_failure(
    history_value: &[u8],
) -> Result<Option<StepAttemptFailedCommand>, prost::DecodeError> {
    Ok(HistoryFailureFields::decode(history_value)?
        .command
        .and_then(|command| command.step_attempt_failed))
}

fn metric_mapping_gap(command_id: &[u8], detail: &str) {
    tracing::error!(command_id = %hex(command_id), detail, "committed metric mapping gap");
    debug_assert!(false, "committed metric mapping gap: {detail}");
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn successful_step_history_skips_full_history_decode() {
        let command = FlowCommand {
            command: Some(Command::StepMethodCompleted(Default::default())),
            ..Default::default()
        };
        let mut history_value = HistoryRecord {
            command: Some(command),
            ..Default::default()
        }
        .encode_to_vec();
        history_value.extend_from_slice(&[0x3a, 0x01, 0xff]);

        assert!(projected_step_failure(&history_value).unwrap().is_none());
        assert!(HistoryRecord::decode(history_value.as_slice()).is_err());
    }
}
