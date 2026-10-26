// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Engine simulation over a fixed seed set; the crate README lists its environment variables.

use std::collections::BTreeMap;
use std::path::PathBuf;

use dexzd_simulation::{
    EngineSimulationConfig, EngineSimulationFailure, EngineSimulationProfile,
    EngineSimulationSummary, run_engine_seed, run_engine_seed_with_final_state,
};

const DEFAULT_SEED_COUNT: u64 = 64;
const DEFAULT_ACTIVE_SECONDS: u64 = 120;
/// First seed of the fixed set; any constant keeps the set reproducible.
const FIRST_SEED: u64 = 0xe5e5_0000;
const DEFAULT_REDEPLOY_SEED_COUNT: u64 = 16;
/// The redeploy profile changes every trajectory, so its seeds form their own group.
const FIRST_REDEPLOY_SEED: u64 = 0x4ede_0000;
const DEFAULT_STEP_FAILURE_SEED_COUNT: u64 = 32;
/// Step failure policies change every trajectory, so their seeds form their own group.
const FIRST_STEP_FAILURE_SEED: u64 = 0x5fa1_0000;
const REGRESSION_SEEDS: &str = include_str!("../engine-regression-seeds.txt");
/// Canonical command digests with full-rate startup faults; a moved regression seed
/// stops reproducing.
const PINNED_CANONICAL_COMMAND_DIGESTS: &[(EngineSimulationProfile, u64, u64, u128)] = &[
    (
        EngineSimulationProfile::Standard,
        3856990760,
        120,
        0x2447e5f68a57a32306e812f8b1002767,
    ),
    (
        EngineSimulationProfile::Standard,
        3856990458,
        120,
        0x09a51e2ebd859ebd04fbbe2c52a57ccd,
    ),
    (
        EngineSimulationProfile::Standard,
        3856990875,
        120,
        0x17817d96277f49b2f806f3d1f48b1e50,
    ),
    (
        EngineSimulationProfile::Standard,
        FIRST_SEED,
        120,
        0x1bbd5d8aabe7137fe43ad2fa5a6dc00f,
    ),
    (
        EngineSimulationProfile::Standard,
        FIRST_SEED + 1,
        120,
        0xd04d9e0a9137446c7aa714b9b90788e4,
    ),
    (
        EngineSimulationProfile::Standard,
        FIRST_SEED + 2,
        120,
        0x36bca163101426c0159ea3570b66f6a4,
    ),
    (
        EngineSimulationProfile::RedeploysWithShortHistoryWindow,
        FIRST_REDEPLOY_SEED,
        120,
        0x754b2a7167ed08e5049265d8d143ebfc,
    ),
];

fn environment_number(name: &str) -> Option<u64> {
    let text = std::env::var(name).ok()?;
    Some(
        text.parse()
            .unwrap_or_else(|error| panic!("{name}={text:?} is not a number: {error}")),
    )
}

/// Returns the active seconds of each seed to run, and whether one seed was requested.
fn configured(
    default_seed_count: u64,
    default_first_seed: u64,
    regression_cases: &[(u64, u64)],
) -> (BTreeMap<u64, u64>, bool) {
    let active_seconds =
        environment_number("DEXZD_SIMULATION_SECONDS").unwrap_or(DEFAULT_ACTIVE_SECONDS);
    match environment_number("DEXZD_SIMULATION_SEED") {
        Some(seed) => (BTreeMap::from([(seed, active_seconds)]), true),
        None => {
            let count = environment_number("DEXZD_SIMULATION_SEEDS").unwrap_or(default_seed_count);
            let first_seed =
                environment_number("DEXZD_SIMULATION_BASE_SEED").unwrap_or(default_first_seed);
            let end_seed = first_seed
                .checked_add(count)
                .expect("simulation seed range overflow");
            let mut seeds: BTreeMap<u64, u64> = (first_seed..end_seed)
                .map(|seed| (seed, active_seconds))
                .collect();
            // A longer active phase replays the same trajectory up to the regression's failure.
            for &(seed, regression_seconds) in regression_cases {
                seeds
                    .entry(seed)
                    .and_modify(|seconds| *seconds = (*seconds).max(regression_seconds))
                    .or_insert(regression_seconds);
            }
            (seeds, false)
        }
    }
}

fn regression_seed_cases() -> Vec<(u64, u64)> {
    REGRESSION_SEEDS
        .lines()
        .filter(|line| !line.trim().is_empty())
        .map(|line| {
            let mut fields = line.split_whitespace();
            let seed = fields
                .next()
                .expect("regression seed is missing")
                .parse()
                .expect("regression seed is not a number");
            let active_seconds = fields
                .next()
                .expect("regression active seconds are missing")
                .parse()
                .expect("regression active seconds are not a number");
            assert!(fields.next().is_none(), "regression seed has extra fields");
            (seed, active_seconds)
        })
        .collect()
}

#[test]
fn the_engine_keeps_every_invariant_through_faults_and_crashes() {
    let (seeds, is_single_seed) =
        configured(DEFAULT_SEED_COUNT, FIRST_SEED, &regression_seed_cases());
    let mut total = run_seeds(&seeds, EngineSimulationProfile::Standard);
    if std::env::var_os("DEXZD_SIMULATION_PRINT_EVENTS").is_some() {
        for event in &total.recent_events {
            println!("{event}");
        }
    }
    total.recent_events.clear();
    println!("engine simulation coverage: {total:#?}");
    println!(
        "twin comparisons: {} compared, {} skipped while hard-delete uploads were pending",
        total.state_comparisons, total.state_comparisons_skipped_for_upload_ledger
    );
    if is_single_seed {
        return;
    }
    assert_covered(&total);
}

#[test]
fn redeploys_with_a_short_history_window_keep_every_invariant() {
    let (seeds, is_single_seed) = configured(DEFAULT_REDEPLOY_SEED_COUNT, FIRST_REDEPLOY_SEED, &[]);
    let mut total = run_seeds(
        &seeds,
        EngineSimulationProfile::RedeploysWithShortHistoryWindow,
    );
    total.recent_events.clear();
    println!("redeploy profile coverage: {total:#?}");
    if is_single_seed {
        return;
    }
    let checks = [
        ("incarnation changes", total.incarnation_changes),
        (
            "closed-stream replays over voided batches",
            total.closed_stream_replays_over_voided_batches,
        ),
        (
            "pruned semantic history events",
            total.final_pruned_semantic_events,
        ),
        (
            "recovered-store checks during an old-epoch overlap",
            total.recovered_checks_during_old_epoch_overlap,
        ),
        ("old-epoch store checks", total.old_epoch_store_checks),
        (
            "recovered-store checks with older window points",
            total.recovered_checks_with_older_window_points,
        ),
    ];
    for (name, value) in checks {
        assert!(value > 0, "the redeploy profile had no {name}: {total:#?}");
    }
}

#[test]
fn step_failure_policies_keep_every_invariant() {
    let (seeds, is_single_seed) = configured(
        DEFAULT_STEP_FAILURE_SEED_COUNT,
        FIRST_STEP_FAILURE_SEED,
        &[],
    );
    let mut total = run_seeds(&seeds, EngineSimulationProfile::StepFailurePolicies);
    total.recent_events.clear();
    println!("Step failure profile coverage: {total:#?}");
    if is_single_seed {
        return;
    }
    assert!(
        total
            .close_hold_dry_runs
            .get("TimerSkip")
            .is_some_and(|count| *count > 0),
        "the Step failure profile never dry-ran a Flow timeout skip for an admitted RPC: {total:#?}"
    );
    for outcome in [
        "wait-for-proceed",
        "execute-recovery",
        "timeout-handler-recovery",
        "retry-deadline",
    ] {
        assert!(
            total
                .step_failure_recoveries
                .get(outcome)
                .is_some_and(|count| *count > 0),
            "the Step failure profile had no {outcome}: {total:#?}"
        );
    }
}

#[test]
fn pinned_seeds_keep_their_canonical_command_trajectories() {
    for (seed, active_seconds) in regression_seed_cases() {
        let is_pinned = PINNED_CANONICAL_COMMAND_DIGESTS.iter().any(
            |&(profile, pinned_seed, pinned_seconds, _)| {
                (profile, pinned_seed, pinned_seconds)
                    == (EngineSimulationProfile::Standard, seed, active_seconds)
            },
        );
        assert!(
            is_pinned,
            "pin the canonical command digest of regression seed {seed} {active_seconds}"
        );
    }
    let mut changed_trajectories = Vec::new();
    for &(profile, seed, active_seconds, expected) in PINNED_CANONICAL_COMMAND_DIGESTS {
        let config = EngineSimulationConfig {
            active_seconds,
            profile,
            ..EngineSimulationConfig::default()
        };
        let summary = run_engine_seed(seed, &config).unwrap_or_else(|failure| panic!("{failure}"));
        if summary.canonical_command_digest != expected {
            changed_trajectories.push(format!(
                "{profile:?} seed {seed} {active_seconds}: expected {expected:#034x}, \
                 observed {:#034x}",
                summary.canonical_command_digest
            ));
        }
    }
    assert!(
        changed_trajectories.is_empty(),
        "canonical command trajectories changed:\n{}",
        changed_trajectories.join("\n")
    );
}

/// Runs each seed for its active seconds; a failing seed writes its report and panics.
fn run_seeds(
    seeds: &BTreeMap<u64, u64>,
    profile: EngineSimulationProfile,
) -> EngineSimulationSummary {
    let failure_directory = std::env::var_os("DEXZD_SIMULATION_FAILURE_DIR").map(PathBuf::from);
    let mut total = EngineSimulationSummary::default();
    for (&seed, &active_seconds) in seeds {
        let config = EngineSimulationConfig {
            active_seconds,
            profile,
            fault_probability_percent: environment_number("DEXZD_SIMULATION_FAULT_PERCENT").map(
                |percent| {
                    assert!(
                        percent <= 100,
                        "DEXZD_SIMULATION_FAULT_PERCENT must be 0..=100"
                    );
                    u8::try_from(percent).expect("validated fault percentage fits in u8")
                },
            ),
            ..EngineSimulationConfig::default()
        };
        match run_engine_seed(seed, &config) {
            Ok(summary) => total.merge(&summary),
            Err(failure) => {
                write_failure_report(failure_directory.as_ref(), &failure);
                panic!("{failure}");
            }
        }
    }
    total
}

/// Publishes `<seed>.txt` by renaming, so a collector never reads a partial report.
fn write_failure_report(directory: Option<&PathBuf>, failure: &EngineSimulationFailure) {
    let Some(directory) = directory else {
        return;
    };
    std::fs::create_dir_all(directory).expect("create engine simulation failure directory");
    let pending_report = directory.join(format!("{}.tmp", failure.seed));
    std::fs::write(&pending_report, failure.to_string())
        .expect("write engine simulation failure report");
    std::fs::rename(
        pending_report,
        directory.join(format!("{}.txt", failure.seed)),
    )
    .expect("publish engine simulation failure report");
}

/// The fixed seed set must exercise every fault, crash, and Worker behavior the harness models.
fn assert_covered(total: &EngineSimulationSummary) {
    let count = |counts: &std::collections::BTreeMap<String, u64>, name: &str| {
        counts.get(name).copied().unwrap_or(0)
    };
    for restart in ["kept", "lost", "rolled-back"] {
        assert!(
            count(&total.restarts_by_local_store, restart) > 0,
            "no restart with a {restart} local store: {total:#?}"
        );
    }
    for fault in [
        "SlowResponse/Delayed",
        "ServerError/WrittenOutcomeUnknown",
        "ServerError/NotWrittenOutcomeUnknown",
        "ServerError/FailedBeforeEffect",
        "Throttled/FailedBeforeEffect",
        "ConditionalRequestConflict/FailedBeforeEffect",
        "Timeout/WrittenOutcomeUnknown",
        "Timeout/NotWrittenOutcomeUnknown",
        "ResponseLost/WrittenOutcomeUnknown",
        "ResponseLost/ReadDiscarded",
    ] {
        assert!(
            count(&total.injected_faults, fault) > 0,
            "no injected {fault}: {total:#?}"
        );
    }
    for behavior in [
        "Succeed",
        "Fail",
        "Hang",
        "Slow",
        "RepeatResult",
        "DisconnectBeforeOutput",
        "DisconnectAfterOutput",
        "NoWorker",
        "Hold",
        "HeartbeatWithValue",
        "HeartbeatWithoutValue",
        "RpcSucceed",
        "RpcFail",
        "RpcTimeout",
    ] {
        assert!(
            count(&total.worker_behaviors, behavior) > 0,
            "no Worker behaved as {behavior}: {total:#?}"
        );
    }
    for kind in [
        "FlowStart",
        "FlowStop",
        "AttributesSet",
        "ChannelPublish",
        "ChannelMessageDelete",
        "RpcCommitted",
        "StepAttemptStarted",
        "StepAttemptHeartbeat",
        "StepAttemptFailed",
        "StepMethodCompleted",
        "TimersFired",
        "RunPurge",
    ] {
        assert!(
            count(&total.applied_by_kind, kind) > 0,
            "the seed set never committed a {kind} command: {total:#?}"
        );
    }
    for (counts, name) in [
        (&total.precondition_failed_puts, "log"),
        (&total.precondition_failed_puts, "lease"),
        (&total.deleted_objects, "log"),
        (&total.snapshot_reads_during_start, "lost"),
        (&total.snapshot_reads_during_start, "takeover"),
        (&total.old_epoch_write_attempts, "log"),
        (&total.old_epoch_write_attempts, "lease"),
        (&total.writes_landed_after_crash, "log"),
        (&total.crashes_with_outstanding_work, "timers"),
        (&total.crashes_with_outstanding_work, "running-attempts"),
        (&total.crashes_with_outstanding_work, "outbox"),
        (&total.crashes_with_outstanding_work, "pending-hard-deletes"),
        (&total.interrupted_work_finished, "outbox"),
        (&total.interrupted_work_finished, "pending-hard-deletes"),
        (&total.self_stop_reasons, "older-epoch-batch"),
    ] {
        assert!(
            count(counts, name) > 0,
            "missing {name} in a coverage count: {total:#?}"
        );
    }
    let rejected_results = count(&total.rejected_by_kind, "StepMethodCompleted")
        + count(&total.rejected_by_kind, "StepAttemptFailed");
    let checks = [
        ("rejected duplicate results", rejected_results),
        ("old-epoch frames", total.old_epoch_frames),
        (
            "attempts compared after the quiet crash",
            total.attempts_compared_after_quiet_crash,
        ),
        ("runs in the final state", total.final_runs as u64),
        ("old-epoch overlaps", total.old_epoch_overlaps),
        ("crashes during start", total.crashes_during_start),
        ("self-stopped processes", total.self_stopped_processes),
        (
            "EpochStarts below landed batches",
            total.epoch_starts_below_landed_batches,
        ),
        ("clock jumps", total.clock_jumps),
        ("acknowledged writes", total.acknowledged_writes),
        ("checked reads", total.checked_reads),
        ("checked read values", total.checked_read_values),
        ("replayed frames", total.replayed_frames),
        ("state comparisons", total.state_comparisons),
        (
            "state comparisons after recovery",
            total.recovered_state_comparisons,
        ),
        ("dispatch comparisons", total.dispatch_comparisons),
        (
            "recovered-store checks during an old-epoch overlap",
            total.recovered_checks_during_old_epoch_overlap,
        ),
        ("old-epoch store checks", total.old_epoch_store_checks),
        (
            "child results dry-run for an admitted RPC of their parent",
            count(&total.close_hold_dry_runs, "InboxMessage"),
        ),
    ];
    for (name, value) in checks {
        assert!(value > 0, "the seed set had no {name}: {total:#?}");
    }
}

#[test]
fn one_seed_produces_the_same_trace_and_final_state_twice() {
    for (seed, profile) in [
        (FIRST_SEED, EngineSimulationProfile::Standard),
        (FIRST_SEED + 1, EngineSimulationProfile::Standard),
        (FIRST_SEED + 2, EngineSimulationProfile::Standard),
        (
            FIRST_REDEPLOY_SEED,
            EngineSimulationProfile::RedeploysWithShortHistoryWindow,
        ),
        (
            FIRST_STEP_FAILURE_SEED,
            EngineSimulationProfile::StepFailurePolicies,
        ),
    ] {
        let config = EngineSimulationConfig {
            active_seconds: 60,
            profile,
            ..EngineSimulationConfig::default()
        };
        let run = || {
            run_engine_seed_with_final_state(seed, &config)
                .unwrap_or_else(|failure| panic!("{failure}"))
        };
        let (first_summary, first_state) = run();
        let (second_summary, second_state) = run();
        if let Some(difference) = first_state.first_difference(&second_state) {
            panic!("two runs of seed {seed} differ: {difference}");
        }
        assert_eq!(first_summary.trace_hash, second_summary.trace_hash);
        assert!(
            first_state.trace_events.len() > 300,
            "seed {seed} produced only {} events",
            first_state.trace_events.len()
        );
        assert!(
            first_state.local_store.is_some() && !first_state.objects.is_empty(),
            "seed {seed} left no final state to compare"
        );
    }
}
