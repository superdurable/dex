// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! A manual soak of an active and a hot standby `serve` process on one object storage prefix,
//! under a steady Flow load and a seeded schedule of faults; see `crates/dexzd-server/README.md`.

mod cluster;
mod layout;
mod metrics_tally;
mod target;
mod vault;

use std::collections::BTreeMap;
use std::fmt;
use std::panic::resume_unwind;
use std::path::PathBuf;
use std::sync::Arc;
use std::time::Duration;

use tempfile::TempDir;
use tokio::time::Instant;

use crate::robustness::processes::replay_field;
use crate::robustness::workload::{LoadShape, Workload};
use cluster::SoakCluster;
use layout::check_versioned_layout;
use target::{SoakTarget, count_by_area, list_all};
use vault::Vault;

const DURATION_VARIABLE: &str = "DEX_SOAK_DURATION";
const FAULT_INTERVAL_VARIABLE: &str = "DEX_SOAK_FAULT_INTERVAL";
const SEED_VARIABLE: &str = "DEX_SOAK_SEED";
const LOG_DIRECTORY_VARIABLE: &str = "DEX_SOAK_LOG_DIR";
const KEEP_OBJECTS_VARIABLE: &str = "DEX_SOAK_KEEP_OBJECTS";
const DEFAULT_DURATION: Duration = Duration::from_secs(2 * 60 * 60);
/// Faults come at most this far apart by default, and a sixth of a short soak apart.
const LONGEST_DEFAULT_FAULT_INTERVAL: Duration = Duration::from_secs(4 * 60);
const SHORTEST_FAULT_INTERVAL: Duration = Duration::from_secs(30);
const PROGRESS_INTERVAL: Duration = Duration::from_secs(30);
const VAULT_ROUND_INTERVAL: Duration = Duration::from_secs(10);
const SCRAPE_INTERVAL: Duration = Duration::from_secs(10);
const LOOP_PAUSE: Duration = Duration::from_millis(200);
const FIRST: usize = 0;
const SECOND: usize = 1;
const SLOT_NAMES: [&str; 2] = ["a", "b"];
const COUNTER_COUNT: usize = 2;
/// A few Flows and increments per second: each starter waits for its Flow to close, then pauses.
const LOAD_SHAPE: LoadShape = LoadShape {
    chain_starters: 3,
    timer_starters: 1,
    call_timeout: Duration::from_secs(5),
    increment_resend_budget: Some(Duration::from_secs(120)),
    pause_between_calls: Duration::from_millis(500),
};
const STARTUP_BUDGET: Duration = Duration::from_secs(120);
const TAKEOVER_BUDGET: Duration = Duration::from_secs(90);
/// Covers a snapshot restore and log catch-up from a real bucket.
const STANDBY_BUDGET: Duration = Duration::from_secs(180);
const READ_BACK_BUDGET: Duration = Duration::from_secs(90);
const HARD_DELETE_DRAIN_BUDGET: Duration = Duration::from_secs(180);
const HARD_DELETE_PREFIX: &str = "hard-delete/";
const SNAPSHOT_PREFIX: &str = "snapshot/";
/// A COS listing can repeat an earlier one for about three seconds.
const LISTING_SETTLE: Duration = Duration::from_secs(5);
const VERIFICATION_BASE_BUDGET: Duration = Duration::from_secs(120);
const VERIFICATION_BUDGET_PER_FLOW: Duration = Duration::from_millis(20);

/// One scheduled fault.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord)]
enum SoakFault {
    /// SIGKILL the active; the hot standby takes over, and the killed slot restarts as standby.
    KillActive,
    /// SIGTERM the active, which releases its lease; it restarts as standby.
    RestartActive,
    /// SIGTERM both, wipe both local directories, and restore from object storage.
    ColdRestartBoth,
    /// Stop the oldest vault Flows and erase their hard-delete values.
    HardDeleteClosedRuns,
}

const FAULT_KINDS: [SoakFault; 4] = [
    SoakFault::KillActive,
    SoakFault::RestartActive,
    SoakFault::ColdRestartBoth,
    SoakFault::HardDeleteClosedRuns,
];

/// Faults in shuffled rounds of every kind, from a seeded SplitMix64 generator.
struct FaultSchedule {
    state: u64,
    pending: Vec<SoakFault>,
}

impl FaultSchedule {
    fn new(seed: u64) -> Self {
        Self {
            state: seed,
            pending: Vec::new(),
        }
    }

    fn next_fault(&mut self) -> SoakFault {
        if self.pending.is_empty() {
            let mut round = FAULT_KINDS.to_vec();
            for index in (1..round.len()).rev() {
                let pick = usize::try_from(self.next_random() % (index as u64 + 1))
                    .expect("a shuffle index fits in usize");
                round.swap(index, pick);
            }
            self.pending = round;
        }
        self.pending.pop().expect("a refilled round is not empty")
    }

    fn next_random(&mut self) -> u64 {
        self.state = self.state.wrapping_add(0x9e37_79b9_7f4a_7c15);
        let mut mixed = self.state;
        mixed = (mixed ^ (mixed >> 30)).wrapping_mul(0xbf58_476d_1ce4_e5b9);
        mixed = (mixed ^ (mixed >> 27)).wrapping_mul(0x94d0_49bb_1331_11eb);
        mixed ^ (mixed >> 31)
    }
}

/// The soak's settings from the environment.
struct SoakSettings {
    duration: Duration,
    fault_interval: Duration,
    seed: u64,
    log_directory: PathBuf,
    keeps_objects: bool,
}

impl SoakSettings {
    fn from_environment(run_id: &str, default_seed: u64) -> Self {
        let duration = std::env::var(DURATION_VARIABLE)
            .ok()
            .filter(|text| !text.is_empty())
            .map_or(DEFAULT_DURATION, |text| {
                parse_duration(&text).unwrap_or_else(|| {
                    panic!(
                        "{DURATION_VARIABLE} must look like 2h, 30m, 90s, or 1h30m, not {text:?}"
                    )
                })
            });
        let fault_interval = std::env::var(FAULT_INTERVAL_VARIABLE)
            .ok()
            .filter(|text| !text.is_empty())
            .map_or_else(
                || (duration / 6).clamp(SHORTEST_FAULT_INTERVAL, LONGEST_DEFAULT_FAULT_INTERVAL),
                |text| {
                    parse_duration(&text).unwrap_or_else(|| {
                        panic!("{FAULT_INTERVAL_VARIABLE} must be a duration, not {text:?}")
                    })
                },
            );
        let seed = std::env::var(SEED_VARIABLE)
            .ok()
            .filter(|text| !text.is_empty())
            .map_or(default_seed, |text| {
                text.parse()
                    .unwrap_or_else(|_| panic!("{SEED_VARIABLE} must be a u64, not {text:?}"))
            });
        let log_directory = std::env::var(LOG_DIRECTORY_VARIABLE)
            .ok()
            .filter(|text| !text.is_empty())
            .map_or_else(
                || std::env::temp_dir().join(format!("dexzd-soak-{run_id}")),
                PathBuf::from,
            );
        std::fs::create_dir_all(&log_directory).expect("the soak log directory is created");
        Self {
            duration,
            fault_interval,
            seed,
            log_directory,
            keeps_objects: std::env::var(KEEP_OBJECTS_VARIABLE).is_ok_and(|text| text == "1"),
        }
    }
}

impl fmt::Display for SoakSettings {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(
            formatter,
            "duration {:?}, a fault every {:?}, seed {} ({SEED_VARIABLE}), process logs in {}",
            self.duration,
            self.fault_interval,
            self.seed,
            self.log_directory.display()
        )
    }
}

/// How long one fault took the cluster to serve again.
struct Recovery {
    fault: SoakFault,
    from: String,
    to: String,
    serving_again_after: Duration,
}

/// Everything the soak reports at the end.
#[derive(Default)]
struct SoakLedger {
    faults: BTreeMap<SoakFault, usize>,
    recoveries: Vec<Recovery>,
    snapshot_restores: Vec<u64>,
    hard_delete_closures: usize,
}

/// Soaks a cluster on the target that `DEX_SOAK_TARGET` names; see `crates/dexzd-server/README.md`.
#[tokio::test(flavor = "multi_thread", worker_threads = 8)]
#[ignore = "manual soak on real object storage or s3local; see crates/dexzd-server/README.md"]
async fn object_store_soak() {
    let nanos = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .expect("the system clock is after the Unix epoch")
        .as_nanos();
    let run_id = nanos.to_string();
    let settings = SoakSettings::from_environment(&run_id, nanos as u64);
    let target = Arc::new(SoakTarget::from_environment(&run_id).await);
    eprintln!("soak: {}; {settings}", target.describe());
    let keeps_objects = settings.keeps_objects;
    let outcome = tokio::spawn(run_soak(Arc::clone(&target), settings, run_id)).await;
    let remaining = if keeps_objects {
        eprintln!("soak: {KEEP_OBJECTS_VARIABLE}=1, so the objects stay under the prefix");
        Vec::new()
    } else {
        target.delete_everything().await
    };
    eprintln!(
        "soak: {} objects remain under the prefix after cleanup{}",
        remaining.len(),
        if remaining.is_empty() {
            String::new()
        } else {
            format!(": {:?}", &remaining[..remaining.len().min(20)])
        }
    );
    if let Ok(target) = Arc::try_unwrap(target) {
        target.shutdown().await;
    }
    if let Err(error) = outcome {
        resume_unwind(error.into_panic());
    }
}

async fn run_soak(target: Arc<SoakTarget>, settings: SoakSettings, run_id: String) {
    let directory = TempDir::new().expect("a temporary soak directory can be created");
    let workload = Workload::new();
    let mut cluster = SoakCluster::start(
        directory.path(),
        &settings.log_directory,
        &target,
        &workload,
        &SLOT_NAMES,
    )
    .await;
    let inspection = target.physical_store().await;
    let started_at = Instant::now();
    cluster.spawn(FIRST);
    cluster.wait_for_serving(None, STARTUP_BUDGET).await;
    cluster.spawn(SECOND);
    cluster.wait_for_hot_standby(SECOND, STANDBY_BUDGET).await;
    eprintln!(
        "soak +{}: {} serves and {} is a hot standby",
        elapsed(started_at),
        cluster.slot_name(FIRST),
        cluster.slot_name(SECOND)
    );
    workload
        .start_counters(cluster.router_channel(), COUNTER_COUNT)
        .await;
    let worker = workload.start_reconnecting_worker(cluster.router_address());
    let load = workload.start_shaped_load(cluster.router_address(), "soak", &LOAD_SHAPE);
    let mut vault = Vault::new(&run_id);
    let mut schedule = FaultSchedule::new(settings.seed);
    let mut ledger = SoakLedger::default();
    let mut next_fault_at = started_at + settings.fault_interval;
    let mut next_progress_at = started_at + PROGRESS_INTERVAL;
    let mut next_vault_round_at = started_at;
    let mut next_scrape_at = started_at;
    let soak_ends_at = started_at + settings.duration;
    while Instant::now() < soak_ends_at {
        cluster.supervise();
        let now = Instant::now();
        if now >= next_vault_round_at {
            if let Err(failure) = vault.run_round(&cluster.router_channel()).await {
                panic!(
                    "soak +{}: a vault write failed: {failure}",
                    elapsed(started_at)
                );
            }
            next_vault_round_at = now + VAULT_ROUND_INTERVAL;
        }
        if now >= next_scrape_at {
            cluster.scrape_all().await;
            next_scrape_at = now + SCRAPE_INTERVAL;
        }
        if now >= next_fault_at {
            let fault = schedule.next_fault();
            *ledger.faults.entry(fault).or_default() += 1;
            let number: usize = ledger.faults.values().sum();
            inject_fault(
                fault,
                number,
                &mut cluster,
                &mut vault,
                &mut ledger,
                &inspection,
                started_at,
            )
            .await;
            check_read_back(
                &cluster,
                &workload,
                &mut vault,
                started_at,
                &format!("fault #{number} {fault:?}"),
            )
            .await;
            next_fault_at = Instant::now() + settings.fault_interval;
        }
        if now >= next_progress_at {
            eprintln!(
                "soak +{}: {} serves; {} acknowledged Flow starts and {} acknowledged calls; vault: {}; faults {:?}; {}",
                elapsed(started_at),
                cluster
                    .serving_slot()
                    .map_or("nothing", |slot| cluster.slot_name(slot)),
                workload.acknowledged_start_count(),
                workload.acknowledged_count(),
                vault.report(),
                ledger.faults,
                cluster.tally_so_far()
            );
            next_progress_at = now + PROGRESS_INTERVAL;
        }
        tokio::time::sleep(LOOP_PAUSE).await;
    }

    eprintln!(
        "soak +{}: the soak time is over; verifying",
        elapsed(started_at)
    );
    load.stop().await;
    let channel = cluster.router_channel();
    if let Err(failure) = vault.close_and_erase_all(&channel).await {
        panic!("closing the vault Flows failed: {failure}");
    }
    check_read_back(
        &cluster,
        &workload,
        &mut vault,
        started_at,
        "the final read",
    )
    .await;
    let verification_budget = VERIFICATION_BASE_BUDGET
        + VERIFICATION_BUDGET_PER_FLOW
            * u32::try_from(workload.acknowledged_start_count()).unwrap_or(u32::MAX);
    let report = cluster
        .verify_workload(&workload, TAKEOVER_BUDGET, verification_budget)
        .await;
    let hard_delete_objects = wait_for_hard_delete_drain(&inspection).await;
    assert!(
        hard_delete_objects.is_empty(),
        "every hard-delete value was erased, but {} objects stay under {HARD_DELETE_PREFIX} after {HARD_DELETE_DRAIN_BUDGET:?}: {:?}",
        hard_delete_objects.len(),
        &hard_delete_objects[..hard_delete_objects.len().min(10)]
    );
    let registrations = worker.stop().await;
    let cluster_report = cluster.finish().await;
    tokio::time::sleep(LISTING_SETTLE).await;
    let objects = list_all(&inspection, "")
        .await
        .expect("the soak prefix lists");
    let layout = if target.provider.is_head_probing() {
        Some(check_versioned_layout(&inspection, &objects).await)
    } else {
        None
    };

    eprintln!("soak summary after {}:", elapsed(started_at));
    eprintln!("  target: {}", target.describe());
    eprintln!("  seed {}; faults {:?}", settings.seed, ledger.faults);
    eprintln!("  workload: {report}");
    eprintln!(
        "  {} acknowledged Flow starts, {} acknowledged calls; {registrations}",
        workload.acknowledged_start_count(),
        workload.acknowledged_count()
    );
    eprintln!("  vault: {}", vault.report());
    eprintln!(
        "  {} hard-delete closures; snapshot restores at seqs {:?}",
        ledger.hard_delete_closures, ledger.snapshot_restores
    );
    for recovery in &ledger.recoveries {
        eprintln!(
            "  {:?}: {} served again after {:?}, from {}",
            recovery.fault, recovery.to, recovery.serving_again_after, recovery.from
        );
    }
    eprintln!(
        "  {} process starts, {} stale-batch restarts; readiness {:?}",
        cluster_report.process_starts, cluster_report.stale_batch_restarts, cluster_report.serving
    );
    eprintln!(
        "  {} failed starts that a restart recovered{}",
        cluster_report.failed_starts.len(),
        cluster_report
            .failed_starts
            .iter()
            .map(|failure| format!("\n    FAILED START {failure}"))
            .collect::<String>()
    );
    eprintln!("  metrics: {}", cluster_report.tally);
    eprintln!("  objects by area at the end:");
    for (area, (count, bytes)) in count_by_area(&objects) {
        eprintln!("    {area}: {count} objects, {bytes} bytes");
    }
    if let Some(layout) = &layout {
        eprintln!("  versioned layout: {layout}");
        assert!(
            layout.violations.is_empty(),
            "the versioned keys break the HEAD-probing layout:\n{}",
            layout.violations.join("\n")
        );
    }
}

/// Injects `fault`, waits until the cluster serves with a hot standby again, and records it.
async fn inject_fault(
    fault: SoakFault,
    number: usize,
    cluster: &mut SoakCluster,
    vault: &mut Vault,
    ledger: &mut SoakLedger,
    inspection: &dexzd_object_store::S3ObjectStore,
    started_at: Instant,
) {
    let active = cluster.wait_for_serving(None, TAKEOVER_BUDGET).await;
    let standby = (0..cluster.slot_count())
        .find(|slot| *slot != active)
        .expect("the cluster has two slots");
    let from = cluster.slot_name(active).to_owned();
    eprintln!(
        "soak +{}: fault #{number} {fault:?} with {from} active",
        elapsed(started_at)
    );
    match fault {
        SoakFault::KillActive | SoakFault::RestartActive => {
            let stopped_at = Instant::now();
            if fault == SoakFault::KillActive {
                cluster.kill(active).await;
            } else {
                cluster.terminate(active).await;
            }
            let serving = cluster
                .wait_for_serving(Some(active), TAKEOVER_BUDGET)
                .await;
            ledger.recoveries.push(Recovery {
                fault,
                from,
                to: cluster.slot_name(serving).to_owned(),
                serving_again_after: stopped_at.elapsed(),
            });
            cluster.spawn(active);
            cluster.wait_for_hot_standby(active, STANDBY_BUDGET).await;
        }
        SoakFault::ColdRestartBoth => {
            let snapshot_manifests =
                list_all(inspection, SNAPSHOT_PREFIX)
                    .await
                    .map_or(0, |objects| {
                        objects
                            .iter()
                            .filter(|object| object.key.contains("/manifest/"))
                            .count()
                    });
            cluster.terminate(standby).await;
            let stopped_at = Instant::now();
            cluster.terminate(active).await;
            cluster.wipe(active);
            cluster.wipe(standby);
            cluster.spawn(active);
            let serving = cluster.wait_for_serving(None, STARTUP_BUDGET).await;
            let restored_seq = replay_field(&cluster.process_log(serving), "last_applied_seq");
            assert!(
                snapshot_manifests == 0 || restored_seq > 0,
                "fault #{number}: {snapshot_manifests} snapshot manifests existed, but the cold restart replayed the log from seq 1:\n{}",
                cluster.process_log(serving)
            );
            ledger.snapshot_restores.push(restored_seq);
            ledger.recoveries.push(Recovery {
                fault,
                from,
                to: cluster.slot_name(serving).to_owned(),
                serving_again_after: stopped_at.elapsed(),
            });
            cluster.spawn(standby);
            cluster.wait_for_hot_standby(standby, STANDBY_BUDGET).await;
        }
        SoakFault::HardDeleteClosedRuns => {
            let closed = vault
                .close_and_erase_oldest(&cluster.router_channel())
                .await
                .unwrap_or_else(|failure| panic!("fault #{number}: {failure}"));
            ledger.hard_delete_closures += closed;
        }
    }
    eprintln!(
        "soak +{}: fault #{number} done; {} serves",
        elapsed(started_at),
        cluster
            .serving_slot()
            .map_or("nothing", |slot| cluster.slot_name(slot))
    );
}

/// Panics unless every counter and vault Flow reads back its acknowledged writes.
async fn check_read_back(
    cluster: &SoakCluster,
    workload: &Workload,
    vault: &mut Vault,
    started_at: Instant,
    after: &str,
) {
    let channel = cluster.router_channel();
    let behind = workload
        .counters_behind_acknowledged(channel.clone(), READ_BACK_BUDGET)
        .await
        .unwrap_or_else(|failure| panic!("soak +{} after {after}: {failure}", elapsed(started_at)));
    let vault_violations = vault
        .check_all(&channel)
        .await
        .unwrap_or_else(|failure| panic!("soak +{} after {after}: {failure}", elapsed(started_at)));
    let violations: Vec<String> = behind.into_iter().chain(vault_violations).collect();
    assert!(
        violations.is_empty(),
        "soak +{} after {after}, reads lost acknowledged writes:\n{}",
        elapsed(started_at),
        violations.join("\n")
    );
}

/// Waits until no object stays under the hard-delete prefix; returns those that still do.
async fn wait_for_hard_delete_drain(inspection: &dexzd_object_store::S3ObjectStore) -> Vec<String> {
    let deadline = Instant::now() + HARD_DELETE_DRAIN_BUDGET;
    loop {
        let keys: Vec<String> = list_all(inspection, HARD_DELETE_PREFIX)
            .await
            .map(|objects| objects.into_iter().map(|object| object.key).collect())
            .unwrap_or_else(|error| vec![format!("<listing failed: {error}>")]);
        if keys.is_empty() || Instant::now() >= deadline {
            return keys;
        }
        tokio::time::sleep(Duration::from_secs(2)).await;
    }
}

/// Parses durations such as `2h`, `30m`, `90s`, and `1h30m`.
fn parse_duration(text: &str) -> Option<Duration> {
    let mut total = Duration::ZERO;
    let mut digits = String::new();
    for character in text.trim().chars() {
        if character.is_ascii_digit() {
            digits.push(character);
            continue;
        }
        let amount: u64 = digits.parse().ok()?;
        digits.clear();
        let unit = match character {
            'h' => 60 * 60,
            'm' => 60,
            's' => 1,
            _ => return None,
        };
        total += Duration::from_secs(amount.checked_mul(unit)?);
    }
    (digits.is_empty() && total > Duration::ZERO).then_some(total)
}

fn elapsed(started_at: Instant) -> String {
    let seconds = started_at.elapsed().as_secs();
    format!("{}m{:02}s", seconds / 60, seconds % 60)
}
