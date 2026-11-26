// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Timer service wake gate and read cursor: when a pass runs and where it starts reading.

use std::sync::Arc;
use std::sync::atomic::Ordering;
use std::time::Duration;

use dexzd_apply::ApplyEffect;
use dexzd_keyspace::{PartitionId, RunId};
use dexzd_protocol::dexzd::v1::TimerId;

use super::timer_tests::{
    fired_commands, seed_timer, sleep_until_micros, timers_fired_override, wait_timer,
    write_timer_rows,
};
use super::{
    DurableState, EffectHarness, MemoryState, SECOND_MICROS, is_timers_fired, ok_override,
    settings, start_request,
};
use crate::effects::EffectsConfig;
use crate::effects::state_reads::RunLocation;
use crate::effects::timer_service::{PartitionTimerService, PartitionTimerWake};
use crate::flow_coordination::FlowCoordinationEffectSink;
use dexzd_clock::WallClock;

const MILLI_MICROS: i64 = 1000;

/// Starts a harness whose TimersFired commands remove the fired timers from the index.
async fn start_harness(config: EffectsConfig) -> EffectHarness {
    let harness = EffectHarness::with_config(DurableState::new(), config);
    harness.rebuild_all_partitions().await;
    harness
        .flow_effect_operations
        .add_override(timers_fired_override());
    harness
}

/// Publishes the committed TimerArmed effect of a timer that `seed_timer` wrote.
fn notify_armed(harness: &EffectHarness, flow_id: &str, fire_at_micros: i64) {
    harness.runner.handle(
        &EffectHarness::flow(flow_id),
        &[ApplyEffect::TimerArmed { fire_at_micros }],
        true,
    );
}

/// Returns every fired timer with the time its TimersFired command was proposed.
fn fired_timers(harness: &EffectHarness) -> Vec<(TimerId, i64)> {
    fired_commands(harness)
        .into_iter()
        .flat_map(|(_, timers, proposed_at)| {
            timers.into_iter().map(move |timer| (timer, proposed_at))
        })
        .collect()
}

/// Returns the proposal times of the TimersFired commands of `run_id` that carry `timer`.
fn fires_of(harness: &EffectHarness, run_id: RunId, timer: &TimerId) -> Vec<i64> {
    fired_commands(harness)
        .into_iter()
        .filter(|(fired_run, timers, _)| {
            fired_run.as_slice() == run_id.as_bytes() && timers.contains(timer)
        })
        .map(|(_, _, proposed_at)| proposed_at)
        .collect()
}

/// Starts a run without Steps, so only seeded timers wake its partition's timer service.
fn start_flow_without_steps(harness: &EffectHarness, flow_id: &str) -> RunId {
    let mut request = start_request(flow_id);
    request.start_step_type.clear();
    harness.start_flow(request, settings())
}

/// A `wait_timer(0)` that a commit racing a pass's snapshot arms.
struct TimerArmedAfterSnapshot {
    partition: PartitionId,
    run: RunLocation,
    fire_at_micros: i64,
    wake: Arc<PartitionTimerWake>,
}

impl TimerArmedAfterSnapshot {
    /// Writes the timer rows into `state`, then reports the armed timer as the router would.
    fn commit_and_notify(self, state: &mut MemoryState) {
        write_timer_rows(
            state,
            self.partition,
            self.run,
            &wait_timer(0),
            self.fire_at_micros,
        );
        self.wake.note_armed(self.fire_at_micros);
    }
}

/// Starts a run and fires its `wait_timer(0)`, moving the read cursor past it.
///
/// Returns the run and the timer's fire time.
async fn fire_first_timer(harness: &EffectHarness, flow_id: &str) -> (RunId, i64) {
    let run_id = start_flow_without_steps(harness, flow_id);
    let fire_at = harness.clock.now_unix_micros() + SECOND_MICROS;
    seed_timer(harness, flow_id, run_id, &wait_timer(0), fire_at);
    notify_armed(harness, flow_id, fire_at);
    harness.settle().await;
    sleep_until_micros(harness, fire_at).await;
    harness.settle().await;
    assert_eq!(
        fires_of(harness, run_id, &wait_timer(0)).len(),
        1,
        "the first timer fires"
    );
    (run_id, fire_at)
}

#[tokio::test(start_paused = true)]
async fn a_timer_due_after_the_planned_wake_fires_without_an_extra_pass() {
    const FLOW_ID: &str = "wake-gate-later";
    let harness = start_harness(EffectsConfig::default()).await;
    let run_id = harness.start_flow(start_request(FLOW_ID), settings());
    let base = harness.clock.now_unix_micros();
    let committed_reads = &harness.flow_effect_operations.committed_reads;
    let reads = || committed_reads.load(Ordering::SeqCst);

    let reads_before_first = reads();
    seed_timer(
        &harness,
        FLOW_ID,
        run_id,
        &wait_timer(0),
        base + 5 * SECOND_MICROS,
    );
    notify_armed(&harness, FLOW_ID, base + 5 * SECOND_MICROS);
    harness.settle().await;
    assert!(
        reads() > reads_before_first,
        "an earlier timer wakes a pass"
    );

    let reads_before_later = reads();
    seed_timer(
        &harness,
        FLOW_ID,
        run_id,
        &wait_timer(1),
        base + 10 * SECOND_MICROS,
    );
    notify_armed(&harness, FLOW_ID, base + 10 * SECOND_MICROS);
    harness.settle().await;
    assert_eq!(reads(), reads_before_later, "a later timer wakes no pass");

    sleep_until_micros(&harness, base + 5 * SECOND_MICROS).await;
    harness.settle().await;
    sleep_until_micros(&harness, base + 10 * SECOND_MICROS - MILLI_MICROS).await;
    harness.settle().await;
    assert_eq!(fires_of(&harness, run_id, &wait_timer(0)).len(), 1);
    assert!(
        fires_of(&harness, run_id, &wait_timer(1)).is_empty(),
        "no timer fires early"
    );
    sleep_until_micros(&harness, base + 10 * SECOND_MICROS).await;
    harness.settle().await;

    let fires = fires_of(&harness, run_id, &wait_timer(1));
    assert_eq!(fires.len(), 1, "{fires:?}");
    assert!(fires[0] >= base + 10 * SECOND_MICROS);
    assert!(fires[0] < base + 10 * SECOND_MICROS + 10 * MILLI_MICROS);
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_timer_armed_after_the_wall_clock_jumps_forward_wakes_the_sleeping_service() {
    const FLOW_ID: &str = "wake-gate-clock-jump";
    let harness = start_harness(EffectsConfig::default()).await;
    let run_id = start_flow_without_steps(&harness, FLOW_ID);
    harness.settle().await;
    // The empty partition sleeps one 60-second lookahead of monotonic time.
    let slept_at = tokio::time::Instant::now();

    harness.clock.move_back(-120 * SECOND_MICROS);
    let fire_at = harness.clock.now_unix_micros() + SECOND_MICROS;
    seed_timer(&harness, FLOW_ID, run_id, &wait_timer(0), fire_at);
    notify_armed(&harness, FLOW_ID, fire_at);
    harness.settle().await;
    tokio::time::sleep_until(slept_at + Duration::from_millis(1100)).await;
    harness.settle().await;

    let fires = fires_of(&harness, run_id, &wait_timer(0));
    assert_eq!(
        fires.len(),
        1,
        "it fires long before the 60-second wake: {fires:?}"
    );
    assert!(fires[0] >= fire_at);
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_timer_armed_behind_the_read_cursor_fires_at_once() {
    const FLOW_ID: &str = "cursor-behind";
    let harness = start_harness(EffectsConfig::default()).await;
    let (run_id, _) = fire_first_timer(&harness, FLOW_ID).await;

    // A zero-delay retry backoff arms a timer at its command time, already past.
    let armed_at = harness.clock.now_unix_micros();
    let fire_at = armed_at - MILLI_MICROS;
    seed_timer(&harness, FLOW_ID, run_id, &wait_timer(1), fire_at);
    notify_armed(&harness, FLOW_ID, fire_at);
    harness.settle().await;

    let fires = fires_of(&harness, run_id, &wait_timer(1));
    assert_eq!(fires.len(), 1, "{fires:?}");
    assert!(
        fires[0] <= armed_at + MILLI_MICROS,
        "it fires without waiting"
    );
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_timer_armed_while_a_pass_fires_is_not_missed() {
    const FLOW_ID: &str = "armed-during-pass";
    let harness = start_harness(EffectsConfig::default()).await;
    *harness
        .flow_effect_operations
        .proposal_delay
        .lock()
        .expect("lock") = Duration::from_millis(100);
    let run_id = harness.start_flow(start_request(FLOW_ID), settings());
    let first_fire_at = harness.clock.now_unix_micros() + SECOND_MICROS;
    seed_timer(&harness, FLOW_ID, run_id, &wait_timer(0), first_fire_at);
    notify_armed(&harness, FLOW_ID, first_fire_at);
    harness.settle().await;
    sleep_until_micros(&harness, first_fire_at + 10 * MILLI_MICROS).await;
    harness.settle().await;
    assert!(
        fires_of(&harness, run_id, &wait_timer(0)).is_empty(),
        "the first fire is committing"
    );

    // Sorts before the timer that the running pass fires, where its read cursor stands.
    let second_fire_at = first_fire_at - MILLI_MICROS;
    seed_timer(&harness, FLOW_ID, run_id, &wait_timer(1), second_fire_at);
    notify_armed(&harness, FLOW_ID, second_fire_at);
    sleep_until_micros(&harness, first_fire_at + 300 * MILLI_MICROS).await;
    harness.settle().await;

    assert_eq!(fires_of(&harness, run_id, &wait_timer(0)).len(), 1);
    let fires = fires_of(&harness, run_id, &wait_timer(1));
    assert_eq!(fires.len(), 1, "{fires:?}");
    assert!(fires[0] <= first_fire_at + 250 * MILLI_MICROS);
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_restart_reads_the_timer_index_from_the_first_timer_and_fires_overdue_timers() {
    const FLOW_ID: &str = "cursor-restart";
    let first = start_harness(EffectsConfig::default()).await;
    let (run_id, first_fire_at) = fire_first_timer(&first, FLOW_ID).await;
    first.settle().await;
    // A command replayed in recovery arms its timer without notifying the timer service.
    seed_timer(
        &first,
        FLOW_ID,
        run_id,
        &wait_timer(1),
        first_fire_at - MILLI_MICROS,
    );
    first.settle().await;
    assert!(
        fires_of(&first, run_id, &wait_timer(1)).is_empty(),
        "behind the read cursor"
    );
    let durable = first.durable.clone();
    first.shutdown().await;

    let second = EffectHarness::with_config(durable, EffectsConfig::default());
    second
        .flow_effect_operations
        .add_override(timers_fired_override());
    let restarted_at = second.clock.now_unix_micros();
    second.rebuild_all_partitions().await;
    second.settle().await;

    let fires = fires_of(&second, run_id, &wait_timer(1));
    assert_eq!(fires.len(), 1, "{fires:?}");
    assert!(fires[0] <= restarted_at + MILLI_MICROS);
    second.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_partition_rebuild_reads_the_timer_index_from_the_first_timer() {
    const FLOW_ID: &str = "cursor-rebuild";
    let harness = start_harness(EffectsConfig::default()).await;
    let (run_id, first_fire_at) = fire_first_timer(&harness, FLOW_ID).await;
    harness.settle().await;
    seed_timer(
        &harness,
        FLOW_ID,
        run_id,
        &wait_timer(1),
        first_fire_at - MILLI_MICROS,
    );
    harness.settle().await;
    assert!(
        fires_of(&harness, run_id, &wait_timer(1)).is_empty(),
        "no notification, no pass"
    );

    let rebuilt_at = harness.clock.now_unix_micros();
    harness.rebuild_all_partitions().await;
    harness.settle().await;

    let fires = fires_of(&harness, run_id, &wait_timer(1));
    assert_eq!(fires.len(), 1, "{fires:?}");
    assert!(fires[0] <= rebuilt_at + MILLI_MICROS);
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn an_idle_partition_wakes_for_its_full_scan_and_finds_an_unnotified_timer_on_time() {
    const FLOW_ID: &str = "cursor-backstop";
    // Without the full scan's own wake, the idle passes come at 1, 101, and 201 seconds.
    let config = EffectsConfig {
        timer_lookahead: Duration::from_secs(100),
        timer_full_scan_interval: Duration::from_secs(120),
        ..EffectsConfig::default()
    };
    let harness = start_harness(config).await;
    let base = harness.clock.now_unix_micros();
    let (run_id, first_fire_at) = fire_first_timer(&harness, FLOW_ID).await;
    harness.settle().await;
    seed_timer(
        &harness,
        FLOW_ID,
        run_id,
        &wait_timer(1),
        first_fire_at - MILLI_MICROS,
    );

    sleep_until_micros(&harness, base + 119 * SECOND_MICROS).await;
    harness.settle().await;
    assert!(
        fires_of(&harness, run_id, &wait_timer(1)).is_empty(),
        "the read cursor skips it"
    );
    sleep_until_micros(&harness, base + 121 * SECOND_MICROS).await;
    harness.settle().await;
    assert_eq!(
        fires_of(&harness, run_id, &wait_timer(1)).len(),
        1,
        "the full scan at 120 seconds finds it"
    );
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_wall_clock_moved_back_keeps_the_backoff_of_a_fired_timer_still_in_the_index() {
    const FLOW_ID: &str = "clock-rollback";
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    // Apply accepts TimersFired but the timer stays in the index.
    harness
        .flow_effect_operations
        .add_override(ok_override(is_timers_fired));
    let run_id = start_flow_without_steps(&harness, FLOW_ID);
    let fire_at = harness.clock.now_unix_micros() + SECOND_MICROS;
    seed_timer(&harness, FLOW_ID, run_id, &wait_timer(0), fire_at);
    notify_armed(&harness, FLOW_ID, fire_at);
    harness.settle().await;
    sleep_until_micros(&harness, fire_at).await;
    harness.settle().await;
    assert_eq!(fires_of(&harness, run_id, &wait_timer(0)).len(), 1);

    // The wall clock falls below the fire time; the 1-second retry backoff still holds.
    harness.clock.move_back(100 * MILLI_MICROS);
    notify_armed(&harness, FLOW_ID, harness.clock.now_unix_micros());
    harness.settle().await;
    tokio::time::sleep(Duration::from_millis(500)).await;
    harness.settle().await;
    assert_eq!(
        fires_of(&harness, run_id, &wait_timer(0)).len(),
        1,
        "no retry before the backoff ends"
    );
    tokio::time::sleep(Duration::from_millis(600)).await;
    harness.settle().await;
    assert_eq!(fires_of(&harness, run_id, &wait_timer(0)).len(), 2);
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_timer_armed_after_a_pass_takes_its_snapshot_fires_in_the_next_pass() {
    const FLOW_ID: &str = "armed-after-snapshot";
    // No rebuild, so no timer service runs besides the one this test drives.
    let harness = EffectHarness::new();
    harness
        .flow_effect_operations
        .add_override(timers_fired_override());
    let run_id = start_flow_without_steps(&harness, FLOW_ID);
    let partition = harness.partition_of(FLOW_ID);
    let run = harness.run_location(FLOW_ID, run_id);
    let wake = Arc::new(PartitionTimerWake::new(
        Arc::clone(&harness.clock) as Arc<dyn WallClock>
    ));
    let mut service = PartitionTimerService::new(
        Arc::clone(harness.effect_context()),
        partition,
        Arc::clone(&wake),
    );
    let racing_timer = TimerArmedAfterSnapshot {
        partition,
        run,
        fire_at_micros: harness.clock.now_unix_micros() - MILLI_MICROS,
        wake: Arc::clone(&wake),
    };
    *harness
        .flow_effect_operations
        .committed_reader_hook
        .lock()
        .expect("lock") = Some(Box::new(move |state| racing_timer.commit_and_notify(state)));

    let first_pass = service
        .fire_due_timers()
        .await
        .expect("the first pass runs");
    assert!(first_pass.is_some(), "the first snapshot holds no timer");
    assert!(fires_of(&harness, run_id, &wait_timer(0)).is_empty());
    let second_pass = service
        .fire_due_timers()
        .await
        .expect("the second pass runs");
    assert!(second_pass.is_none(), "the second pass fires a timer");
    assert_eq!(fires_of(&harness, run_id, &wait_timer(0)).len(), 1);
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_pass_with_no_due_timer_reads_one_later_timer_row() {
    const FLOW_ID: &str = "no-over-read";
    const DUE_TIMERS: u32 = 1000;
    const LATER_TIMERS: u32 = 1000;
    let harness = start_harness(EffectsConfig::default()).await;
    let run_id = harness.start_flow(start_request(FLOW_ID), settings());
    let base = harness.clock.now_unix_micros();
    for index in 0..DUE_TIMERS {
        seed_timer(
            &harness,
            FLOW_ID,
            run_id,
            &wait_timer(index),
            base + SECOND_MICROS,
        );
    }
    for index in DUE_TIMERS..DUE_TIMERS + LATER_TIMERS {
        seed_timer(
            &harness,
            FLOW_ID,
            run_id,
            &wait_timer(index),
            base + 30 * SECOND_MICROS,
        );
    }
    notify_armed(&harness, FLOW_ID, base + SECOND_MICROS);
    harness.settle().await;
    sleep_until_micros(&harness, base + SECOND_MICROS).await;
    harness.settle().await;
    assert_eq!(fired_timers(&harness).len(), 1000, "the due timers fire");

    let timer_rows = || {
        harness
            .flow_effect_operations
            .committed_timer_rows
            .load(Ordering::SeqCst)
    };
    let rows_before = timer_rows();
    // Wakes a pass without arming a row.
    notify_armed(&harness, FLOW_ID, base + SECOND_MICROS);
    harness.settle().await;
    let rows_read = timer_rows() - rows_before;
    assert_eq!(rows_read, 1, "rows read by a pass with nothing due");
    harness.shutdown().await;
}
