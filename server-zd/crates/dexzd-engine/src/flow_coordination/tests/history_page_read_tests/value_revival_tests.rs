// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Reviving offloaded values before a Worker result, Client write, or RPC result that
//! references them (plan section 5.11).

use dexzd_apply::read_model::OffloadedValueLocation;
use dexzd_keyspace::ValueReference;
use dexzd_protocol::dex::{
    InvokeExecuteMethodResponse, InvokeRpcRequest, InvokeWorkerRpcResponse, StepDecision,
    StepMovement, StepOptions,
};
use dexzd_protocol::dexzd::v1::{
    CommandReply, StepAttemptStartedCommand, StepExecutionRecord, StepInvocationKey, StepMethod,
    StepMethodCompletedCommand, ValueReviveCommand, step_method_completed_command,
};

use super::load_blob_read_tests::{offload_profiles, offloading_settings};
use super::*;
use crate::effects::FlowEffectOperations;
use crate::flow_coordination::{CommitUrgency, FlowAddress, WorkerRpcResult};

const START_STEP: &str = "ChargeStep";
const TASK_ID: [u8; 16] = [7; 16];

impl OffloadedRun {
    /// A run whose start Step's Execute attempt is running.
    fn start_with_running_step() -> Self {
        let mut run = Self::start_with_request(
            offloading_settings(1),
            StartFlowRequest {
                flow_id: FLOW_ID.to_owned(),
                flow_type: "OffloadedFlow".to_owned(),
                start_step_type: START_STEP.to_owned(),
                step_options: Some(StepOptions {
                    skip_wait_for: true,
                    ..StepOptions::default()
                }),
                request_id: "start".to_owned(),
                ..StartFlowRequest::default()
            },
        );
        let started = run.apply(Command::StepAttemptStarted(StepAttemptStartedCommand {
            invocation: Some(start_invocation()),
            attempt: 1,
            task_id: TASK_ID.to_vec(),
            pull_routing: None,
        }));
        assert!(
            matches!(started.reply.reply, Some(command_reply::Reply::Ok(_))),
            "{:?}",
            started.reply
        );
        run
    }

    /// A harness over the run's store that applies with the real apply and counts revivals in
    /// `metrics`.
    fn reviving_harness(&self, metrics: Arc<NodeMetrics>) -> FlowCoordinationHarness {
        let harness = self.harness(self.reader(&HistoryReadsConfig::default(), metrics));
        harness
            .applier
            .uses_real_apply
            .store(true, Ordering::SeqCst);
        harness
    }

    fn command(&self, body: Command) -> FlowCommand {
        FlowCommand {
            flow_id: FLOW_ID.to_owned(),
            run_id: self.run_id.as_bytes().to_vec(),
            tenant_id: TENANT.0,
            command: Some(body),
            ..FlowCommand::default()
        }
    }

    fn has_step_execution(&self, step_execution_id: &str) -> bool {
        let prefix = run_table_prefix(self.context.flow_hash, self.run_id, RunTable::StepExecution);
        let (start, end) = prefix_range(&prefix);
        self.rows_in(ColumnFamily::Flow, &start, &end)
            .iter()
            .any(|(_, row)| {
                StepExecutionRecord::decode(row.as_slice())
                    .expect("the Step row decodes")
                    .step_execution_id
                    == step_execution_id
            })
    }

    fn has_value_row(&self, reference: &str) -> bool {
        use dexzd_apply::FlowStateReader;
        let reference: ValueReference = reference.parse().expect("a value reference");
        self.store
            .view()
            .get(
                ColumnFamily::Value,
                &dexzd_keyspace::row_keys::value_row_key(
                    self.context.flow_hash,
                    self.run_id,
                    RunTable::Value,
                    reference.value_id,
                ),
            )
            .expect("the memory view reads")
            .is_some()
    }
}

fn start_invocation() -> StepInvocationKey {
    StepInvocationKey {
        step_execution_id: format!("{START_STEP}-1"),
        method: StepMethod::Execute as i32,
    }
}

fn reference_value(reference: &str) -> Value {
    Value {
        kind: Some(value::Kind::InternalBlobIdForStringValue(
            reference.to_owned(),
        )),
    }
}

fn next_step_decision(reference: &str) -> StepDecision {
    StepDecision {
        next_steps: vec![StepMovement {
            step_type: "ReportStep".to_owned(),
            step_input: Some(reference_value(reference)),
            ..StepMovement::default()
        }],
        ..StepDecision::default()
    }
}

fn copy_command(run: &OffloadedRun, reference: &str) -> FlowCommand {
    run.command(Command::AttributesSet(AttributesSetCommand {
        request: Some(SetAttributesRequest {
            flow_id: FLOW_ID.to_owned(),
            attributes: vec![AttributeWrite {
                key: "copy".to_owned(),
                value: Some(reference_value(reference)),
                ..AttributeWrite::default()
            }],
            request_id: "copy".to_owned(),
            ..SetAttributesRequest::default()
        }),
        hard_delete_objects: Vec::new(),
        request_fingerprint: b"copy".to_vec(),
    }))
}

/// Proposes `command` as an effect does and returns its committed reply.
async fn propose_committed(
    harness: &FlowCoordinationHarness,
    command: FlowCommand,
) -> CommandReply {
    let flow = FlowAddress::new(TENANT, FLOW_ID.to_owned());
    harness
        .drive(
            harness
                .registry
                .propose_committed(&flow, command, CommitUrgency::Prompt),
        )
        .await
        .expect("the command commits")
}

fn submitted_kinds(harness: &FlowCoordinationHarness) -> Vec<&'static str> {
    harness
        .submitted()
        .iter()
        .map(|command| match command.command {
            Some(Command::ValueRevive(_)) => "ValueRevive",
            Some(Command::AttributesSet(_)) => "AttributesSet",
            Some(Command::StepMethodCompleted(_)) => "StepMethodCompleted",
            Some(Command::RpcCommitted(_)) => "RpcCommitted",
            _ => "other",
        })
        .collect()
}

fn is_ok(reply: &CommandReply) -> bool {
    matches!(reply.reply, Some(command_reply::Reply::Ok(_)))
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn a_worker_result_that_passes_an_offloaded_value_to_its_next_step_commits_after_a_revive() {
    let mut run = OffloadedRun::start_with_running_step();
    let offloaded = offload_profiles(&mut run, 2).await;
    let (reference, _) = offloaded.first().expect("a value is offloaded").clone();
    assert!(!run.has_value_row(&reference));
    let metrics = Arc::new(NodeMetrics::unregistered());
    let harness = run.reviving_harness(metrics.clone());
    let result = run.command(Command::StepMethodCompleted(StepMethodCompletedCommand {
        invocation: Some(start_invocation()),
        attempt: 1,
        response: Some(step_method_completed_command::Response::Execute(
            InvokeExecuteMethodResponse {
                step_decision: Some(next_step_decision(&reference)),
                ..InvokeExecuteMethodResponse::default()
            },
        )),
        task_id: TASK_ID.to_vec(),
        ..StepMethodCompletedCommand::default()
    }));

    let reply = propose_committed(&harness, result.clone()).await;
    assert!(is_ok(&reply), "{reply:?}");
    assert_eq!(
        submitted_kinds(&harness),
        ["ValueRevive", "StepMethodCompleted"]
    );
    let submitted = harness.submitted();
    assert_eq!(
        submitted[1].command, result.command,
        "the result commits unchanged, keeping its reference"
    );
    assert!(run.has_step_execution("ReportStep-1"), "the Step succeeded");
    assert!(run.has_value_row(&reference), "the value is local again");
    assert_eq!(metrics.history_value_revivals.get(), 1);
    assert!(run.objects.history_reads() > 0);
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn an_rpc_result_that_passes_an_offloaded_value_to_a_next_step_commits_after_a_revive() {
    let mut run = OffloadedRun::start(offloading_settings(1));
    let offloaded = offload_profiles(&mut run, 2).await;
    let (reference, _) = offloaded.first().expect("a value is offloaded").clone();
    let metrics = Arc::new(NodeMetrics::unregistered());
    let harness = run.reviving_harness(metrics.clone());
    harness
        .rpc_invoker
        .results
        .lock()
        .unwrap()
        .push_back(Ok(WorkerRpcResult {
            response: InvokeWorkerRpcResponse {
                step_decision: Some(next_step_decision(&reference)),
                ..InvokeWorkerRpcResponse::default()
            },
            app_worker_id: "worker-1".to_owned(),
            pull_routing: None,
        }));

    harness
        .drive(harness.registry.invoke_rpc(
            TENANT,
            InvokeRpcRequest {
                flow_id: FLOW_ID.to_owned(),
                rpc_name: "sendReport".to_owned(),
                request_id: "rpc-1".to_owned(),
                ..InvokeRpcRequest::default()
            },
        ))
        .await
        .expect("the RPC commits");
    assert_eq!(submitted_kinds(&harness), ["ValueRevive", "RpcCommitted"]);
    assert!(run.has_step_execution("ReportStep-1"));
    assert!(run.has_value_row(&reference));
    assert_eq!(metrics.history_value_revivals.get(), 1);
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn a_client_write_that_references_offloaded_values_commits_after_a_revive_of_each() {
    let mut run = OffloadedRun::start(offloading_settings(1));
    let offloaded = offload_profiles(&mut run, 3).await;
    assert!(offloaded.len() >= 2, "{offloaded:?}");
    let metrics = Arc::new(NodeMetrics::unregistered());
    let harness = run.reviving_harness(metrics.clone());
    let mut command = copy_command(&run, &offloaded[0].0);
    if let Some(Command::AttributesSet(set)) = command.command.as_mut() {
        let request = set.request.as_mut().expect("the write has a request");
        request.attributes.push(AttributeWrite {
            key: "second-copy".to_owned(),
            value: Some(reference_value(&offloaded[1].0)),
            ..AttributeWrite::default()
        });
    }

    let reply = propose_committed(&harness, command).await;
    assert!(is_ok(&reply), "{reply:?}");
    assert_eq!(
        submitted_kinds(&harness),
        ["ValueRevive", "ValueRevive", "AttributesSet"]
    );
    assert!(run.has_value_row(&offloaded[0].0));
    assert!(run.has_value_row(&offloaded[1].0));
    assert_eq!(metrics.history_value_revivals.get(), 2);
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn a_reference_the_window_passed_is_rejected_as_before_without_a_revive() {
    let mut run = OffloadedRun::start(RunSettings {
        history_window_events: 4,
        history_prune_chunk_events: 1,
        checkpoint_command_threshold: 2,
        ..offloading_settings(1)
    });
    let offloaded = offload_profiles(&mut run, 2).await;
    let (reference, _) = offloaded.first().expect("a value is offloaded").clone();
    for round in 0..12 {
        run.set_text("other", format!("small {round}"));
        run.upload_all().await;
    }
    let parsed: ValueReference = reference.parse().expect("a value reference");
    assert_eq!(run.locate(parsed.value_id), OffloadedValueLocation::Absent);
    let metrics = Arc::new(NodeMetrics::unregistered());
    let harness = run.reviving_harness(metrics.clone());

    let reply = propose_committed(&harness, copy_command(&run, &reference)).await;
    let Some(command_reply::Reply::Error(error)) = reply.reply else {
        panic!("the reference is rejected: {reply:?}");
    };
    assert_eq!(error.grpc_code, grpc_code::INVALID_ARGUMENT, "{error:?}");
    assert_eq!(submitted_kinds(&harness), ["AttributesSet"]);
    assert_eq!(metrics.history_value_revivals.get(), 0);
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn a_run_purged_while_its_value_is_read_stops_reviving_and_the_command_is_not_found() {
    let mut run = OffloadedRun::start(offloading_settings(1));
    let offloaded = offload_profiles(&mut run, 2).await;
    let (reference, _) = offloaded.first().expect("a value is offloaded").clone();
    let metrics = Arc::new(NodeMetrics::unregistered());
    let harness = run.reviving_harness(metrics.clone());
    run.objects.close_gate();

    let purge = async {
        wait_for_waiting_read(&run.objects).await;
        assert_eq!(
            harness.committer.live_views.load(Ordering::SeqCst),
            0,
            "no committed view is held while the value is read"
        );
        let mut record = run.run_record();
        record.is_purging = true;
        run.store.put(
            ColumnFamily::Flow,
            run.run_record_key(),
            record.encode_to_vec(),
        );
        for segment in run.uploaded_segments() {
            run.objects
                .inner
                .delete(&run.object_key(&segment))
                .await
                .expect("the object deletes");
        }
        run.objects.open_gate();
    };
    let (reply, ()) = tokio::join!(
        propose_committed(&harness, copy_command(&run, &reference)),
        purge
    );
    let Some(command_reply::Reply::Error(error)) = reply.reply else {
        panic!("a purging run is not found: {reply:?}");
    };
    assert_eq!(error.grpc_code, grpc_code::NOT_FOUND, "{error:?}");
    assert_eq!(submitted_kinds(&harness), ["AttributesSet"]);
    assert_eq!(metrics.history_value_revivals.get(), 0);
}

/// A write that copies every offloaded value of `references` into its own Attribute.
fn copy_all_command(run: &OffloadedRun, references: &[String]) -> FlowCommand {
    run.command(Command::AttributesSet(AttributesSetCommand {
        request: Some(SetAttributesRequest {
            flow_id: FLOW_ID.to_owned(),
            attributes: references
                .iter()
                .enumerate()
                .map(|(index, reference)| AttributeWrite {
                    key: format!("copy-{index}"),
                    value: Some(reference_value(reference)),
                    ..AttributeWrite::default()
                })
                .collect(),
            request_id: "copy-all".to_owned(),
            ..SetAttributesRequest::default()
        }),
        hard_delete_objects: Vec::new(),
        request_fingerprint: b"copy-all".to_vec(),
    }))
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn many_referenced_values_are_revived_in_batches_that_fit_the_speculation_budget() {
    let mut run = OffloadedRun::start(offloading_settings(1));
    let offloaded = offload_profiles(&mut run, 7).await;
    assert!(offloaded.len() >= 6, "{offloaded:?}");
    let references: Vec<String> = offloaded
        .iter()
        .map(|(reference, _)| reference.clone())
        .collect();
    // Room for two of the ~320-byte values, so a batch holds two of them.
    let config = FlowCoordinationConfig {
        max_speculative_bytes_per_node: 700,
        ..FlowCoordinationConfig::default()
    };
    const BATCH_VALUES: u64 = 2;
    let metrics = Arc::new(NodeMetrics::unregistered());
    let harness = FlowCoordinationHarness::with_history_reader(
        &config,
        run.store.clone(),
        Arc::new(run.reader(&HistoryReadsConfig::default(), metrics.clone())),
    );
    harness
        .applier
        .uses_real_apply
        .store(true, Ordering::SeqCst);
    let registry = harness.registry.clone();
    let command = copy_all_command(&run, &references);
    let proposal = tokio::spawn(async move {
        let flow = FlowAddress::new(TENANT, FLOW_ID.to_owned());
        registry
            .propose_committed(&flow, command, CommitUrgency::Prompt)
            .await
    });

    // Commit one command at a time. Each value takes a header read and a value read from its
    // own segment; values read but not yet restored must never exceed one batch.
    let restored = |run: &OffloadedRun| {
        references
            .iter()
            .filter(|reference| run.has_value_row(reference))
            .count() as u64
    };
    let mut most_unrestored_reads = 0;
    for _ in 0..WAIT_ROUNDS * 10 {
        if proposal.is_finished() {
            break;
        }
        if harness.committer.pending_count() == 0 {
            tokio::time::sleep(Duration::from_millis(1)).await;
            continue;
        }
        let unrestored_reads = run
            .objects
            .history_reads()
            .saturating_sub(2 * restored(&run));
        most_unrestored_reads = most_unrestored_reads.max(unrestored_reads);
        harness.commit_next();
    }
    let reply = proposal
        .await
        .expect("the proposal task finishes")
        .expect("the write commits");
    assert!(is_ok(&reply), "{reply:?}");
    assert_eq!(restored(&run), references.len() as u64);
    assert!(
        most_unrestored_reads <= 2 * BATCH_VALUES,
        "at most one batch of values was held at once: {most_unrestored_reads} reads"
    );
    let revives = submitted_kinds(&harness)
        .iter()
        .filter(|kind| **kind == "ValueRevive")
        .count();
    assert_eq!(revives, references.len());
    assert_eq!(
        metrics.history_value_revivals.get(),
        references.len() as u64
    );
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn a_revive_that_finds_its_value_already_restored_is_not_counted() {
    let mut run = OffloadedRun::start(offloading_settings(1));
    let offloaded = offload_profiles(&mut run, 2).await;
    let (reference, _) = offloaded.first().expect("a value is offloaded").clone();
    let parsed: ValueReference = reference.parse().expect("a value reference");
    let value = run
        .uploaded
        .values()
        .flat_map(|content| &content.values)
        .find(|row| row.value_id == parsed.value_id)
        .map(|row| {
            dexzd_protocol::dexzd::v1::ValueRecord::decode(row.row.as_slice())
                .expect("the value row decodes")
                .value
        })
        .expect("an uploaded object holds the value");
    let metrics = Arc::new(NodeMetrics::unregistered());
    let harness = run.reviving_harness(metrics.clone());
    run.objects.close_gate();

    // Another request restores the value while this one reads it from object storage.
    let restore_meanwhile = async {
        wait_for_waiting_read(&run.objects).await;
        let mut restoring = OffloadedRun {
            store: run.store.clone(),
            objects: run.objects.clone(),
            context: run.context,
            run_id: run.run_id,
            now_micros: run.now_micros,
            request_count: run.request_count,
            uploaded: BTreeMap::new(),
        };
        let outcome = restoring.apply(Command::ValueRevive(ValueReviveCommand {
            value_run_seq: parsed.value_id.run_seq,
            value_index_in_command: parsed.value_id.index_in_command,
            value,
        }));
        assert!(!outcome.writes.is_empty(), "the other request restores it");
        run.objects.open_gate();
    };
    let (reply, ()) = tokio::join!(
        propose_committed(&harness, copy_command(&run, &reference)),
        restore_meanwhile
    );
    assert!(is_ok(&reply), "{reply:?}");
    assert_eq!(submitted_kinds(&harness), ["ValueRevive", "AttributesSet"]);
    assert_eq!(
        metrics.history_value_revivals.get(),
        0,
        "a revive that wrote nothing restored nothing"
    );
}
