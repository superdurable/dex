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
mod log_tally;
mod metrics_tally;
mod target;
mod vault;

use std::collections::BTreeMap;
use std::fmt;
use std::panic::resume_unwind;
use std::path::PathBuf;
use std::sync::Arc;
use std::time::Duration;

use dexzd_object_store::{MANIFEST_OBJECT_KEY, node_lease_object_key};
use tempfile::TempDir;
use tokio::time::Instant;

use crate::robustness::workload::{LoadShape, Workload};
use cluster::{KILLED_LEASE_EXPIRY_WAIT, NODE_ID, SoakCluster};
use layout::check_versioned_layout;
use log_tally::LogTally;
use target::{SoakTarget, count_by_area, list_all};
use vault::Vault;

const DURATION_VARIABLE: &str = "DEX_SOAK_DURATION";
const FAULT_INTERVAL_VARIABLE: &str = "DEX_SOAK_FAULT_INTERVAL";
const SEED_VARIABLE: &str = "DEX_SOAK_SEED";
const LOG_DIRECTORY_VARIABLE: &str = "DEX_SOAK_LOG_DIR";
const KEEP_OBJECTS_VARIABLE: &str = "DEX_SOAK_KEEP_OBJECTS";
const SNAPSHOT_INTERVAL_VARIABLE: &str = "DEX_SOAK_SNAPSHOT_INTERVAL";
const MAX_STANDBY_RESTARTS_VARIABLE: &str = "DEX_SOAK_MAX_STANDBY_RESTARTS";
const MAX_TAKEOVER_RETRIES_VARIABLE: &str = "DEX_SOAK_MAX_TAKEOVER_RETRIES";
const LOG_STALL_INTERVAL_VARIABLE: &str = "DEX_SOAK_LOG_STALL_INTERVAL";
/// Outlasts the 10 s log batch write deadline, so the serving process stops.
const LOG_STALL_HOLD: Duration = Duration::from_secs(15);
/// How long a stall waits for the serving process to exit before it releases the writes.
const LOG_STALL_BUDGET: Duration = Duration::from_secs(30);
const TOP_LOG_PATTERNS: usize = 15;
/// Short enough that every cold restart restores a recent snapshot.
const DEFAULT_SNAPSHOT_INTERVAL: Duration = Duration::from_secs(60);
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
    increment_resend_budget: Some(Duration::from_secs(10 * 60)),
    pause_between_calls: Duration::from_millis(500),
};
/// Covers a snapshot restore and a replay of a long log backlog from a real bucket.
const STARTUP_BUDGET: Duration = Duration::from_secs(5 * 60);
const TAKEOVER_BUDGET: Duration = Duration::from_secs(90);
/// Covers a snapshot restore and log catch-up from a real bucket.
const STANDBY_BUDGET: Duration = Duration::from_secs(5 * 60);
const READ_BACK_BUDGET: Duration = Duration::from_secs(90);
const HARD_DELETE_DRAIN_BUDGET: Duration = Duration::from_secs(180);
const HARD_DELETE_PREFIX: &str = "hard-delete/";
const SNAPSHOT_PREFIX: &str = "snapshot/";
/// A serving process snapshots one interval after it starts; this covers its start and upload.
const SNAPSHOT_MARGIN: Duration = Duration::from_secs(60);
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
    /// SIGKILL both, wait out their lease, wipe both local directories, and start the active slot
    /// as a cold standby, so its recovery replays the log since the latest periodic snapshot.
    CrashColdRestartBoth,
    /// Stop the oldest vault Flows and erase their hard-delete values.
    HardDeleteClosedRuns,
}

const FAULT_KINDS: [SoakFault; 5] = [
    SoakFault::KillActive,
    SoakFault::RestartActive,
    SoakFault::ColdRestartBoth,
    SoakFault::CrashColdRestartBoth,
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
    snapshot_interval: Duration,
    /// More hot standby restarts than this fail the soak; `None` only reports them.
    max_standby_restarts: Option<usize>,
    max_takeover_retries: Option<usize>,
    /// How often an s3local soak holds log writes past their deadline, outside the faults.
    log_stall_interval: Option<Duration>,
}

impl SoakSettings {
    /// Panics when the hot standby restarted or a takeover retried more often than allowed.
    fn check_log_limits(&self, log_tally: &LogTally, when: &str) {
        if let Some(limit) = self.max_standby_restarts {
            assert!(
                log_tally.standby_restarts <= limit,
                "{when}: the hot standby restarted {} times, more than {MAX_STANDBY_RESTARTS_VARIABLE}={limit}; {log_tally}",
                log_tally.standby_restarts
            );
        }
        if let Some(limit) = self.max_takeover_retries {
            assert!(
                log_tally.takeover_retries <= limit,
                "{when}: takeovers retried {} times, more than {MAX_TAKEOVER_RETRIES_VARIABLE}={limit}; {log_tally}",
                log_tally.takeover_retries
            );
        }
    }

    /// Returns whether every active process lives long enough to publish a snapshot.
    fn requires_snapshot_restores(&self) -> bool {
        self.fault_interval >= self.snapshot_interval + SNAPSHOT_MARGIN
    }

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
        let snapshot_interval = std::env::var(SNAPSHOT_INTERVAL_VARIABLE)
            .ok()
            .filter(|text| !text.is_empty())
            .map_or(DEFAULT_SNAPSHOT_INTERVAL, |text| {
                parse_duration(&text).unwrap_or_else(|| {
                    panic!("{SNAPSHOT_INTERVAL_VARIABLE} must be a duration, not {text:?}")
                })
            });
        let log_stall_interval = std::env::var(LOG_STALL_INTERVAL_VARIABLE)
            .ok()
            .filter(|text| !text.is_empty())
            .map(|text| {
                parse_duration(&text).unwrap_or_else(|| {
                    panic!("{LOG_STALL_INTERVAL_VARIABLE} must be a duration, not {text:?}")
                })
            });
        Self {
            duration,
            fault_interval,
            seed,
            log_directory,
            keeps_objects: std::env::var(KEEP_OBJECTS_VARIABLE).is_ok_and(|text| text == "1"),
            snapshot_interval,
            max_standby_restarts: count_limit(MAX_STANDBY_RESTARTS_VARIABLE),
            max_takeover_retries: count_limit(MAX_TAKEOVER_RETRIES_VARIABLE),
            log_stall_interval,
        }
    }
}

/// Returns the count that the environment variable `name` sets, when it sets one.
fn count_limit(name: &str) -> Option<usize> {
    std::env::var(name)
        .ok()
        .filter(|text| !text.is_empty())
        .map(|text| {
            text.parse()
                .unwrap_or_else(|_| panic!("{name} must be a count, not {text:?}"))
        })
}

impl fmt::Display for SoakSettings {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(
            formatter,
            "duration {:?}, a fault every {:?}, a snapshot every {:?}, seed {} ({SEED_VARIABLE}), process logs in {}",
            self.duration,
            self.fault_interval,
            self.snapshot_interval,
            self.seed,
            self.log_directory.display()
        )?;
        if let Some(interval) = self.log_stall_interval {
            write!(
                formatter,
                ", a {LOG_STALL_HOLD:?} log write stall every {interval:?}"
            )?;
        }
        write!(
            formatter,
            ", at most {:?} hot standby restarts and {:?} takeover retries",
            self.max_standby_restarts, self.max_takeover_retries
        )
    }
}

/// How long one fault took the cluster to serve again.
struct Recovery {
    fault: SoakFault,
    from: String,
    to: String,
    serving_again_after: Duration,
    /// The log replay of the process that serves again, when it logged one.
    replay: Option<LogReplay>,
}

/// What the recovery of one process logged about its log replay.
struct LogReplay {
    /// The applied seq that replay started after: the restored snapshot's, or a hot standby's.
    last_applied_seq: u64,
    batches: u64,
    /// From the lease to serving, by the process's own log timestamps.
    recovery_duration: Duration,
}

impl LogReplay {
    /// Parses the first recovery in `log`.
    ///
    /// # Errors
    ///
    /// Returns a description of the missing log line or field.
    fn parse(log: &str) -> Result<Self, String> {
        let replay_line = log
            .lines()
            .find(|line| line.contains("replayed the log stream"))
            .ok_or("the process logged no log replay")?;
        let field = |name: &str| {
            let prefix = format!("{name}=");
            replay_line
                .split_whitespace()
                .find_map(|token| token.strip_prefix(prefix.as_str()))
                .and_then(|value| value.parse().ok())
                .ok_or_else(|| format!("the replay line has no numeric {name}: {replay_line}"))
        };
        let line_seconds = |message: &str| {
            log.lines()
                .find(|line| line.contains(message))
                .and_then(seconds_of_day)
        };
        let (recovering, serving) = line_seconds("engine node recovering")
            .zip(line_seconds("engine node serving"))
            .ok_or("the process logged no timed recovery and serving lines")?;
        let recovery_duration = {
            // A recovery that crosses midnight UTC.
            let seconds = if serving < recovering {
                serving + 86_400.0 - recovering
            } else {
                serving - recovering
            };
            Duration::from_secs_f64(seconds)
        };
        Ok(Self {
            last_applied_seq: field("last_applied_seq")?,
            batches: field("batches")?,
            recovery_duration,
        })
    }
}

impl fmt::Display for LogReplay {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(
            formatter,
            "replayed {} batches after seq {} in a {:.1?} recovery",
            self.batches, self.last_applied_seq, self.recovery_duration
        )
    }
}

/// Returns the UTC seconds of the day of a log line that starts with an RFC 3339 timestamp.
fn seconds_of_day(line: &str) -> Option<f64> {
    let time = line.split_whitespace().next()?.split_once('T')?.1;
    let mut parts = time.trim_end_matches('Z').split(':');
    let hours: f64 = parts.next()?.parse().ok()?;
    let minutes: f64 = parts.next()?.parse().ok()?;
    let seconds: f64 = parts.next()?.parse().ok()?;
    Some(hours * 3_600.0 + minutes * 60.0 + seconds)
}

/// Everything the soak reports at the end.
#[derive(Default)]
struct SoakLedger {
    faults: BTreeMap<SoakFault, usize>,
    recoveries: Vec<Recovery>,
    snapshot_restores: Vec<u64>,
    hard_delete_closures: usize,
    /// Whether every cold restart must restore a snapshot; see `requires_snapshot_restores`.
    requires_snapshot_restores: bool,
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
    assert!(
        settings.log_stall_interval.is_none() || target.is_emulated(),
        "{LOG_STALL_INTERVAL_VARIABLE} works only on an s3local target"
    );
    let keeps_objects = settings.keeps_objects;
    let outcome = tokio::spawn(run_soak(Arc::clone(&target), settings, run_id)).await;
    let cleanup_failure = if keeps_objects {
        match target.remaining_keys().await {
            Ok(keys) => {
                eprintln!(
                    "soak: {KEEP_OBJECTS_VARIABLE}=1, so {} objects stay under the prefix",
                    keys.len()
                );
                None
            }
            Err(failure) => Some(failure),
        }
    } else {
        match target.delete_everything().await {
            Ok(remaining) if remaining.is_empty() => {
                eprintln!("soak: no object remains under the prefix after cleanup");
                None
            }
            Ok(remaining) => Some(format!(
                "{} objects remain under the prefix: {:?}",
                remaining.len(),
                &remaining[..remaining.len().min(20)]
            )),
            Err(failure) => Some(failure),
        }
    };
    if let Some(failure) = &cleanup_failure {
        eprintln!("soak cleanup failed: {failure}");
    }
    if let Ok(target) = Arc::try_unwrap(target) {
        target.shutdown().await;
    }
    if let Err(error) = outcome {
        resume_unwind(error.into_panic());
    }
    if let Some(failure) = cleanup_failure {
        panic!("soak cleanup failed: {failure}");
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
        settings.snapshot_interval,
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
    let mut ledger = SoakLedger {
        requires_snapshot_restores: settings.requires_snapshot_restores(),
        ..SoakLedger::default()
    };
    if !ledger.requires_snapshot_restores {
        eprintln!(
            "soak: WARNING: faults come every {:?}, within a {:?} snapshot interval plus {SNAPSHOT_MARGIN:?}, so a cold restart may find no snapshot and the soak does not require one",
            settings.fault_interval, settings.snapshot_interval
        );
    }
    let mut next_fault_at = started_at + settings.fault_interval;
    let mut next_progress_at = started_at + PROGRESS_INTERVAL;
    let mut next_vault_round_at = started_at;
    let mut next_scrape_at = started_at;
    let mut next_log_stall_at = settings
        .log_stall_interval
        .map(|interval| started_at + interval);
    let mut checked_unplanned_exits = 0;
    let soak_ends_at = started_at + settings.duration;
    while Instant::now() < soak_ends_at {
        cluster.supervise();
        if cluster.unplanned_exit_count() > checked_unplanned_exits {
            cluster.wait_for_serving(None, STARTUP_BUDGET).await;
            check_read_back(
                &cluster,
                &workload,
                &mut vault,
                started_at,
                "an unplanned exit",
            )
            .await;
            checked_unplanned_exits = cluster.unplanned_exit_count();
        }
        if let Some(stall_at) = next_log_stall_at
            && Instant::now() >= stall_at
        {
            stall_log_writes(&mut cluster, &target, started_at).await;
            next_log_stall_at = settings
                .log_stall_interval
                .map(|interval| Instant::now() + interval);
            continue;
        }
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
            checked_unplanned_exits = cluster.unplanned_exit_count();
            next_fault_at = Instant::now() + settings.fault_interval;
        }
        if now >= next_progress_at {
            eprintln!(
                "soak +{}: {} serves; {} acknowledged Flow starts and {} acknowledged calls; vault: {}; faults {:?}; {} unplanned exits; {}; {}",
                elapsed(started_at),
                cluster
                    .serving_slot()
                    .map_or("nothing", |slot| cluster.slot_name(slot)),
                workload.acknowledged_start_count(),
                workload.acknowledged_count(),
                vault.report(),
                ledger.faults,
                cluster.unplanned_exit_count(),
                cluster.log_tally_so_far(),
                cluster.tally_so_far()
            );
            settings.check_log_limits(
                cluster.log_tally_so_far(),
                &format!("soak +{}", elapsed(started_at)),
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
        let required_keys = [
            MANIFEST_OBJECT_KEY.to_owned(),
            node_lease_object_key(NODE_ID),
        ];
        Some(check_versioned_layout(&inspection, &objects, &required_keys).await)
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
            "  {:?}: {} served again after {:.1?}, from {}{}",
            recovery.fault,
            recovery.to,
            recovery.serving_again_after,
            recovery.from,
            recovery
                .replay
                .as_ref()
                .map_or_else(String::new, |replay| format!("; {replay}"))
        );
    }
    eprintln!(
        "  {} process starts, {} stale-batch restarts; readiness sampled every 25 ms, which can miss a short overlap that the lease and create-only writes still fence: {:?}",
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
    eprintln!(
        "  {} unplanned exits that a restart recovered{}",
        cluster_report.unplanned_exits.len(),
        cluster_report
            .unplanned_exits
            .iter()
            .map(|exit| format!("\n    UNPLANNED EXIT {exit}"))
            .collect::<String>()
    );
    let log_tally = &cluster_report.log_tally;
    eprintln!(
        "  logs: {log_tally}; the top {TOP_LOG_PATTERNS} patterns:{}",
        log_tally
            .top_patterns(TOP_LOG_PATTERNS)
            .iter()
            .map(|(count, pattern)| format!("\n    {count:>7} {pattern}"))
            .collect::<String>()
    );
    eprintln!("  metrics: {}", cluster_report.tally);
    eprintln!("  objects by area at the end:");
    for (area, (count, bytes)) in count_by_area(&objects) {
        eprintln!("    {area}: {count} objects, {bytes} bytes");
    }
    if ledger.snapshot_restores.is_empty() {
        eprintln!("  WARNING: no cold restart happened, so no snapshot restore was checked");
    }
    if let Some(layout) = &layout {
        eprintln!("  versioned layout: {layout}");
        assert!(
            layout.violations.is_empty(),
            "the versioned keys break the HEAD-probing layout:\n{}",
            layout.violations.join("\n")
        );
    }
    settings.check_log_limits(log_tally, "the summary");
}

/// Holds log writes until the serving process exits on its write deadline, then releases them.
async fn stall_log_writes(cluster: &mut SoakCluster, target: &SoakTarget, started_at: Instant) {
    let exits_before = cluster.unplanned_exit_count();
    eprintln!(
        "soak +{}: holding log writes for {LOG_STALL_HOLD:?}, outside the fault schedule",
        elapsed(started_at)
    );
    target.start_log_stall(LOG_STALL_HOLD).await;
    let deadline = Instant::now() + LOG_STALL_BUDGET;
    while cluster.unplanned_exit_count() == exits_before && Instant::now() < deadline {
        cluster.supervise();
        tokio::time::sleep(LOOP_PAUSE).await;
    }
    target.end_log_stall().await;
    let exits = cluster.unplanned_exit_count() - exits_before;
    eprintln!(
        "soak +{}: released the held log writes; {exits} unplanned exits during the stall",
        elapsed(started_at)
    );
    assert!(
        exits > 0,
        "soak +{}: a {LOG_STALL_HOLD:?} log write stall stopped no process within {LOG_STALL_BUDGET:?}",
        elapsed(started_at)
    );
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
    let slot_count = cluster.slot_count();
    let other_slot = |slot: usize| {
        (0..slot_count)
            .find(|other| *other != slot)
            .expect("the cluster has two slots")
    };
    // A slot restarted after an unplanned exit may still be catching up.
    let active = cluster.wait_for_serving(None, TAKEOVER_BUDGET).await;
    cluster
        .wait_for_hot_standby(other_slot(active), STANDBY_BUDGET)
        .await;
    let active = cluster.wait_for_serving(None, TAKEOVER_BUDGET).await;
    let standby = other_slot(active);
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
                replay: LogReplay::parse(&cluster.process_log(serving)).ok(),
            });
            cluster.spawn(active);
            cluster.wait_for_hot_standby(active, STANDBY_BUDGET).await;
        }
        SoakFault::ColdRestartBoth | SoakFault::CrashColdRestartBoth => {
            let snapshot_manifests = list_all(inspection, SNAPSHOT_PREFIX)
                .await
                .unwrap_or_else(|error| {
                    panic!("fault #{number}: listing the snapshots failed: {error}")
                })
                .iter()
                .filter(|object| object.key.contains("/manifest/"))
                .count();
            let stopped_at = if fault == SoakFault::CrashColdRestartBoth {
                cluster.kill(standby).await;
                cluster.kill(active).await;
                let stopped_at = Instant::now();
                // A process started before the lease expires follows the log as a hot standby.
                tokio::time::sleep(KILLED_LEASE_EXPIRY_WAIT).await;
                stopped_at
            } else {
                cluster.terminate(standby).await;
                let stopped_at = Instant::now();
                cluster.terminate(active).await;
                stopped_at
            };
            cluster.wipe(active);
            cluster.wipe(standby);
            if fault == SoakFault::CrashColdRestartBoth {
                cluster.spawn_as_cold_standby(active);
            } else {
                cluster.spawn(active);
            }
            let serving = cluster.wait_for_serving(None, STARTUP_BUDGET).await;
            let log = cluster.process_log(serving);
            let replay = LogReplay::parse(&log)
                .unwrap_or_else(|failure| panic!("fault #{number}: {failure}:\n{log}"));
            assert!(
                (snapshot_manifests == 0 && !ledger.requires_snapshot_restores)
                    || replay.last_applied_seq > 0,
                "fault #{number}: {snapshot_manifests} snapshot manifests existed, but the cold restart replayed the log from seq 1:\n{log}"
            );
            assert!(
                fault != SoakFault::CrashColdRestartBoth || replay.batches > 0,
                "fault #{number}: the crash left no log after the latest snapshot to replay:\n{log}"
            );
            ledger.snapshot_restores.push(replay.last_applied_seq);
            eprintln!(
                "soak +{}: fault #{number} {fault:?}: {replay}",
                elapsed(started_at)
            );
            ledger.recoveries.push(Recovery {
                fault,
                from,
                to: cluster.slot_name(serving).to_owned(),
                serving_again_after: stopped_at.elapsed(),
                replay: Some(replay),
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
