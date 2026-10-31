// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Event IDs that Step executions keep for later events; IDs resolve once a command records events.

use dexzd_protocol::dex::{FlowHistoryEvent, StepMethodEventContext, flow_history_event};

use crate::ApplyError;
use crate::command_handlers::run_cursor::timestamp;
use crate::state_access::{FlowTransaction, RunRows};

/// Position of an event among the events of the command being applied.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) struct HistoryEventIndex(usize);

/// A Step execution field that takes the event ID of one event of the command.
#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) enum StepEventLink {
    /// The event started the Step execution with this ID.
    StartedStepExecution(String),
    /// The event is the StepWaitForCompleted of the Step execution with this ID.
    CompletedWaitFor(String),
}

impl StepEventLink {
    fn step_execution_id(&self) -> &str {
        match self {
            Self::StartedStepExecution(step_execution_id)
            | Self::CompletedWaitFor(step_execution_id) => step_execution_id,
        }
    }
}

/// History events of one command, with the Step execution links that their IDs resolve.
#[derive(Debug, Default)]
pub(crate) struct PendingHistoryEvents {
    payloads: Vec<flow_history_event::Payload>,
    links: Vec<(HistoryEventIndex, StepEventLink)>,
}

impl PendingHistoryEvents {
    pub(crate) fn push(&mut self, payload: flow_history_event::Payload) -> HistoryEventIndex {
        self.payloads.push(payload);
        HistoryEventIndex(self.payloads.len() - 1)
    }

    pub(crate) fn link(&mut self, event: HistoryEventIndex, link: StepEventLink) {
        self.links.push((event, link));
    }

    /// Appends `other` after these events, keeping its links on the same events.
    pub(crate) fn append(&mut self, other: Self) {
        let offset = self.payloads.len();
        self.payloads.extend(other.payloads);
        self.links.extend(
            other
                .links
                .into_iter()
                .map(|(HistoryEventIndex(index), link)| (HistoryEventIndex(index + offset), link)),
        );
    }

    /// Splits the events into their payloads, in order, and their links.
    pub(crate) fn into_parts(
        self,
    ) -> (
        Vec<flow_history_event::Payload>,
        Vec<(HistoryEventIndex, StepEventLink)>,
    ) {
        (self.payloads, self.links)
    }
}

/// Stores each linked event's ID on its Step execution and that Step's later events; zero IDs
/// stay unresolved.
pub(crate) fn resolve_step_event_links(
    transaction: &mut FlowTransaction<'_>,
    rows: RunRows,
    links: &[(HistoryEventIndex, StepEventLink)],
    events: &mut [FlowHistoryEvent],
    event_time_micros: i64,
) -> Result<(), ApplyError> {
    for (HistoryEventIndex(index), link) in links {
        let Some(event_id) = events
            .get(*index)
            .map(|event| event.event_id)
            .filter(|event_id| *event_id > 0)
        else {
            continue;
        };
        let Some(mut step) = rows.step_execution(transaction, link.step_execution_id())? else {
            continue;
        };
        match link {
            StepEventLink::StartedStepExecution(_) => step.from_event_id = event_id,
            StepEventLink::CompletedWaitFor(_) => {
                step.wait_for_completed_event_id = event_id;
                step.wait_for_completed_at_micros = event_time_micros;
            }
        }
        rows.put_step_execution(transaction, &step);
        for later_event in events.iter_mut().skip(index + 1) {
            if let Some(payload) = later_event.payload.as_mut() {
                record_link_in_context(payload, link, event_id, event_time_micros);
            }
        }
    }
    Ok(())
}

/// Returns the replayed events with their recorded IDs, or ID zero where the recorded event
/// differs in payload kind or Step execution.
pub(crate) fn with_recorded_event_ids(
    payloads: Vec<flow_history_event::Payload>,
    recorded_events: &[FlowHistoryEvent],
) -> Vec<FlowHistoryEvent> {
    payloads
        .into_iter()
        .enumerate()
        .map(|(index, payload)| {
            let recorded = recorded_events
                .get(index)
                .filter(|recorded| is_same_recorded_event(recorded, &payload));
            debug_assert!(
                recorded.is_some(),
                "replayed event {index} differs from the recorded event"
            );
            let event_id = recorded.map_or(0, |recorded| recorded.event_id);
            FlowHistoryEvent {
                event_id,
                payload: Some(payload),
                ..FlowHistoryEvent::default()
            }
        })
        .collect()
}

/// Whether a recorded event has the replayed payload's kind and Step execution.
fn is_same_recorded_event(
    recorded: &FlowHistoryEvent,
    payload: &flow_history_event::Payload,
) -> bool {
    recorded.payload.as_ref().is_some_and(|recorded_payload| {
        std::mem::discriminant(recorded_payload) == std::mem::discriminant(payload)
            && method_event_step_execution_id(recorded_payload)
                == method_event_step_execution_id(payload)
    })
}

/// The Step execution ID of a Step method event, or `None` for other events.
fn method_event_step_execution_id(payload: &flow_history_event::Payload) -> Option<&str> {
    use flow_history_event::Payload;
    let context = match payload {
        Payload::StepWaitForCompleted(event) => event.context.as_ref(),
        Payload::StepWaitForFailed(event) => event.context.as_ref(),
        Payload::StepExecuteCompleted(event) => event.context.as_ref(),
        Payload::StepExecuteFailed(event) => event.context.as_ref(),
        Payload::StepWaitForPending(event) | Payload::StepExecutePending(event) => {
            event.context.as_ref()
        }
        Payload::FlowStartedOrContinued(_)
        | Payload::FlowClosed(_)
        | Payload::RpcExecutionCompleted(_)
        | Payload::ChannelExternalPublish(_)
        | Payload::TimeTravelFork(_)
        | Payload::ChannelExternalDelete(_) => None,
    };
    context.map(|context| context.step_execution_id.as_str())
}

/// Copies a resolved link into a Step method event of the same Step execution.
fn record_link_in_context(
    payload: &mut flow_history_event::Payload,
    link: &StepEventLink,
    event_id: i64,
    event_time_micros: i64,
) {
    let Some((context, is_execute)) = method_event_context(payload) else {
        return;
    };
    if context.step_execution_id != link.step_execution_id() {
        return;
    }
    match link {
        StepEventLink::StartedStepExecution(_) => context.from_event_id = event_id,
        StepEventLink::CompletedWaitFor(_) if is_execute => {
            context.wait_for_completed_event_id = event_id;
            context.wait_for_completed_time = Some(timestamp(event_time_micros));
        }
        StepEventLink::CompletedWaitFor(_) => {}
    }
}

/// The context of a Step method event, and whether the event belongs to Execute.
fn method_event_context(
    payload: &mut flow_history_event::Payload,
) -> Option<(&mut StepMethodEventContext, bool)> {
    use flow_history_event::Payload;
    match payload {
        Payload::StepWaitForCompleted(event) => {
            event.context.as_mut().map(|context| (context, false))
        }
        Payload::StepWaitForFailed(event) => event.context.as_mut().map(|context| (context, false)),
        Payload::StepWaitForPending(event) => {
            event.context.as_mut().map(|context| (context, false))
        }
        Payload::StepExecuteCompleted(event) => {
            event.context.as_mut().map(|context| (context, true))
        }
        Payload::StepExecuteFailed(event) => event.context.as_mut().map(|context| (context, true)),
        Payload::StepExecutePending(event) => event.context.as_mut().map(|context| (context, true)),
        Payload::FlowStartedOrContinued(_)
        | Payload::FlowClosed(_)
        | Payload::RpcExecutionCompleted(_)
        | Payload::ChannelExternalPublish(_)
        | Payload::TimeTravelFork(_)
        | Payload::ChannelExternalDelete(_) => None,
    }
}

#[cfg(test)]
mod tests {
    use dexzd_keyspace::{ColumnFamily, FlowHash, PartitionId, RunId};
    use dexzd_protocol::dex::{
        FlowHistoryEvent, RpcExecutionCompletedEvent, StepMethodEventContext,
        StepMethodPendingEvent, StepWaitForCompletedEvent, flow_history_event::Payload,
    };
    use dexzd_protocol::dexzd::v1::StepExecutionRecord;

    use super::{PendingHistoryEvents, StepEventLink, resolve_step_event_links};
    use crate::command_handlers::run_cursor::timestamp;
    use crate::state_access::{FlowTransaction, RunRows};
    use crate::{ApplyContext, FlowStateReader, StateReadError, StateRow};

    struct EmptyState;

    impl FlowStateReader for EmptyState {
        fn get(
            &self,
            _column_family: ColumnFamily,
            _key: &[u8],
        ) -> Result<Option<Vec<u8>>, StateReadError> {
            Ok(None)
        }

        fn scan(
            &self,
            _column_family: ColumnFamily,
            _start: &[u8],
            _end: &[u8],
            _limit: usize,
        ) -> Result<Vec<StateRow>, StateReadError> {
            Ok(Vec::new())
        }
    }

    fn context(step_execution_id: &str) -> Option<StepMethodEventContext> {
        Some(StepMethodEventContext {
            step_execution_id: step_execution_id.to_owned(),
            ..StepMethodEventContext::default()
        })
    }

    fn pending(step_execution_id: &str) -> StepMethodPendingEvent {
        StepMethodPendingEvent {
            context: context(step_execution_id),
            ..StepMethodPendingEvent::default()
        }
    }

    fn pending_context(event: &FlowHistoryEvent) -> &StepMethodEventContext {
        match event.payload.as_ref() {
            Some(Payload::StepWaitForPending(pending) | Payload::StepExecutePending(pending)) => {
                pending
                    .context
                    .as_ref()
                    .expect("pending events have a context")
            }
            other => panic!("expected a pending event, found {other:?}"),
        }
    }

    #[test]
    fn links_fill_step_rows_and_the_later_events_of_the_same_command() {
        let flow_hash = FlowHash(7);
        let rows = RunRows::new(flow_hash, RunId([3; 16]));
        let mut transaction = FlowTransaction::new(
            &EmptyState,
            ApplyContext {
                partition: PartitionId(0),
                flow_hash,
            },
        );
        for step_execution_id in ["ChargeStep-1", "ShipStep-1", "PackStep-1"] {
            rows.put_step_execution(
                &mut transaction,
                &StepExecutionRecord {
                    step_execution_id: step_execution_id.to_owned(),
                    ..StepExecutionRecord::default()
                },
            );
        }
        let mut events = PendingHistoryEvents::default();
        let wait_for = events.push(Payload::StepWaitForCompleted(StepWaitForCompletedEvent {
            context: context("ChargeStep-1"),
            ..StepWaitForCompletedEvent::default()
        }));
        events.link(
            wait_for,
            StepEventLink::CompletedWaitFor("ChargeStep-1".to_owned()),
        );
        let mut reapplied = PendingHistoryEvents::default();
        let rpc = reapplied.push(Payload::RpcExecutionCompleted(
            RpcExecutionCompletedEvent::default(),
        ));
        reapplied.link(
            rpc,
            StepEventLink::StartedStepExecution("ShipStep-1".to_owned()),
        );
        events.append(reapplied);
        events.push(Payload::StepWaitForPending(pending("ShipStep-1")));
        events.push(Payload::StepExecutePending(pending("ChargeStep-1")));
        events.push(Payload::StepExecutePending(pending("PackStep-1")));
        let (payloads, links) = events.into_parts();
        let mut recorded: Vec<FlowHistoryEvent> = payloads
            .into_iter()
            .zip(10..)
            .map(|(payload, event_id)| FlowHistoryEvent {
                event_id,
                payload: Some(payload),
                ..FlowHistoryEvent::default()
            })
            .collect();
        resolve_step_event_links(&mut transaction, rows, &links, &mut recorded, 42)
            .expect("links resolve");

        let charge = rows
            .step_execution(&transaction, "ChargeStep-1")
            .unwrap()
            .unwrap();
        assert_eq!(
            (
                charge.wait_for_completed_event_id,
                charge.wait_for_completed_at_micros
            ),
            (10, 42)
        );
        let ship = rows
            .step_execution(&transaction, "ShipStep-1")
            .unwrap()
            .unwrap();
        assert_eq!(ship.from_event_id, 11, "the appended RPC keeps its link");
        let untouched = rows
            .step_execution(&transaction, "PackStep-1")
            .unwrap()
            .unwrap();
        assert_eq!(
            untouched,
            StepExecutionRecord {
                step_execution_id: "PackStep-1".to_owned(),
                ..StepExecutionRecord::default()
            }
        );
        let ship_pending = pending_context(&recorded[2]);
        assert_eq!(
            (
                ship_pending.from_event_id,
                ship_pending.wait_for_completed_event_id
            ),
            (11, 0),
            "a WaitFor event takes the source but no WaitFor link"
        );
        let charge_pending = pending_context(&recorded[3]);
        assert_eq!(charge_pending.wait_for_completed_event_id, 10);
        assert_eq!(charge_pending.wait_for_completed_time, Some(timestamp(42)));
        assert_eq!(
            pending_context(&recorded[4]),
            &context("PackStep-1").unwrap()
        );
    }

    #[test]
    #[cfg_attr(
        debug_assertions,
        should_panic(expected = "replayed event 0 differs from the recorded event")
    )]
    fn an_event_without_an_id_leaves_its_link_unresolved() {
        let flow_hash = FlowHash(7);
        let rows = RunRows::new(flow_hash, RunId([3; 16]));
        let mut transaction = FlowTransaction::new(
            &EmptyState,
            ApplyContext {
                partition: PartitionId(0),
                flow_hash,
            },
        );
        let step = StepExecutionRecord {
            step_execution_id: "ShipStep-1".to_owned(),
            ..StepExecutionRecord::default()
        };
        rows.put_step_execution(&mut transaction, &step);
        let mut events = PendingHistoryEvents::default();
        let rpc = events.push(Payload::RpcExecutionCompleted(
            RpcExecutionCompletedEvent::default(),
        ));
        events.link(
            rpc,
            StepEventLink::StartedStepExecution("ShipStep-1".to_owned()),
        );
        let (payloads, links) = events.into_parts();
        let mut recorded = super::with_recorded_event_ids(
            payloads,
            &[FlowHistoryEvent {
                event_id: 5,
                payload: Some(Payload::StepWaitForCompleted(
                    StepWaitForCompletedEvent::default(),
                )),
                ..FlowHistoryEvent::default()
            }],
        );
        assert_eq!(
            recorded[0].event_id, 0,
            "a different recorded kind gives no ID"
        );
        resolve_step_event_links(&mut transaction, rows, &links, &mut recorded, 42)
            .expect("links resolve");
        assert_eq!(
            rows.step_execution(&transaction, "ShipStep-1").unwrap(),
            Some(step)
        );
    }
}
