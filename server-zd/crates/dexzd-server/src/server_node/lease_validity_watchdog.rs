// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! An operating-system thread that ends the process once the lease validity deadline passes.

use std::sync::mpsc::{self, RecvTimeoutError};
use std::thread::JoinHandle;
use std::time::{Duration, Instant};

use dexzd_api::HealthStatus;
use dexzd_cluster_metadata::LeaseValidityDeadline;

/// Time after the deadline for the graceful lease-lost path, which exits 1, before the abort.
pub(crate) const LEASE_EXPIRY_ABORT_BUDGET: Duration = Duration::from_secs(2);
/// Longest sleep between deadline reads, so arming and renewals are noticed promptly.
const DEADLINE_POLL_INTERVAL: Duration = Duration::from_millis(100);

/// Owns the watchdog thread; [`Self::shutdown`] or dropping it stops the thread.
///
/// The thread reads only the lock-free deadline, so it works while the async runtime is stalled.
pub(crate) struct LeaseValidityWatchdog {
    stop_sender: Option<mpsc::Sender<()>>,
    thread: Option<JoinHandle<()>>,
}

impl LeaseValidityWatchdog {
    /// Starts watching `deadline`.
    ///
    /// When it passes, the thread reports NOT_SERVING through `health` without the async runtime.
    /// If the process still runs [`LEASE_EXPIRY_ABORT_BUDGET`] later, it aborts with SIGABRT.
    pub(crate) fn start(
        deadline: LeaseValidityDeadline,
        health: HealthStatus,
    ) -> std::io::Result<Self> {
        let (stop_sender, stop_receiver) = mpsc::channel();
        let thread = std::thread::Builder::new()
            .name("dexzd-lease-watchdog".to_owned())
            .spawn(move || watch_deadline(&deadline, &health, &stop_receiver))?;
        Ok(Self {
            stop_sender: Some(stop_sender),
            thread: Some(thread),
        })
    }

    /// Stops the thread and waits for it; the process then never exits from the watchdog.
    pub(crate) fn shutdown(mut self) {
        self.stop_and_join();
    }

    fn stop_and_join(&mut self) {
        // Dropping the sender disconnects the channel, which wakes the thread at once.
        drop(self.stop_sender.take());
        if let Some(thread) = self.thread.take() {
            thread
                .join()
                .expect("the watchdog thread only waits and logs, so it cannot panic");
        }
    }
}

impl Drop for LeaseValidityWatchdog {
    fn drop(&mut self) {
        self.stop_and_join();
    }
}

/// Waits until the armed deadline passes or the owner stops the watchdog.
fn watch_deadline(
    deadline: &LeaseValidityDeadline,
    health: &HealthStatus,
    stop_receiver: &mpsc::Receiver<()>,
) {
    loop {
        let expired_deadline = loop {
            let now = Instant::now();
            let wait = match deadline.armed_deadline() {
                Some(valid_until) if now >= valid_until => break valid_until,
                Some(valid_until) => (valid_until - now).min(DEADLINE_POLL_INTERVAL),
                None => DEADLINE_POLL_INTERVAL,
            };
            if !is_still_watching(stop_receiver, wait) {
                return;
            }
        };
        tracing::error!(
            overdue_milliseconds = expired_deadline.elapsed().as_millis(),
            abort_budget_milliseconds = LEASE_EXPIRY_ABORT_BUDGET.as_millis(),
            "the node lease validity deadline passed without a renewal; reporting NOT_SERVING"
        );
        report_not_serving(health.clone());
        // Counted from noticing, so a process resumed after a freeze still gets the graceful budget.
        let abort_at = Instant::now().max(expired_deadline) + LEASE_EXPIRY_ABORT_BUDGET;
        loop {
            let remaining = abort_at.saturating_duration_since(Instant::now());
            if remaining.is_zero() {
                break;
            }
            if !is_still_watching(stop_receiver, remaining) {
                return;
            }
        }
        // A confirmed release disarms the deadline; the graceful path then owns the exit.
        if !deadline
            .armed_deadline()
            .is_some_and(|valid_until| Instant::now() >= valid_until)
        {
            tracing::warn!(
                "the node lease was released or renewed after its deadline; not aborting"
            );
            continue;
        }
        tracing::error!(
            "the node did not stop within the lease expiry budget; aborting the process with \
             SIGABRT, because exit handlers wait for local storage background work"
        );
        std::process::abort();
    }
}

/// Waits up to `wait`; returns `false` once the owner stopped the watchdog.
fn is_still_watching(stop_receiver: &mpsc::Receiver<()>, wait: Duration) -> bool {
    match stop_receiver.recv_timeout(wait) {
        Err(RecvTimeoutError::Timeout) => true,
        Ok(()) | Err(RecvTimeoutError::Disconnected) => false,
    }
}

/// Reports NOT_SERVING on a helper thread, so a held health lock cannot delay the exit.
fn report_not_serving(health: HealthStatus) {
    let spawned = std::thread::Builder::new()
        .name("dexzd-lease-expiry-health".to_owned())
        .spawn(move || {
            futures::executor::block_on(health.set_not_serving());
            tracing::warn!("gRPC health reports NOT_SERVING after the lease validity deadline");
        });
    match spawned {
        // Detached on purpose: it finishes at once, and the process exit ends it otherwise.
        Ok(health_thread) => drop(health_thread),
        Err(error) => {
            tracing::error!(%error, "could not start the thread that reports NOT_SERVING")
        }
    }
}

#[cfg(test)]
mod tests {
    use std::process::{Command, Output};
    use std::time::{Duration, Instant};

    use dexzd_api::HealthStatus;
    use dexzd_cluster_metadata::LeaseValidityDeadline;

    use super::{LEASE_EXPIRY_ABORT_BUDGET, LeaseValidityWatchdog};

    /// Set in a re-executed test binary, which then runs one child scenario.
    const CHILD_SCENARIO_VARIABLE: &str = "DEXZD_LEASE_WATCHDOG_CHILD_SCENARIO";
    const INITIAL_VALIDITY: Duration = Duration::from_millis(300);
    /// Validity that the renewing child re-arms every third of it, like a lease.
    const RENEWED_VALIDITY: Duration = Duration::from_millis(1500);
    const SIGABRT: i32 = 6;
    /// Process start and log flushing on a loaded CI machine.
    const EXIT_SLACK: Duration = Duration::from_secs(3);
    const STALLED_WORKER_THREADS: usize = 2;
    const RENEWING_CHILD_RUN_TIME: Duration = Duration::from_secs(4);

    #[test]
    fn a_stalled_runtime_aborts_after_the_deadline_and_reports_not_serving() {
        use std::os::unix::process::ExitStatusExt;

        let started = Instant::now();
        let output = run_child_scenario("child_stalls_every_runtime_worker_past_the_deadline");
        let elapsed = started.elapsed();
        let log = String::from_utf8_lossy(&output.stderr);
        assert_eq!(output.status.signal(), Some(SIGABRT), "{log}");
        assert!(
            elapsed >= INITIAL_VALIDITY + LEASE_EXPIRY_ABORT_BUDGET,
            "aborted after {elapsed:?}, before the graceful budget ended:\n{log}"
        );
        assert!(
            elapsed < INITIAL_VALIDITY + LEASE_EXPIRY_ABORT_BUDGET + EXIT_SLACK,
            "aborted after {elapsed:?}:\n{log}"
        );
        assert!(
            log.contains("gRPC health reports NOT_SERVING after the lease validity deadline"),
            "{log}"
        );
        assert!(log.contains("aborting the process"), "{log}");
    }

    #[test]
    fn a_release_after_the_deadline_prevents_the_abort() {
        let output = run_child_scenario("child_releases_the_lease_after_the_deadline");
        let log = String::from_utf8_lossy(&output.stderr);
        assert_eq!(output.status.code(), Some(0), "{log}");
        assert!(log.contains("after its deadline; not aborting"), "{log}");
    }

    #[test]
    fn a_renewed_deadline_keeps_a_stalled_runtime_alive() {
        let output = run_child_scenario("child_renews_the_deadline_while_every_worker_stalls");
        let log = String::from_utf8_lossy(&output.stderr);
        assert_eq!(output.status.code(), Some(0), "{log}");
        assert!(!log.contains("deadline passed"), "{log}");
    }

    #[test]
    #[ignore = "a child scenario that the parent test runs in its own process"]
    fn child_stalls_every_runtime_worker_past_the_deadline() {
        if !is_child_scenario() {
            return;
        }
        let deadline = LeaseValidityDeadline::disarmed();
        deadline.arm_until(Instant::now() + INITIAL_VALIDITY);
        let (runtime, _watchdog) = start_stalled_runtime(&deadline);
        std::thread::sleep(INITIAL_VALIDITY + LEASE_EXPIRY_ABORT_BUDGET + EXIT_SLACK * 2);
        drop(runtime);
        panic!("the watchdog did not abort the process");
    }

    #[test]
    #[ignore = "a child scenario that the parent test runs in its own process"]
    fn child_releases_the_lease_after_the_deadline() {
        if !is_child_scenario() {
            return;
        }
        let deadline = LeaseValidityDeadline::disarmed();
        deadline.arm_until(Instant::now() + INITIAL_VALIDITY);
        let (runtime, watchdog) = start_stalled_runtime(&deadline);
        std::thread::sleep(INITIAL_VALIDITY + LEASE_EXPIRY_ABORT_BUDGET / 2);
        deadline.disarm();
        std::thread::sleep(LEASE_EXPIRY_ABORT_BUDGET);
        watchdog.shutdown();
        // Dropping the runtime would wait for the stalled workers forever.
        std::mem::forget(runtime);
    }

    #[test]
    #[ignore = "a child scenario that the parent test runs in its own process"]
    fn child_renews_the_deadline_while_every_worker_stalls() {
        if !is_child_scenario() {
            return;
        }
        let deadline = LeaseValidityDeadline::disarmed();
        deadline.arm_until(Instant::now() + RENEWED_VALIDITY);
        let (runtime, watchdog) = start_stalled_runtime(&deadline);
        let renewing_until = Instant::now() + RENEWING_CHILD_RUN_TIME;
        while Instant::now() < renewing_until {
            std::thread::sleep(RENEWED_VALIDITY / 3);
            deadline.arm_until(Instant::now() + RENEWED_VALIDITY);
        }
        watchdog.shutdown();
        // Dropping the runtime would wait for the stalled workers forever.
        std::mem::forget(runtime);
    }

    fn is_child_scenario() -> bool {
        std::env::var_os(CHILD_SCENARIO_VARIABLE).is_some()
    }

    /// Starts the watchdog and blocks every worker of a runtime, like a deadlock.
    fn start_stalled_runtime(
        deadline: &LeaseValidityDeadline,
    ) -> (tokio::runtime::Runtime, LeaseValidityWatchdog) {
        let subscriber = tracing_subscriber::fmt()
            .with_writer(std::io::stderr)
            .finish();
        tracing::subscriber::set_global_default(subscriber)
            .expect("the child process installs one subscriber");
        let runtime = tokio::runtime::Builder::new_multi_thread()
            .worker_threads(STALLED_WORKER_THREADS)
            .enable_all()
            .build()
            .expect("the test runtime starts");
        let health = runtime.block_on(async {
            let (health, _server) = HealthStatus::new(deadline.clone()).await;
            health.set_serving().await;
            health
        });
        let watchdog = LeaseValidityWatchdog::start(deadline.clone(), health)
            .expect("the watchdog thread starts");
        let (stalled_sender, stalled_receiver) = std::sync::mpsc::channel();
        for _ in 0..STALLED_WORKER_THREADS {
            let stalled_sender = stalled_sender.clone();
            drop(runtime.spawn(async move {
                stalled_sender
                    .send(())
                    .expect("the test thread waits for every stalled worker");
                loop {
                    std::thread::sleep(Duration::from_secs(3600));
                }
            }));
        }
        for _ in 0..STALLED_WORKER_THREADS {
            stalled_receiver
                .recv()
                .expect("every worker reports that it stalled");
        }
        (runtime, watchdog)
    }

    fn run_child_scenario(scenario: &str) -> Output {
        let test_binary = std::env::current_exe().expect("the test binary path is known");
        Command::new(test_binary)
            .args([
                &format!("server_node::lease_validity_watchdog::tests::{scenario}"),
                "--exact",
                "--ignored",
                "--nocapture",
                "--test-threads=1",
            ])
            .env(CHILD_SCENARIO_VARIABLE, scenario)
            .output()
            .expect("the child test process runs")
    }
}
