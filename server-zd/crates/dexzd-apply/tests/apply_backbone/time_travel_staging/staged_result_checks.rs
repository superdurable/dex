// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Staged results apply rejects: malformed chunks, mismatched stagings, misplaced input,
//! unrecorded values, and computations over budget.

use super::*;

fn is_accepted(outcome: &ApplyOutcome) -> bool {
    matches!(outcome.reply.reply, Some(command_reply::Reply::Ok(_)))
}

/// Decodes the chunks of `stages`, lets `edit` change them, and returns them as the chunks of
/// staging `staging_id`.
fn restaged(
    stages: &[TimeTravelStageCommand],
    staging_id: &[u8],
    edit: impl FnOnce(&mut Vec<TimeTravelStagedChunk>),
) -> Vec<TimeTravelStageCommand> {
    let mut chunks: Vec<TimeTravelStagedChunk> = stages
        .iter()
        .map(|stage| TimeTravelStagedChunk::decode(stage.chunk.as_slice()).expect("chunk decodes"))
        .collect();
    edit(&mut chunks);
    let chunk_count = chunks.len();
    chunks
        .into_iter()
        .enumerate()
        .map(|(index, chunk)| TimeTravelStageCommand {
            staging_id: staging_id.to_vec(),
            chunk_index: u32::try_from(index).expect("a test stages few chunks"),
            is_last_chunk: index + 1 == chunk_count,
            chunk: chunk.encode_to_vec(),
        })
        .collect()
}

/// A run whose Beginning reset reapplies two Attribute writes, and that reset's result.
fn run_with_reapplied_writes() -> (Harness, FlowCommand, ComputedTimeTravel) {
    let mut harness = Harness::new("order-1");
    harness.start(start_request("start"));
    harness.set_attribute("first", string_value("1"), "set-1");
    harness.set_attribute("second", string_value("2"), "set-2");
    let travel = harness.travel_command(reset_to(FlowResetType::Beginning, true));
    let computed = computed(harness.compute_travel(&travel, &NoOffloadedValues, u64::MAX));
    (harness, travel, computed)
}

/// Changes one field of a TimeTravel command.
type CommandChange = fn(&mut TimeTravelCommand);

/// A TimeTravel with another request, computed seq, or target point than its staging is
/// rejected.
#[test]
fn a_time_travel_that_does_not_match_its_staging_is_rejected() {
    let (mut harness, travel, computed) = run_with_reapplied_writes();
    let attempt = computed.run_attempt();
    let mismatches: [(&str, CommandChange); 3] = [
        ("a different request", |command| {
            command.request.as_mut().expect("a request").reason = "another reason".to_owned();
        }),
        ("but the command names run seq", |command| {
            command.computed_through_seq -= 1;
        }),
        ("the target point", |command| command.target_run_seq += 1),
    ];
    for (index, (expected_detail, change)) in mismatches.into_iter().enumerate() {
        let staging_id = format!("mismatch-{index}").into_bytes();
        let stages = computed
            .stage_commands(&staging_id)
            .expect("the result stages");
        assert!(
            harness
                .apply_stages_at(stages, attempt)
                .iter()
                .all(is_accepted)
        );
        let mut body = computed.time_travel_command(&staging_id, true);
        change(&mut body);
        let rejected = harness.apply_time_travel_at(body, attempt);
        assert!(
            is_error(&rejected.reply, INVALID_ARGUMENT),
            "{:?}",
            rejected.reply
        );
        assert!(
            error_message(&rejected.reply).contains(expected_detail),
            "{}",
            error_message(&rejected.reply)
        );
        assert!(rejected.writes.is_empty());
    }
    let outcome = harness.apply_staged(&travel, &computed, b"matching");
    assert!(is_time_travel(&outcome.reply), "{:?}", outcome.reply);
}

/// Staged input must lie after the target through the computed seq, increasing across
/// chunks.
#[test]
fn staged_input_out_of_range_or_order_is_rejected() {
    let (mut harness, _, computed) = run_with_reapplied_writes();
    let attempt = computed.run_attempt();
    let stages = computed.stage_commands(b"base").expect("the result stages");
    let last_seq = computed.computed_through_seq();
    let split_reversed = restaged(&stages, b"reversed", |chunks| {
        let inputs = std::mem::take(&mut chunks[0].reapplied_inputs);
        assert_eq!(inputs.len(), 2, "both writes are reapplied");
        for input in inputs.into_iter().rev() {
            chunks.push(TimeTravelStagedChunk {
                reapplied_inputs: vec![input],
                ..TimeTravelStagedChunk::default()
            });
        }
    });
    let after_computed_seq = restaged(&stages, b"late", |chunks| {
        chunks[0].reapplied_inputs[1].run_seq = last_seq + 1;
    });
    for (staging_id, stages, expected_detail) in [
        (&b"reversed"[..], split_reversed, "out of order or repeated"),
        (&b"late"[..], after_computed_seq, "is outside run seqs"),
    ] {
        assert!(
            harness
                .apply_stages_at(stages, attempt)
                .iter()
                .all(is_accepted)
        );
        let rejected =
            harness.apply_time_travel_at(computed.time_travel_command(staging_id, true), attempt);
        assert!(
            is_error(&rejected.reply, INVALID_ARGUMENT),
            "{:?}",
            rejected.reply
        );
        assert!(
            error_message(&rejected.reply).contains(expected_detail),
            "{}",
            error_message(&rejected.reply)
        );
        assert!(rejected.writes.is_empty());
    }
}

/// A malformed chunk is rejected before it is stored; one from another run attempt is out of
/// date.
#[test]
fn a_malformed_stage_chunk_is_rejected_before_it_is_stored() {
    let (mut harness, _, computed) = run_with_reapplied_writes();
    let attempt = computed.run_attempt();
    let stages = computed.stage_commands(b"base").expect("the result stages");
    let last_seq = computed.computed_through_seq();
    let target_edit = |edit: fn(&mut TimeTravelStagedTarget)| {
        move |chunks: &mut Vec<TimeTravelStagedChunk>| {
            edit(chunks[0].target.as_mut().expect("chunk 0 has the target"));
        }
    };
    let malformed: Vec<(&str, Vec<TimeTravelStageCommand>)> = vec![
        (
            "outside the attempt",
            restaged(&stages, b"m-table", |chunks| {
                chunks[0].rows[0].table = u32::from(RunTable::RunRecord as u8);
            }),
        ),
        (
            "has no key",
            restaged(&stages, b"m-key", |chunks| {
                chunks[0].rows[0].key_suffix.clear()
            }),
        ),
        (
            "Timer row has a malformed key",
            restaged(&stages, b"m-timer-key", |chunks| {
                let row = &mut chunks[0].rows[0];
                row.table = u32::from(RunTable::Timer as u8);
                row.key_suffix = vec![0xff];
                row.value = dexzd_protocol::dexzd::v1::TimerRecord::default().encode_to_vec();
            }),
        ),
        (
            "ChannelMessage row has a malformed key",
            restaged(&stages, b"m-message-key", |chunks| {
                let row = &mut chunks[0].rows[0];
                row.table = u32::from(RunTable::ChannelMessage as u8);
                row.key_suffix = vec![0x00, 0x05, b'a'];
                row.value = Vec::new();
            }),
        ),
        (
            "does not decode",
            restaged(&stages, b"m-row", |chunks| {
                chunks[0].rows[0].value = vec![0xff, 0xff, 0xff];
            }),
        ),
        (
            "no ID or no value",
            restaged(&stages, b"m-value", |chunks| {
                chunks[0].values.push(TimeTravelStagedValue {
                    value_id: Some(StoredValueId {
                        run_seq: 1,
                        index_in_command: 0,
                    }),
                    value: None,
                });
            }),
        ),
        (
            "not a reapplied kind",
            restaged(&stages, b"m-kind", |chunks| {
                let stop = harness.command(Command::FlowStop(FlowStopCommand::default()));
                chunks[0].reapplied_inputs[0].command = Some(stop);
            }),
        ),
        (
            "out of order",
            restaged(&stages, b"m-order", |chunks| {
                let first = chunks[0].reapplied_inputs[0].clone();
                chunks[0].reapplied_inputs.push(first);
            }),
        ),
        (
            "has no run status",
            restaged(
                &stages,
                b"m-status",
                target_edit(|target| target.run_status = None),
            ),
        ),
        (
            "has run status 0",
            restaged(
                &stages,
                b"m-unknown",
                target_edit(|target| {
                    target.run_status.as_mut().expect("a run status").status = 0;
                }),
            ),
        ),
        (
            "has no request",
            restaged(
                &stages,
                b"m-request",
                target_edit(|target| target.request = None),
            ),
        ),
        (
            "do not fit the run's seqs",
            restaged(&stages, b"m-seq", |chunks| {
                chunks[0]
                    .target
                    .as_mut()
                    .expect("chunk 0 has the target")
                    .computed_through_seq = last_seq + 1;
            }),
        ),
    ];
    for (expected_detail, stages) in malformed {
        let outcomes = harness.apply_stages_at(stages, attempt);
        let rejected = outcomes.last().expect("one chunk");
        assert!(
            is_error(&rejected.reply, INVALID_ARGUMENT),
            "{expected_detail}: {:?}",
            rejected.reply
        );
        assert!(
            error_message(&rejected.reply).contains(expected_detail),
            "{}",
            error_message(&rejected.reply)
        );
        assert!(rejected.writes.is_empty());
        assert!(harness.run_record().time_travel_staging.is_none());
    }
    let other_attempt = restaged(
        &stages,
        b"m-attempt",
        target_edit(|target| target.expected_run_attempt += 1),
    );
    let stale = harness.apply_stages_at(other_attempt, attempt);
    assert!(is_error(&stale[0].reply, ABORTED), "{:?}", stale[0].reply);
    assert!(harness.staging_chunk_indexes().is_empty());
}

/// A staged value is restored once, over its birth record, with its recorded size; otherwise
/// apply names it.
#[test]
fn a_staged_value_is_restored_only_over_its_birth_record_with_its_size() {
    let mut harness = Harness::new("order-1");
    harness.start(start_request("start"));
    harness.set_attribute("report", large_text('o', 300), "set-old");
    let old_seq = harness.run_record().next_run_seq - 1;
    let old_value = table_value_id(
        harness
            .attribute("report")
            .expect("the report exists")
            .value
            .as_ref(),
    );
    harness.set_attribute("report", large_text('n', 300), "set-new");
    let mut pruned = harness.twin();
    let travel = harness.travel_command(reset_to_time(harness.history_timestamp(old_seq)));
    let mut removed = RemovedValues::default();
    removed
        .values
        .insert(old_value, harness.offload_superseded_value(old_value));
    let staged = computed(harness.compute_travel(&travel, &removed, u64::MAX));
    assert_eq!(staged.unstored_value_ids_for_test(), vec![old_value]);
    let attempt = staged.run_attempt();
    let stages = staged.stage_commands(b"base").expect("the result stages");
    let old_reference = harness.value_reference(old_value.run_seq, old_value.index_in_command);

    let twice_in_one_chunk = restaged(&stages, b"twice-in-chunk", |chunks| {
        let value = chunks[0].values[0].clone();
        chunks[0].values.push(value);
    });
    let rejected_stage = harness.apply_stages_at(twice_in_one_chunk, attempt);
    assert!(is_error(&rejected_stage[0].reply, INVALID_ARGUMENT));

    let resized = restaged(&stages, b"resized", |chunks| {
        chunks[0].values[0].value = Some(large_text('x', 301));
    });
    let twice_across_chunks = restaged(&stages, b"twice", |chunks| {
        let value = chunks[0].values.remove(0);
        for _ in 0..2 {
            chunks.push(TimeTravelStagedChunk {
                values: vec![value.clone()],
                ..TimeTravelStagedChunk::default()
            });
        }
    });
    for (staging_id, stages, expected_detail) in [
        (&b"resized"[..], resized, "bytes, but the run recorded"),
        (&b"twice"[..], twice_across_chunks, "twice"),
    ] {
        assert!(
            harness
                .apply_stages_at(stages, attempt)
                .iter()
                .all(is_accepted)
        );
        let rejected =
            harness.apply_time_travel_at(staged.time_travel_command(staging_id, true), attempt);
        assert!(is_error(&rejected.reply, ABORTED), "{:?}", rejected.reply);
        let message = error_message(&rejected.reply);
        assert!(
            message.contains(expected_detail) && message.contains(&old_reference),
            "{message}"
        );
        assert!(rejected.writes.is_empty());
    }

    pruned.prune_superseded_value(old_value);
    assert!(
        pruned
            .apply_stages_at(staged.stage_commands(b"pruned").expect("stages"), attempt)
            .iter()
            .all(is_accepted)
    );
    let no_birth =
        pruned.apply_time_travel_at(staged.time_travel_command(b"pruned", true), attempt);
    assert!(is_error(&no_birth.reply, ABORTED), "{:?}", no_birth.reply);
    assert!(
        error_message(&no_birth.reply).contains("no birth record"),
        "{}",
        error_message(&no_birth.reply)
    );

    let restored = harness.apply_staged(&travel, &staged, b"valid");
    assert!(is_time_travel(&restored.reply), "{:?}", restored.reply);
    assert_eq!(harness.holder_count(old_value), 1);
}

/// A Beginning reset replays a small checkpoint, but its large reapplied writes exceed the
/// budget.
#[test]
fn a_beginning_reset_that_holds_more_input_than_its_budget_is_rejected() {
    let mut harness = Harness::new("order-1");
    harness.start(start_request("start"));
    for index in 0..8 {
        harness.set_attribute(
            &format!("document-{index}"),
            large_text('d', 64 << 10),
            &format!("set-{index}"),
        );
    }
    let budget_bytes = 256 << 10;
    let reapplying = harness.travel_command(reset_to(FlowResetType::Beginning, true));
    let TimeTravelComputation::Rejected(reply) =
        harness.compute_travel(&reapplying, &NoOffloadedValues, budget_bytes)
    else {
        panic!("eight 64 KiB writes do not fit in a 256 KiB budget");
    };
    assert!(is_error(&reply, RESOURCE_EXHAUSTED), "{reply:?}");
    let message = error_message(&reply);
    assert!(
        message.contains("external input")
            && message.contains("history.timeTravelReplayMemoryBytes")
            && message.contains("skip_writes_reapply"),
        "{message}"
    );
    let skipping = harness.travel_command(reset_to(FlowResetType::Beginning, false));
    assert!(matches!(
        harness.compute_travel(&skipping, &NoOffloadedValues, budget_bytes),
        TimeTravelComputation::Computed(_)
    ));
    assert!(matches!(
        harness.compute_travel(&reapplying, &NoOffloadedValues, 4 << 20),
        TimeTravelComputation::Computed(_)
    ));
}

/// A Beginning reset without reapplied writes still reads the current attempt; its many held
/// values exceed the budget.
#[test]
fn a_reset_whose_current_attempt_holds_more_than_its_budget_is_rejected() {
    let mut harness = Harness::new("order-1");
    harness.start(start_request("start"));
    let attributes = (0..600)
        .map(|index| AttributeWrite {
            key: format!("document-{index}"),
            value: Some(large_text('d', 150)),
            ..AttributeWrite::default()
        })
        .collect();
    let set = harness.apply(Command::AttributesSet(AttributesSetCommand {
        request: Some(SetAttributesRequest {
            attributes,
            request_id: "set-many".to_owned(),
            ..SetAttributesRequest::default()
        }),
        hard_delete_objects: Vec::new(),
        request_fingerprint: b"set-many".to_vec(),
    }));
    assert!(
        !matches!(set.reply.reply, Some(command_reply::Reply::Error(_))),
        "{:?}",
        set.reply
    );
    let travel = harness.travel_command(reset_to(FlowResetType::Beginning, false));
    let budget_bytes = 32 << 10;
    let TimeTravelComputation::Rejected(reply) =
        harness.compute_travel(&travel, &NoOffloadedValues, budget_bytes)
    else {
        panic!("600 held values do not fit in a 32 KiB budget");
    };
    assert!(is_error(&reply, RESOURCE_EXHAUSTED), "{reply:?}");
    let message = error_message(&reply);
    assert!(message.contains("current attempt state"), "{message}");
    assert!(matches!(
        harness.compute_travel(&travel, &NoOffloadedValues, 1 << 20),
        TimeTravelComputation::Computed(_)
    ));
}
