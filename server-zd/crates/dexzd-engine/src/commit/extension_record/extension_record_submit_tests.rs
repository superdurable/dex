// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Submitting extension records through `CommandCommitter`, with a fake embedding application.

use std::collections::BTreeMap;
use std::sync::Arc;
use std::sync::atomic::{AtomicI64, AtomicU64, Ordering};
use std::time::{Duration, Instant};

use bytes::Bytes;
use dexzd_apply::ApplyContext;
use dexzd_clock::WallClock;
use dexzd_cluster_metadata::LeaseValidityDeadline;
use dexzd_keyspace::{FlowHash, PartitionHasher, PartitionId, TenantId};
use dexzd_protocol::dexzd::v1::{ExtensionRecord, FlowCommand, LogPosition, LogRecord, log_record};
use dexzd_storage::{InMemoryLocalStore, RestoredLocalStore};
use prost::Message;
use tokio::sync::mpsc;
use uuid::Uuid;

use super::{
    CommittedRecordKind, ExtensionCommitTicket, ExtensionCommitted, ExtensionStamp,
    ExtensionSubmitError, largest_stamped_extension_record_bytes,
};
use crate::SystemEngineIdSource;
use crate::commit::command_committer::{CommandCommitter, CommandCommitterParts, LogProposal};
use crate::commit::commit_state::{CommitState, NodeStopReason, PendingCommit};
use crate::flow_coordination::{CommitError, CommitUrgency};

const PARTITION_COUNT: u32 = 4;
const EPOCH: u64 = 7;
const CLOCK_START_MICROS: i64 = 1_800_000_000_000_000;
const DEFAULT_MAX_UNAPPLIED_BYTES: u32 = 64 * 1024 * 1024;

fn test_partition_hasher() -> PartitionHasher {
    PartitionHasher::new(b"extension-record", PARTITION_COUNT)
        .expect("the key has 16 bytes and the partition count is a power of two")
}

/// A wall clock that tests set by hand and that counts its reads.
struct SettableWallClock {
    micros: AtomicI64,
    reads: AtomicU64,
}

impl SettableWallClock {
    fn set(&self, micros: i64) {
        self.micros.store(micros, Ordering::SeqCst);
    }

    fn reads(&self) -> u64 {
        self.reads.load(Ordering::SeqCst)
    }
}

impl WallClock for SettableWallClock {
    fn now_unix_micros(&self) -> i64 {
        self.reads.fetch_add(1, Ordering::SeqCst);
        self.micros.load(Ordering::SeqCst)
    }
}

/// What the fake embedding application's apply returns for one record.
#[derive(Debug, PartialEq, Eq)]
struct FakeApplyOutcome {
    routing_key: String,
    applied_payload: Bytes,
}

/// The fake application's payload: its routing key, then the application's bytes.
fn fake_payload(routing_key: &str, body: &[u8]) -> Bytes {
    let mut payload = Vec::with_capacity(1 + routing_key.len() + body.len());
    payload.push(u8::try_from(routing_key.len()).expect("test routing keys are short"));
    payload.extend_from_slice(routing_key.as_bytes());
    payload.extend_from_slice(body);
    Bytes::from(payload)
}

/// A committer of one node, its proposals, and the fake embedding application around it.
struct FakeEmbeddingApplication {
    committer: CommandCommitter,
    state: Arc<CommitState>,
    clock: Arc<SettableWallClock>,
    hasher: PartitionHasher,
    proposals: mpsc::UnboundedReceiver<LogProposal>,
    next_log_seq: u64,
}

impl FakeEmbeddingApplication {
    fn start(record_kind: CommittedRecordKind, max_unapplied_bytes: u32) -> Self {
        let lease_validity_deadline = LeaseValidityDeadline::disarmed();
        lease_validity_deadline.arm_until(Instant::now() + Duration::from_secs(3600));
        let state = Arc::new(CommitState::new(PARTITION_COUNT, lease_validity_deadline));
        let clock = Arc::new(SettableWallClock {
            micros: AtomicI64::new(CLOCK_START_MICROS),
            reads: AtomicU64::new(0),
        });
        let hasher = test_partition_hasher();
        let (proposal_sender, proposals) = mpsc::unbounded_channel();
        let committer = CommandCommitter::new(CommandCommitterParts {
            state: state.clone(),
            store: Arc::new(RestoredLocalStore::Memory(InMemoryLocalStore::new())),
            clock: clock.clone(),
            id_source: Arc::new(SystemEngineIdSource),
            epoch: EPOCH,
            behavior_version: 1,
            max_unapplied_bytes,
            is_checking_replay: false,
            proposals: proposal_sender,
            hasher: hasher.clone(),
            record_kind,
        });
        assert!(state.start_serving(), "the node starts serving once");
        Self {
            committer,
            state,
            clock,
            hasher,
            proposals,
            next_log_seq: 1,
        }
    }

    fn routing_hash(&self, routing_key: &str) -> FlowHash {
        self.hasher.flow_hash(TenantId(0), routing_key)
    }

    fn submit(
        &self,
        routing_key: &str,
        body: &[u8],
    ) -> Result<(ExtensionStamp, ExtensionCommitTicket), ExtensionSubmitError> {
        self.committer.submit_extension_record(
            self.routing_hash(routing_key),
            fake_payload(routing_key, body),
            CommitUrgency::Prompt,
        )
    }

    fn next_proposal(&mut self) -> LogProposal {
        self.proposals
            .try_recv()
            .expect("the committer enqueued a proposal")
    }

    fn assert_no_proposal(&mut self) {
        assert!(
            self.proposals.try_recv().is_err(),
            "the committer enqueued nothing"
        );
    }

    /// Stands in for the frame applier: applies the fake payload and acknowledges its submitter.
    fn apply_and_acknowledge(&mut self, proposal: &LogProposal) -> ExtensionRecord {
        let record = decode_extension_record(proposal);
        let routing_key_length = usize::from(record.payload[0]);
        let outcome = FakeApplyOutcome {
            routing_key: String::from_utf8(record.payload[1..=routing_key_length].to_vec())
                .expect("the fake payload holds a UTF-8 routing key"),
            applied_payload: record.payload.slice(1 + routing_key_length..),
        };
        let position = LogPosition {
            seq: self.next_log_seq,
            ..LogPosition::default()
        };
        self.next_log_seq += 1;
        let Some(PendingCommit::ExtensionRecord(pending)) = self.state.take(&record.command_id)
        else {
            panic!("an extension record waits for its acknowledgement");
        };
        pending.responder.resolve(Ok(ExtensionCommitted {
            position,
            stamp: stamp_of(&record),
            outcome: Box::new(outcome),
        }));
        record
    }
}

fn decode_extension_record(proposal: &LogProposal) -> ExtensionRecord {
    let log_record = LogRecord::decode(proposal.record.clone()).expect("the record decodes");
    assert_eq!(
        log_record.partition,
        u32::from(proposal.partition.0),
        "the record names its proposal's partition"
    );
    let Some(log_record::Body::ExtensionRecord(record)) = log_record.body else {
        panic!("the proposal holds an extension record");
    };
    record
}

fn stamp_of(record: &ExtensionRecord) -> ExtensionStamp {
    ExtensionStamp {
        epoch: record.epoch,
        command_id: Uuid::from_slice(&record.command_id).expect("command IDs have 16 bytes"),
        proposed_at_micros: record.proposed_at_micros,
        routing_hash: FlowHash(record.routing_hash),
    }
}

async fn wait_for_fake_outcome(ticket: ExtensionCommitTicket) -> (LogPosition, FakeApplyOutcome) {
    let committed = ticket.wait().await.expect("the record commits");
    let outcome = committed
        .outcome
        .downcast::<FakeApplyOutcome>()
        .expect("the outcome is the fake application's type");
    (committed.position, *outcome)
}

#[tokio::test]
async fn commits_a_record_and_hands_its_outcome_back_to_the_submitter() {
    let mut application = FakeEmbeddingApplication::start(
        CommittedRecordKind::ExtensionRecord,
        DEFAULT_MAX_UNAPPLIED_BYTES,
    );

    let (stamp, ticket) = application
        .submit("routing-key-1", b"first body")
        .expect("the record is enqueued");
    let proposal = application.next_proposal();
    assert!(
        proposal.should_wait_for_commit,
        "Prompt urgency seals promptly"
    );
    let record = application.apply_and_acknowledge(&proposal);

    assert_eq!(
        stamp,
        stamp_of(&record),
        "the returned stamp is the logged stamp"
    );
    assert_eq!(stamp.epoch, EPOCH);
    assert_eq!(stamp.proposed_at_micros, CLOCK_START_MICROS);
    assert_eq!(
        stamp.routing_hash,
        application.routing_hash("routing-key-1")
    );
    assert_eq!(
        stamp.command_id.get_version_num(),
        7,
        "command IDs are UUIDv7"
    );
    assert_eq!(record.payload, fake_payload("routing-key-1", b"first body"));
    let (position, outcome) = wait_for_fake_outcome(ticket).await;
    assert_eq!(position.seq, 1);
    assert_eq!(
        outcome,
        FakeApplyOutcome {
            routing_key: "routing-key-1".to_owned(),
            applied_payload: Bytes::from_static(b"first body"),
        }
    );
}

#[tokio::test]
async fn interleaved_records_keep_submission_order_and_their_own_outcomes() {
    let mut application = FakeEmbeddingApplication::start(
        CommittedRecordKind::ExtensionRecord,
        DEFAULT_MAX_UNAPPLIED_BYTES,
    );
    let routing_keys = ["routing-key-a", "routing-key-b", "routing-key-c"];

    let mut tickets = Vec::new();
    for round in 0..3_u8 {
        for routing_key in routing_keys {
            let (stamp, ticket) = application
                .submit(routing_key, &[round])
                .expect("the record is enqueued");
            tickets.push((routing_key, round, stamp, ticket));
        }
    }
    let proposals = (0..tickets.len())
        .map(|_| application.next_proposal())
        .collect::<Vec<_>>();
    application.assert_no_proposal();
    for (proposal, (_, _, stamp, _)) in proposals.iter().zip(&tickets) {
        assert_eq!(
            stamp_of(&decode_extension_record(proposal)),
            *stamp,
            "proposals arrive in submission order"
        );
    }
    // Acknowledging in reverse order still resolves each ticket with its own outcome.
    for proposal in proposals.iter().rev() {
        application.apply_and_acknowledge(proposal);
    }
    for (routing_key, round, _, ticket) in tickets {
        let (_, outcome) = wait_for_fake_outcome(ticket).await;
        assert_eq!(outcome.routing_key, routing_key);
        assert_eq!(outcome.applied_payload, Bytes::from(vec![round]));
    }
}

#[test]
fn stamps_strictly_increase_per_partition_when_the_clock_stalls_or_moves_back() {
    let application = FakeEmbeddingApplication::start(
        CommittedRecordKind::ExtensionRecord,
        DEFAULT_MAX_UNAPPLIED_BYTES,
    );
    let seeded_partition = application
        .hasher
        .partition_of(application.routing_hash("routing-key-seeded"));
    let seeded_watermark = CLOCK_START_MICROS + 1_000;
    application
        .committer
        .seed_stamp_watermarks(&[(seeded_partition, seeded_watermark)]);

    let mut last_stamp_by_partition = BTreeMap::<PartitionId, i64>::new();
    for index in 0..40_i64 {
        // The clock stalls, then moves back by a second halfway through.
        if index == 20 {
            application.clock.set(CLOCK_START_MICROS - 1_000_000);
        }
        let routing_key = format!("routing-key-{}", index % 8);
        let (stamp, _ticket) = application
            .submit(&routing_key, b"")
            .expect("the record is enqueued");
        let partition = application.hasher.partition_of(stamp.routing_hash);
        if let Some(previous) = last_stamp_by_partition.insert(partition, stamp.proposed_at_micros)
        {
            assert!(
                stamp.proposed_at_micros > previous,
                "stamps of partition {} increase strictly",
                partition.0
            );
        }
    }
    let (stamp, _ticket) = application
        .submit("routing-key-seeded", b"")
        .expect("the record is enqueued");
    assert!(
        stamp.proposed_at_micros > seeded_watermark,
        "stamps start above the applied command timestamp"
    );
}

#[test]
fn extension_records_and_flow_commands_share_a_partition_watermark() {
    let application = FakeEmbeddingApplication::start(
        CommittedRecordKind::ExtensionRecord,
        DEFAULT_MAX_UNAPPLIED_BYTES,
    );
    let (first, _first_ticket) = application
        .submit("routing-key-1", b"")
        .expect("the record is enqueued");
    let partition = application.hasher.partition_of(first.routing_hash);
    let later_watermark = first.proposed_at_micros + 500;
    application
        .committer
        .seed_stamp_watermarks(&[(partition, later_watermark)]);

    let (second, _second_ticket) = application
        .submit("routing-key-1", b"")
        .expect("the record is enqueued");
    assert_eq!(second.proposed_at_micros, later_watermark + 1);
}

#[test]
fn derives_the_partition_from_the_routing_hash() {
    let mut application = FakeEmbeddingApplication::start(
        CommittedRecordKind::ExtensionRecord,
        DEFAULT_MAX_UNAPPLIED_BYTES,
    );
    let mut partitions = std::collections::BTreeSet::new();
    for index in 0..32 {
        let routing_key = format!("routing-key-{index}");
        let routing_hash = application.routing_hash(&routing_key);
        let (_stamp, _ticket) = application
            .submit(&routing_key, b"")
            .expect("the record is enqueued");
        let proposal = application.next_proposal();
        assert_eq!(
            proposal.partition,
            application.hasher.partition_of(routing_hash)
        );
        assert_eq!(
            decode_extension_record(&proposal).routing_hash,
            routing_hash.0
        );
        partitions.insert(proposal.partition);
    }
    assert!(
        partitions.len() > 1,
        "the routing keys span several partitions"
    );
}

#[test]
fn rejects_an_oversized_record_before_stamping() {
    let max_unapplied_bytes = 1024;
    let mut application =
        FakeEmbeddingApplication::start(CommittedRecordKind::ExtensionRecord, max_unapplied_bytes);
    let limit_bytes = usize::try_from(max_unapplied_bytes).expect("fits usize");
    let fitting_body_bytes = (0..limit_bytes)
        .rev()
        .find(|body_bytes| {
            largest_stamped_extension_record_bytes(&fake_payload(
                "routing-key-1",
                &vec![0; *body_bytes],
            )) <= limit_bytes
        })
        .expect("a small body fits");

    let error = application
        .submit("routing-key-1", &vec![0; fitting_body_bytes + 1])
        .expect_err("the record is too large");
    let ExtensionSubmitError::RecordTooLarge {
        record_bytes,
        limit_bytes: reported_limit_bytes,
    } = error
    else {
        panic!("the record is too large, not {error:?}");
    };
    assert!(record_bytes > limit_bytes);
    assert_eq!(reported_limit_bytes, limit_bytes);
    assert_eq!(application.clock.reads(), 0, "nothing was stamped");
    application.assert_no_proposal();

    let (stamp, _ticket) = application
        .submit("routing-key-1", &vec![0; fitting_body_bytes])
        .expect("a record at the limit is enqueued");
    assert_eq!(
        stamp.proposed_at_micros, CLOCK_START_MICROS,
        "the rejected record left the watermark unchanged"
    );
    let proposal = application.next_proposal();
    assert!(proposal.record.len() <= limit_bytes);
}

#[test]
fn caps_records_at_the_log_record_limit_when_the_budget_is_larger() {
    let application = FakeEmbeddingApplication::start(
        CommittedRecordKind::ExtensionRecord,
        DEFAULT_MAX_UNAPPLIED_BYTES,
    );
    let error = application
        .committer
        .submit_extension_record(
            application.routing_hash("routing-key-1"),
            Bytes::from(vec![0; dexzd_log::MAX_RECORD_LENGTH]),
            CommitUrgency::Batched,
        )
        .expect_err("the record is too large");
    assert!(matches!(
        error,
        ExtensionSubmitError::RecordTooLarge {
            limit_bytes: dexzd_log::MAX_RECORD_LENGTH,
            ..
        }
    ));
}

#[test]
fn the_largest_stamped_length_bounds_every_real_stamp() {
    for payload_bytes in [0, 1, 100, 127, 128, 16_383, 16_384, 70_000] {
        let payload = Bytes::from(vec![0xab; payload_bytes]);
        let encoded = LogRecord {
            partition: u32::MAX,
            body: Some(log_record::Body::ExtensionRecord(ExtensionRecord {
                epoch: u64::MAX,
                command_id: vec![0xff; 16],
                proposed_at_micros: i64::MIN,
                routing_hash: u64::MAX,
                payload: payload.clone(),
            })),
        }
        .encoded_len();
        assert_eq!(
            largest_stamped_extension_record_bytes(&payload),
            encoded,
            "payload of {payload_bytes} bytes"
        );
    }
}

#[test]
fn a_flow_command_node_rejects_extension_records() {
    let mut application = FakeEmbeddingApplication::start(
        CommittedRecordKind::FlowCommand,
        DEFAULT_MAX_UNAPPLIED_BYTES,
    );
    let error = application
        .submit("routing-key-1", b"")
        .expect_err("the node commits Flow commands");
    assert_eq!(error, ExtensionSubmitError::WrongRecordKind);
    assert_eq!(application.clock.reads(), 0, "nothing was stamped");
    application.assert_no_proposal();
}

#[test]
fn an_extension_record_node_rejects_flow_commands() {
    let mut application = FakeEmbeddingApplication::start(
        CommittedRecordKind::ExtensionRecord,
        DEFAULT_MAX_UNAPPLIED_BYTES,
    );
    let flow_id = "flow-1";
    let flow_hash = application.hasher.flow_hash(TenantId(0), flow_id);
    let context = ApplyContext {
        partition: application.hasher.partition_of(flow_hash),
        flow_hash,
    };
    let command = FlowCommand {
        flow_id: flow_id.to_owned(),
        ..FlowCommand::default()
    };
    let error = application
        .committer
        .submit(context, command, CommitUrgency::Prompt)
        .expect_err("the node commits extension records");
    assert!(matches!(error, CommitError::Unavailable { .. }));
    assert_eq!(application.clock.reads(), 0, "nothing was stamped");
    application.assert_no_proposal();
}

#[test]
fn a_flow_command_node_still_commits_flow_commands() {
    let mut application = FakeEmbeddingApplication::start(
        CommittedRecordKind::FlowCommand,
        DEFAULT_MAX_UNAPPLIED_BYTES,
    );
    let flow_id = "flow-1";
    let flow_hash = application.hasher.flow_hash(TenantId(0), flow_id);
    let context = ApplyContext {
        partition: application.hasher.partition_of(flow_hash),
        flow_hash,
    };
    let command = FlowCommand {
        flow_id: flow_id.to_owned(),
        ..FlowCommand::default()
    };
    let (stamped, _ticket) = application
        .committer
        .submit(context, command, CommitUrgency::Prompt)
        .expect("the command is enqueued");
    assert_eq!(stamped.epoch, EPOCH);
    assert_eq!(stamped.proposed_at_micros, CLOCK_START_MICROS);
    let proposal = application.next_proposal();
    let log_record = LogRecord::decode(proposal.record).expect("the record decodes");
    assert_eq!(log_record.body, Some(log_record::Body::Flow(stamped)));
    assert!(
        application
            .state
            .take_flow_command(&log_record_command_id(&log_record))
            .is_some(),
        "the Flow command waits for its acknowledgement"
    );
}

fn log_record_command_id(log_record: &LogRecord) -> Vec<u8> {
    match &log_record.body {
        Some(log_record::Body::Flow(command)) => command.command_id.clone(),
        Some(log_record::Body::ExtensionRecord(record)) => record.command_id.clone(),
        None => panic!("the record has a body"),
    }
}

#[test]
fn a_flow_record_never_takes_an_extension_record_waiter() {
    let mut application = FakeEmbeddingApplication::start(
        CommittedRecordKind::ExtensionRecord,
        DEFAULT_MAX_UNAPPLIED_BYTES,
    );
    let (stamp, _ticket) = application
        .submit("routing-key-1", b"")
        .expect("the record is enqueued");
    let proposal = application.next_proposal();
    let command_id = stamp.command_id.as_bytes().to_vec();
    assert!(application.state.take_flow_command(&command_id).is_none());
    application.apply_and_acknowledge(&proposal);
}

#[tokio::test]
async fn a_full_backlog_rejects_records_until_acknowledgements_release_it() {
    let max_unapplied_bytes = 4096;
    let mut application =
        FakeEmbeddingApplication::start(CommittedRecordKind::ExtensionRecord, max_unapplied_bytes);
    let body = vec![0; 1500];
    let (_first_stamp, first_ticket) = application
        .submit("routing-key-1", &body)
        .expect("the first record fits the budget");
    let (_second_stamp, _second_ticket) = application
        .submit("routing-key-2", &body)
        .expect("the second record fits the budget");
    let error = application
        .submit("routing-key-3", &body)
        .expect_err("the backlog is full");
    assert!(matches!(
        error,
        ExtensionSubmitError::Commit(CommitError::Unavailable { .. })
    ));

    let first_proposal = application.next_proposal();
    application.apply_and_acknowledge(&first_proposal);
    wait_for_fake_outcome(first_ticket).await;
    application
        .submit("routing-key-3", &body)
        .expect("the acknowledgement released its budget");
}

#[tokio::test]
async fn stopping_the_node_fails_waiting_extension_records() {
    let application = FakeEmbeddingApplication::start(
        CommittedRecordKind::ExtensionRecord,
        DEFAULT_MAX_UNAPPLIED_BYTES,
    );
    let (_stamp, ticket) = application
        .submit("routing-key-1", b"")
        .expect("the record is enqueued");
    application.state.stop(NodeStopReason::Fenced);

    let error = ticket.wait().await.expect_err("the node stopped");
    assert!(matches!(error, CommitError::NodeStopping { .. }));
    let error = application
        .submit("routing-key-1", b"")
        .expect_err("a stopped node accepts nothing");
    assert!(matches!(
        error,
        ExtensionSubmitError::Commit(CommitError::NodeStopping { .. })
    ));
}

#[test]
fn a_halted_partition_rejects_its_records_only() {
    let mut application = FakeEmbeddingApplication::start(
        CommittedRecordKind::ExtensionRecord,
        DEFAULT_MAX_UNAPPLIED_BYTES,
    );
    let halted_partition = application
        .hasher
        .partition_of(application.routing_hash("routing-key-1"));
    application.state.halt_partition(halted_partition);
    let error = application
        .submit("routing-key-1", b"")
        .expect_err("the partition halted");
    assert!(matches!(
        error,
        ExtensionSubmitError::Commit(CommitError::Unavailable { .. })
    ));
    application.assert_no_proposal();

    let other_key = (0..64)
        .map(|index| format!("routing-key-other-{index}"))
        .find(|key| {
            application
                .hasher
                .partition_of(application.routing_hash(key))
                != halted_partition
        })
        .expect("some routing key selects another partition");
    application
        .submit(&other_key, b"")
        .expect("other partitions keep accepting records");
}

#[test]
fn a_recovering_node_rejects_records() {
    let state = Arc::new(CommitState::new(
        PARTITION_COUNT,
        LeaseValidityDeadline::disarmed(),
    ));
    let (proposal_sender, _proposals) = mpsc::unbounded_channel();
    let hasher = test_partition_hasher();
    let committer = CommandCommitter::new(CommandCommitterParts {
        state,
        store: Arc::new(RestoredLocalStore::Memory(InMemoryLocalStore::new())),
        clock: Arc::new(dexzd_clock::SystemWallClock),
        id_source: Arc::new(SystemEngineIdSource),
        epoch: EPOCH,
        behavior_version: 1,
        max_unapplied_bytes: DEFAULT_MAX_UNAPPLIED_BYTES,
        is_checking_replay: false,
        proposals: proposal_sender,
        hasher: hasher.clone(),
        record_kind: CommittedRecordKind::ExtensionRecord,
    });
    let error = committer
        .submit_extension_record(
            hasher.flow_hash(TenantId(0), "routing-key-1"),
            Bytes::new(),
            CommitUrgency::Prompt,
        )
        .expect_err("the node recovers");
    assert!(matches!(
        error,
        ExtensionSubmitError::Commit(CommitError::Unavailable { .. })
    ));
}
