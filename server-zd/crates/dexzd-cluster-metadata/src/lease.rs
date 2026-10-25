// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::Arc;
use std::time::Duration;

use bytes::Bytes;
use dexzd_clock::WallClock;
use dexzd_keyspace::validate_node_identifier;
use dexzd_metrics::{NodeMetrics, Outcome};
use dexzd_object_store::{ObjectStore, ObjectStoreErrorKind, WriteCondition, WriteOutcome};
use dexzd_protocol::dexzd::v1::NodeLease;
use prost::Message;
use tokio::sync::{oneshot, watch};
use tokio::task::JoinHandle;
use tokio::time::Instant;
use uuid::Uuid;

use crate::{LeaseValidityDeadline, MetadataError, NodeConfig, bump_node_epoch};

const RELEASED_LEASE_EXPIRY_UNIX_MICROS: i64 = i64::MIN;
const RELEASE_RETRY_DELAY: Duration = Duration::from_millis(10);
/// Longest wait between reads of a lease another process holds.
const HELD_LEASE_POLL_LIMIT: Duration = Duration::from_secs(1);
/// First wait before retrying a failed renewal attempt; later waits double.
const RENEWAL_RETRY_INITIAL_BACKOFF: Duration = Duration::from_millis(200);
/// Longest wait between renewal attempts.
const RENEWAL_RETRY_MAX_BACKOFF: Duration = Duration::from_secs(2);
/// Floor of one renewal attempt, which otherwise gets one sixth of the lease duration.
/// It keeps a slow but successful write from being cut off and retried in a loop.
const RENEWAL_ATTEMPT_TIMEOUT_FLOOR: Duration = Duration::from_secs(1);
/// Cap of one renewal attempt.
const RENEWAL_ATTEMPT_TIMEOUT_LIMIT: Duration = Duration::from_secs(5);
/// A renewal must complete this long before the deadline, so the watchdog never races its arming.
const RENEWAL_COMPLETION_MARGIN: Duration = Duration::from_millis(100);

/// Timing and contention policy for a node lease.
#[derive(Debug, Clone)]
pub struct LeaseConfig {
    /// Lease duration; defaults to ten seconds.
    pub duration: Duration,
    /// Additional wait after an old lease expires before takeover.
    pub takeover_grace: Duration,
    /// Return immediately when another live process holds the lease.
    pub fail_fast: bool,
    /// Defaults to false; when true, takes over a held lease at once. Set only when its holder provably stopped.
    pub should_take_over_held_lease: bool,
}

impl Default for LeaseConfig {
    fn default() -> Self {
        Self {
            duration: Duration::from_secs(10),
            takeover_grace: Duration::from_secs(1),
            fail_fast: false,
            should_take_over_held_lease: false,
        }
    }
}

impl LeaseConfig {
    /// Acquires the lease, or returns `None` if cancellation arrives before its write begins.
    pub async fn acquire_until_cancelled(
        &self,
        store: Arc<dyn ObjectStore>,
        clock: Arc<dyn WallClock>,
        node: &NodeConfig,
        cancellation: &mut watch::Receiver<bool>,
    ) -> Result<Option<LeaseHandle>, MetadataError> {
        self.acquire_until_cancelled_with_holder_process_id(
            store,
            clock,
            node,
            cancellation,
            Uuid::new_v4().to_string(),
        )
        .await
    }

    /// Acquires the lease like [`Self::acquire_until_cancelled`], naming `holder_process_id` as
    /// its holder.
    pub async fn acquire_until_cancelled_with_holder_process_id(
        &self,
        store: Arc<dyn ObjectStore>,
        clock: Arc<dyn WallClock>,
        node: &NodeConfig,
        cancellation: &mut watch::Receiver<bool>,
        holder_process_id: String,
    ) -> Result<Option<LeaseHandle>, MetadataError> {
        acquire_lease_inner(
            store,
            clock,
            node,
            self,
            Some(cancellation),
            holder_process_id,
            LeaseObservers::default(),
        )
        .await
    }

    /// Acquires a process-specific lease that reports to `observers`.
    pub async fn acquire_until_cancelled_with_observers(
        &self,
        store: Arc<dyn ObjectStore>,
        clock: Arc<dyn WallClock>,
        node: &NodeConfig,
        cancellation: &mut watch::Receiver<bool>,
        holder_process_id: String,
        observers: LeaseObservers,
    ) -> Result<Option<LeaseHandle>, MetadataError> {
        acquire_lease_inner(
            store,
            clock,
            node,
            self,
            Some(cancellation),
            holder_process_id,
            observers,
        )
        .await
    }
}

/// Process-local observers of one lease.
#[derive(Clone, Debug, Default)]
pub struct LeaseObservers {
    /// Records renewal outcomes when set.
    pub metrics: Option<NodeMetrics>,
    /// Armed with the local validity of every successful acquisition and renewal.
    pub validity_deadline: LeaseValidityDeadline,
}

/// Local monotonic lease validity observed by the engine.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum LeaseStatus {
    /// Serving is allowed only until this instant.
    ValidUntil(Instant),
    /// Serving must stop immediately.
    Lost(String),
}

/// Why this process could not confirm its node lease release; the lease stays held until expiry.
#[derive(Debug, thiserror::Error)]
pub enum LeaseReleaseError {
    /// The renewal task ended abnormally, so the state needed for the release write is gone.
    #[error("node {node_id:?} lease renewal task failed before the release: {detail}")]
    RenewalTaskFailed {
        /// Node ID.
        node_id: String,
        /// How the task ended.
        detail: String,
    },
    /// Object storage rejected the release write or its follow-up read.
    #[error("releasing node {node_id:?} lease failed: {source}")]
    Rejected {
        /// Node ID.
        node_id: String,
        /// The failed metadata operation.
        #[source]
        source: MetadataError,
    },
    /// Stopping renewal and writing the release did not finish within the budget.
    #[error("releasing node {node_id:?} lease did not finish within {budget:?}")]
    TimedOut {
        /// Node ID.
        node_id: String,
        /// The exceeded budget, [`LeaseHandle::RELEASE_BUDGET`].
        budget: Duration,
    },
}

/// Owns lease renewal and publishes lease validity.
///
/// Dropping it unreleased leaves the lease to expire, like a crash.
pub struct LeaseHandle {
    node_id: String,
    epoch: u64,
    status: watch::Receiver<LeaseStatus>,
    validity_deadline: LeaseValidityDeadline,
    renewal_task: Option<JoinHandle<LeaseOwner>>,
    shutdown_sender: Option<oneshot::Sender<()>>,
}

impl LeaseHandle {
    /// Longest time [`LeaseHandle::shutdown`] spends stopping renewal and writing the release.
    pub const RELEASE_BUDGET: Duration = Duration::from_secs(2);

    /// Returns the node epoch assigned after lease acquisition.
    pub fn epoch(&self) -> u64 {
        self.epoch
    }

    /// Subscribes to local monotonic lease validity.
    pub fn subscribe(&self) -> watch::Receiver<LeaseStatus> {
        self.status.clone()
    }

    /// Returns the lock-free deadline that renewals extend and a confirmed release disarms.
    pub fn validity_deadline(&self) -> LeaseValidityDeadline {
        self.validity_deadline.clone()
    }

    /// Stops renewal and releases the lease after the node stops serving.
    /// Subscribers observe `Lost` before the release write.
    ///
    /// Returns `Ok` once this process no longer holds the lease: the release was written, or another
    /// process had already taken the lease over. Returns [`LeaseReleaseError`] when the release could
    /// not be confirmed within [`LeaseHandle::RELEASE_BUDGET`]; the lease then stays held until it
    /// expires.
    pub async fn shutdown(mut self) -> Result<(), LeaseReleaseError> {
        let stopped =
            tokio::time::timeout(Self::RELEASE_BUDGET, self.stop_renewal_and_release()).await;
        stopped.unwrap_or_else(|_| {
            Err(LeaseReleaseError::TimedOut {
                node_id: self.node_id.clone(),
                budget: Self::RELEASE_BUDGET,
            })
        })
    }

    async fn stop_renewal_and_release(&mut self) -> Result<(), LeaseReleaseError> {
        if let Some(sender) = self.shutdown_sender.take()
            && sender.send(()).is_err()
        {
            tracing::debug!("lease renewal task stopped before shutdown");
        }
        let task = self
            .renewal_task
            .as_mut()
            .expect("only shutdown takes the renewal task, and it consumes the handle");
        let owner_result = task.await;
        drop(self.renewal_task.take());
        let owner = owner_result.map_err(|error| LeaseReleaseError::RenewalTaskFailed {
            node_id: self.node_id.clone(),
            detail: error.to_string(),
        })?;
        drop(owner.status.send_replace(LeaseStatus::Lost(
            "lease released during shutdown".to_owned(),
        )));
        owner
            .release()
            .await
            .map_err(|source| LeaseReleaseError::Rejected {
                node_id: self.node_id.clone(),
                source,
            })
    }
}

impl Drop for LeaseHandle {
    fn drop(&mut self) {
        if let Some(task) = self.renewal_task.take() {
            task.abort();
        }
    }
}

struct LeaseOwner {
    metrics: Option<NodeMetrics>,
    store: Arc<dyn ObjectStore>,
    clock: Arc<dyn WallClock>,
    key: String,
    lease: NodeLease,
    etag: String,
    duration: Duration,
    last_request_sent: Instant,
    current_valid_until: Instant,
    status: watch::Sender<LeaseStatus>,
    validity_deadline: LeaseValidityDeadline,
    /// Writes of the current renewal cycle; a timed-out one may still have landed.
    unconfirmed_renewals: Vec<UnconfirmedRenewal>,
}

/// Acquires a process-specific node lease, raises its epoch, and starts renewal.
///
/// Polls a held lease at most every second, so a released lease is taken at once.
/// Each wait is randomized between half and all of that interval, so waiters spread their reads.
pub async fn acquire_lease(
    store: Arc<dyn ObjectStore>,
    clock: Arc<dyn WallClock>,
    node: &NodeConfig,
    config: &LeaseConfig,
) -> Result<LeaseHandle, MetadataError> {
    acquire_lease_with_holder_process_id(store, clock, node, config, Uuid::new_v4().to_string())
        .await
}

/// Acquires the lease like [`acquire_lease`], naming `holder_process_id` as its holder.
pub async fn acquire_lease_with_holder_process_id(
    store: Arc<dyn ObjectStore>,
    clock: Arc<dyn WallClock>,
    node: &NodeConfig,
    config: &LeaseConfig,
    holder_process_id: String,
) -> Result<LeaseHandle, MetadataError> {
    let acquired = acquire_lease_inner(
        store,
        clock,
        node,
        config,
        None,
        holder_process_id,
        LeaseObservers::default(),
    )
    .await?;
    Ok(acquired.expect("a lease acquisition without cancellation must complete"))
}

async fn acquire_lease_inner(
    store: Arc<dyn ObjectStore>,
    clock: Arc<dyn WallClock>,
    node: &NodeConfig,
    config: &LeaseConfig,
    mut cancellation: Option<&mut watch::Receiver<bool>>,
    process_id: String,
    observers: LeaseObservers,
) -> Result<Option<LeaseHandle>, MetadataError> {
    let LeaseObservers {
        metrics,
        validity_deadline,
    } = observers;
    let duration_micros =
        i64::try_from(config.duration.as_micros()).map_err(|_| MetadataError::InvalidConfig {
            field: "lease duration",
            detail: "lease duration exceeds i64 microseconds".to_owned(),
        })?;
    if duration_micros <= 0 || config.duration / 3 == Duration::ZERO {
        return Err(MetadataError::InvalidConfig {
            field: "lease duration",
            detail: "lease duration must permit a positive renewal interval".to_owned(),
        });
    }
    validate_node_identifier(&node.node_id)?;
    let grace_micros = i64::try_from(config.takeover_grace.as_micros()).map_err(|_| {
        MetadataError::InvalidConfig {
            field: "lease takeover grace",
            detail: "takeover grace exceeds i64 microseconds".to_owned(),
        }
    })?;
    let key = format!("lease/{}", node.node_id);
    let held_lease_poll_interval = HELD_LEASE_POLL_LIMIT.min(config.duration / 3);
    let mut has_reported_wait = false;
    loop {
        if cancellation
            .as_ref()
            .is_some_and(|receiver| *receiver.borrow())
        {
            return Ok(None);
        }
        let existing = store.get(&key).await?;
        let previous = existing
            .as_ref()
            .map(|content| {
                NodeLease::decode(content.bytes.clone()).map_err(|source| MetadataError::Decode {
                    key: "lease",
                    source,
                })
            })
            .transpose()?;
        let sent_at = Instant::now();
        let now = clock.now_unix_micros();
        if let Some(previous) = &previous {
            if previous.node_id != node.node_id {
                return Err(MetadataError::InvalidMetadata {
                    key: "lease",
                    detail: "lease node ID disagrees with its object key".to_owned(),
                });
            }
            let available_at = previous.expires_at_unix_micros.saturating_add(grace_micros);
            if now < available_at && config.should_take_over_held_lease {
                // The conditional write and epoch raise below still fence the previous holder.
                tracing::info!(
                    node_id = %node.node_id,
                    holder_process_id = %previous.holder_process_id,
                    expires_at_unix_micros = previous.expires_at_unix_micros,
                    "taking over the node lease of a stopped process without waiting for its expiry"
                );
            } else if now < available_at {
                if config.fail_fast {
                    return Err(MetadataError::LeaseHeld {
                        node_id: node.node_id.clone(),
                        expires_at_unix_micros: previous.expires_at_unix_micros,
                    });
                }
                if !has_reported_wait {
                    tracing::info!(
                        node_id = %node.node_id,
                        holder_process_id = %previous.holder_process_id,
                        expires_at_unix_micros = previous.expires_at_unix_micros,
                        "another process holds the node lease; waiting for its release or expiry"
                    );
                    has_reported_wait = true;
                }
                let until_available = Duration::from_micros((available_at - now) as u64);
                let poll_delay = jittered_poll_delay(held_lease_poll_interval);
                let delay = until_available.min(poll_delay);
                if let Some(cancellation_receiver) = cancellation.as_mut() {
                    tokio::select! {
                        () = tokio::time::sleep(delay) => {}
                        changed = cancellation_receiver.changed() => {
                            if changed.is_ok() && *cancellation_receiver.borrow() {
                                return Ok(None);
                            }
                            if changed.is_err() {
                                tokio::time::sleep(delay).await;
                            }
                        }
                    }
                } else {
                    tokio::time::sleep(delay).await;
                }
                continue;
            }
        }
        if cancellation
            .as_ref()
            .is_some_and(|receiver| *receiver.borrow())
        {
            return Ok(None);
        }
        let version = previous
            .as_ref()
            .map_or(0, |lease| lease.version)
            .checked_add(1)
            .ok_or(MetadataError::Overflow {
                field: "node lease version",
            })?;
        let lease = NodeLease {
            node_id: node.node_id.clone(),
            incarnation: node.incarnation,
            epoch: previous.as_ref().map_or(0, |lease| lease.epoch),
            holder_process_id: process_id.clone(),
            expires_at_unix_micros: now.checked_add(duration_micros).ok_or(
                MetadataError::Overflow {
                    field: "lease expiry",
                },
            )?,
            duration_micros,
            version,
        };
        let condition = existing.map_or(WriteCondition::CreateOnly, |content| {
            WriteCondition::ReplaceIfEtagMatches { etag: content.etag }
        });
        let write_result = store
            .put(&key, Bytes::from(lease.encode_to_vec()), condition)
            .await;
        let etag = match write_result {
            Ok(WriteOutcome::Written { etag }) => etag,
            Ok(WriteOutcome::PreconditionFailed) => continue,
            Err(error) if error.kind == ObjectStoreErrorKind::OutcomeUnknown => {
                let current = store.get(&key).await?;
                match current {
                    Some(content)
                        if NodeLease::decode(content.bytes.clone()).map_err(|source| {
                            MetadataError::Decode {
                                key: "lease",
                                source,
                            }
                        })? == lease =>
                    {
                        content.etag
                    }
                    _ => continue,
                }
            }
            Err(error) => return Err(error.into()),
        };
        let valid_until = sent_at + config.duration;
        validity_deadline.arm_until(valid_until.into_std());
        let (status, mut receiver) = watch::channel(LeaseStatus::ValidUntil(valid_until));
        let mut pending = PendingLease {
            owner: Some(LeaseOwner {
                metrics: metrics.clone(),
                store,
                clock,
                key,
                lease,
                etag,
                duration: config.duration,
                last_request_sent: sent_at,
                current_valid_until: valid_until,
                status,
                validity_deadline: validity_deadline.clone(),
                unconfirmed_renewals: Vec::new(),
            }),
        };
        let epoch = match pending.owner_mut().finish_acquisition(node).await {
            Ok(epoch) => epoch,
            Err(error) => return Err(pending.release_after(error).await),
        };
        let owner = pending.take_owner();
        drop(receiver.borrow_and_update());
        let (shutdown_sender, shutdown_receiver) = oneshot::channel();
        let renewal_task = tokio::spawn(async move { owner.run(shutdown_receiver).await });
        return Ok(Some(LeaseHandle {
            node_id: node.node_id.clone(),
            epoch,
            status: receiver,
            validity_deadline,
            renewal_task: Some(renewal_task),
            shutdown_sender: Some(shutdown_sender),
        }));
    }
}

/// A written lease still being acquired; dropping it releases the lease in the background.
struct PendingLease {
    owner: Option<LeaseOwner>,
}

impl PendingLease {
    fn owner_mut(&mut self) -> &mut LeaseOwner {
        self.owner
            .as_mut()
            .expect("the owner stays until the acquisition finishes")
    }

    /// Disarms the guard and returns the owner.
    fn take_owner(&mut self) -> LeaseOwner {
        self.owner
            .take()
            .expect("the owner stays until the acquisition finishes")
    }

    /// Releases the lease, then returns `error`. The release continues if the caller is cancelled.
    async fn release_after(mut self, error: MetadataError) -> MetadataError {
        let owner = self.take_owner();
        let release_task = tokio::spawn(release_unfinished_acquisition(owner));
        if let Err(join_error) = release_task.await {
            tracing::error!(
                error = %join_error,
                "the node lease release task failed after an unfinished acquisition"
            );
        }
        error
    }
}

impl Drop for PendingLease {
    fn drop(&mut self) {
        let Some(owner) = self.owner.take() else {
            return;
        };
        match tokio::runtime::Handle::try_current() {
            Ok(runtime) => {
                // Detached on purpose: the release must outlive the cancelled acquisition.
                let release_task = runtime.spawn(release_unfinished_acquisition(owner));
                drop(release_task);
            }
            Err(_) => tracing::error!(
                node_id = %owner.lease.node_id,
                "no async runtime can release the node lease; it stays held until it expires"
            ),
        }
    }
}

/// Releases the lease of a failed or cancelled acquisition within the release budget.
async fn release_unfinished_acquisition(owner: LeaseOwner) {
    let node_id = owner.lease.node_id.clone();
    match tokio::time::timeout(LeaseHandle::RELEASE_BUDGET, owner.release()).await {
        Ok(Ok(())) => tracing::info!(
            %node_id,
            "released the node lease after an unfinished acquisition"
        ),
        Ok(Err(error)) => tracing::error!(
            %node_id,
            %error,
            "the node lease stays held after an unfinished acquisition"
        ),
        Err(_) => tracing::error!(
            %node_id,
            budget_milliseconds = LeaseHandle::RELEASE_BUDGET.as_millis(),
            "releasing the node lease after an unfinished acquisition timed out"
        ),
    }
}

impl LeaseOwner {
    async fn run(mut self, mut shutdown_receiver: oneshot::Receiver<()>) -> Self {
        loop {
            tokio::select! {
                biased;
                _ = &mut shutdown_receiver => return self,
                () = tokio::time::sleep_until(self.last_request_sent + self.duration / 3) => {}
            }
            let renewal_result = tokio::select! {
                biased;
                _ = &mut shutdown_receiver => return self,
                result = self.renew_before_deadline() => result,
            };
            if let Err(error) = renewal_result {
                drop(
                    self.status
                        .send_replace(LeaseStatus::Lost(error.to_string())),
                );
                return self;
            }
        }
    }

    /// Raises the node epoch and renews once, both within the first lease period.
    ///
    /// Returns the raised epoch, or the error that ended the acquisition.
    async fn finish_acquisition(&mut self, node: &NodeConfig) -> Result<u64, MetadataError> {
        if Instant::now() >= self.current_valid_until {
            return Err(self.lost("acquisition completed after local lease deadline"));
        }
        let epoch = bump_node_epoch(self.store.as_ref(), self.clock.as_ref(), node).await?;
        if Instant::now() >= self.current_valid_until {
            return Err(self.lost("epoch update completed after local lease deadline"));
        }
        self.lease.epoch = epoch;
        self.renew_before_deadline().await?;
        Ok(epoch)
    }

    /// Writes an expired copy of the lease and disarms the validity deadline once that is confirmed.
    ///
    /// Returns `Ok` once this process no longer holds the lease, including when another process
    /// already took it over, and the storage or decode error otherwise.
    async fn release(&self) -> Result<(), MetadataError> {
        let released = self.write_release().await;
        if released.is_ok() {
            self.validity_deadline.disarm();
        }
        released
    }

    async fn write_release(&self) -> Result<(), MetadataError> {
        let mut current_lease = self.lease.clone();
        let mut current_etag = self.etag.clone();
        loop {
            let mut released = current_lease.clone();
            released.version = released
                .version
                .checked_add(1)
                .ok_or(MetadataError::Overflow {
                    field: "node lease version",
                })?;
            released.expires_at_unix_micros = RELEASED_LEASE_EXPIRY_UNIX_MICROS;
            let outcome = self
                .store
                .put(
                    &self.key,
                    Bytes::from(released.encode_to_vec()),
                    WriteCondition::ReplaceIfEtagMatches { etag: current_etag },
                )
                .await;
            match outcome {
                Ok(WriteOutcome::Written { .. }) => return Ok(()),
                Ok(WriteOutcome::PreconditionFailed) => {}
                Err(error) if error.kind == ObjectStoreErrorKind::OutcomeUnknown => {}
                Err(error) => return Err(error.into()),
            }
            let Some(content) = self.store.get(&self.key).await? else {
                return Ok(());
            };
            let stored_lease =
                NodeLease::decode(content.bytes).map_err(|source| MetadataError::Decode {
                    key: "lease",
                    source,
                })?;
            if stored_lease == released
                || (stored_lease.holder_process_id == self.lease.holder_process_id
                    && stored_lease.expires_at_unix_micros == RELEASED_LEASE_EXPIRY_UNIX_MICROS)
            {
                return Ok(());
            }
            if stored_lease.holder_process_id != self.lease.holder_process_id {
                tracing::info!(
                    node_id = %self.lease.node_id,
                    "another process took the node lease over before the release"
                );
                return Ok(());
            }
            current_lease = stored_lease;
            current_etag = content.etag;
            tokio::time::sleep(RELEASE_RETRY_DELAY).await;
        }
    }

    /// Renews the lease, retrying transient failures until the renewal cutoff before the deadline.
    ///
    /// Each attempt times out after `attempt_timeout` while a retry still fits after it; the last
    /// attempt may use all remaining time.
    ///
    /// Returns `Ok` after a confirmed renewal, and [`MetadataError::LeaseLost`] or the storage
    /// error that lost the lease otherwise.
    async fn renew_before_deadline(&mut self) -> Result<(), MetadataError> {
        let attempt_timeout =
            (self.duration / 6).clamp(RENEWAL_ATTEMPT_TIMEOUT_FLOOR, RENEWAL_ATTEMPT_TIMEOUT_LIMIT);
        let mut retry_backoff = RENEWAL_RETRY_INITIAL_BACKOFF;
        let mut attempt_number: u32 = 0;
        loop {
            attempt_number = attempt_number.saturating_add(1);
            let attempt_cutoff = Instant::now() + attempt_timeout;
            // The last attempt that fits gets all remaining time, as a single attempt did before.
            let is_retry_possible_after_attempt =
                attempt_cutoff + retry_backoff + attempt_timeout <= self.renewal_cutoff();
            let attempt_deadline = if is_retry_possible_after_attempt {
                attempt_cutoff
            } else {
                self.renewal_cutoff()
            };
            let attempt = match tokio::time::timeout_at(attempt_deadline, self.renew()).await {
                Ok(attempt) => attempt,
                Err(_) if Instant::now() >= self.renewal_cutoff() => Err(
                    RenewalAttemptFailure::Lost(self.lost("renewal exceeded local lease deadline")),
                ),
                Err(_) => Err(RenewalAttemptFailure::Retryable(format!(
                    "renewal attempt exceeded its {attempt_timeout:?} timeout"
                ))),
            };
            if let Some(metrics) = &self.metrics {
                metrics.record_lease_renewal(if attempt.is_ok() {
                    Outcome::Ok
                } else {
                    Outcome::Error
                });
            }
            let failure = match attempt {
                Ok(()) => return Ok(()),
                Err(RenewalAttemptFailure::Lost(error)) => return Err(error),
                Err(RenewalAttemptFailure::Retryable(failure)) => failure,
            };
            let retry_delay = self.renewal_retry_delay(retry_backoff, attempt_number);
            if Instant::now() + retry_delay >= self.renewal_cutoff() {
                return Err(self.lost(&format!(
                    "renewal retries ran out before the local lease deadline; last failure: {failure}"
                )));
            }
            tracing::warn!(
                node_id = %self.lease.node_id,
                %failure,
                retry_in_milliseconds = retry_delay.as_millis(),
                "node lease renewal attempt failed; retrying"
            );
            tokio::time::sleep(retry_delay).await;
            retry_backoff = (retry_backoff * 2).min(RENEWAL_RETRY_MAX_BACKOFF);
        }
    }

    async fn renew(&mut self) -> Result<(), RenewalAttemptFailure> {
        let mut proposed = self.lease.clone();
        proposed.version = proposed
            .version
            .checked_add(1)
            .ok_or(RenewalAttemptFailure::Lost(MetadataError::Overflow {
                field: "node lease version",
            }))?;
        let sent_at = Instant::now();
        proposed.expires_at_unix_micros = self
            .clock
            .now_unix_micros()
            .checked_add(proposed.duration_micros)
            .ok_or(RenewalAttemptFailure::Lost(MetadataError::Overflow {
                field: "lease expiry",
            }))?;
        // Recorded before the write, so a timed-out attempt that landed is recognized later.
        self.unconfirmed_renewals.push(UnconfirmedRenewal {
            proposed: proposed.clone(),
            sent_at,
        });
        let condition = WriteCondition::ReplaceIfEtagMatches {
            etag: self.etag.clone(),
        };
        let result = self
            .store
            .put(&self.key, Bytes::from(proposed.encode_to_vec()), condition)
            .await;
        match result {
            Ok(WriteOutcome::Written { etag }) => self.complete_renewal(proposed, etag, sent_at),
            Ok(WriteOutcome::PreconditionFailed) => {
                match self
                    .reconcile_unconfirmed_renewal("renewal precondition failed")
                    .await?
                {
                    ReconciledRenewal::Landed {
                        lease,
                        etag,
                        sent_at,
                    } => self.complete_renewal(lease, etag, sent_at),
                    ReconciledRenewal::NotExtended => Err(RenewalAttemptFailure::Retryable(
                        "renewal precondition failed while this process still holds the lease"
                            .to_owned(),
                    )),
                }
            }
            Err(error) if error.kind == ObjectStoreErrorKind::OutcomeUnknown => {
                match self
                    .reconcile_unconfirmed_renewal("renewal outcome could not be confirmed")
                    .await?
                {
                    ReconciledRenewal::Landed {
                        lease,
                        etag,
                        sent_at,
                    } => self.complete_renewal(lease, etag, sent_at),
                    ReconciledRenewal::NotExtended => {
                        Err(RenewalAttemptFailure::Retryable(format!(
                            "renewal outcome was unknown and the write did not take effect: {error}"
                        )))
                    }
                }
            }
            Err(error) if error.kind == ObjectStoreErrorKind::Transient => {
                Err(RenewalAttemptFailure::Retryable(error.to_string()))
            }
            Err(error) => Err(RenewalAttemptFailure::Lost(error.into())),
        }
    }

    /// Adopts a stored renewal whose write was sent at `sent_at` and publishes its validity.
    fn complete_renewal(
        &mut self,
        lease: NodeLease,
        etag: String,
        sent_at: Instant,
    ) -> Result<(), RenewalAttemptFailure> {
        let valid_until = sent_at + self.duration;
        if Instant::now() >= self.renewal_cutoff() {
            return Err(RenewalAttemptFailure::Lost(
                self.lost("renewal completed after previous lease deadline"),
            ));
        }
        if Instant::now() >= valid_until {
            return Err(RenewalAttemptFailure::Lost(
                self.lost("renewal completed after local lease deadline"),
            ));
        }
        self.lease = lease;
        self.etag = etag;
        self.last_request_sent = sent_at;
        self.current_valid_until = valid_until;
        self.unconfirmed_renewals.clear();
        self.validity_deadline.arm_until(valid_until.into_std());
        drop(
            self.status
                .send_replace(LeaseStatus::ValidUntil(valid_until)),
        );
        Ok(())
    }

    /// Reads the lease after a renewal write that failed its condition or had an unknown outcome.
    ///
    /// Returns [`ReconciledRenewal::Landed`] when one of this cycle's attempts is stored, and
    /// [`ReconciledRenewal::NotExtended`] when this process still holds a lease no attempt extended.
    /// Returns [`RenewalAttemptFailure::Lost`] when another process holds the lease, and
    /// [`RenewalAttemptFailure::Retryable`] when the read failed transiently.
    async fn reconcile_unconfirmed_renewal(
        &mut self,
        lost_detail: &str,
    ) -> Result<ReconciledRenewal, RenewalAttemptFailure> {
        let current = match self.store.get(&self.key).await {
            Ok(current) => current,
            Err(error)
                if matches!(
                    error.kind,
                    ObjectStoreErrorKind::Transient | ObjectStoreErrorKind::OutcomeUnknown
                ) =>
            {
                return Err(RenewalAttemptFailure::Retryable(error.to_string()));
            }
            Err(error) => return Err(RenewalAttemptFailure::Lost(error.into())),
        };
        let Some(content) = current else {
            return Err(RenewalAttemptFailure::Lost(self.lost(lost_detail)));
        };
        let stored = NodeLease::decode(content.bytes).map_err(|source| {
            RenewalAttemptFailure::Lost(MetadataError::Decode {
                key: "lease",
                source,
            })
        })?;
        if let Some(landed) = self
            .unconfirmed_renewals
            .iter()
            .find(|attempt| attempt.proposed == stored)
        {
            return Ok(ReconciledRenewal::Landed {
                sent_at: landed.sent_at,
                lease: stored,
                etag: content.etag,
            });
        }
        let is_held_by_this_process = stored.holder_process_id == self.lease.holder_process_id
            && stored.version >= self.lease.version;
        if !is_held_by_this_process {
            return Err(RenewalAttemptFailure::Lost(self.lost(lost_detail)));
        }
        // Only the version and ETag move on; a just-raised epoch must not revert.
        self.lease.version = stored.version;
        self.etag = content.etag;
        Ok(ReconciledRenewal::NotExtended)
    }

    /// Returns the last instant a renewal may complete, [`RENEWAL_COMPLETION_MARGIN`] before the deadline.
    fn renewal_cutoff(&self) -> Instant {
        self.current_valid_until
            .checked_sub(RENEWAL_COMPLETION_MARGIN)
            .unwrap_or(self.current_valid_until)
    }

    /// Returns a delay between half of `backoff` and all of it.
    ///
    /// A fixed hash of the holder keeps simulations repeatable across toolchains and spreads processes.
    fn renewal_retry_delay(&self, backoff: Duration, attempt_number: u32) -> Duration {
        let holder_hash = self
            .lease
            .holder_process_id
            .bytes()
            .fold(FNV_OFFSET_BASIS, |hash, byte| {
                (hash ^ u64::from(byte)).wrapping_mul(FNV_PRIME)
            });
        let mixed = splitmix64(
            holder_hash ^ splitmix64(self.lease.version ^ splitmix64(u64::from(attempt_number))),
        );
        let half_nanos = u64::try_from((backoff / 2).as_nanos()).unwrap_or(u64::MAX);
        let reduction_nanos = mixed % half_nanos.saturating_add(1);
        backoff - Duration::from_nanos(reduction_nanos)
    }

    fn lost(&self, detail: &str) -> MetadataError {
        MetadataError::LeaseLost {
            node_id: self.lease.node_id.clone(),
            detail: detail.to_owned(),
        }
    }
}

/// A renewal write sent since the last confirmed renewal.
struct UnconfirmedRenewal {
    proposed: NodeLease,
    sent_at: Instant,
}

/// What a read of the lease shows after an unconfirmed renewal write.
enum ReconciledRenewal {
    /// One of this cycle's writes is stored; validity counts from when it was sent.
    Landed {
        lease: NodeLease,
        etag: String,
        sent_at: Instant,
    },
    /// This process still holds the lease, but no write of this cycle extended it.
    NotExtended,
}

/// Why one renewal attempt failed.
enum RenewalAttemptFailure {
    /// The lease may still be this process's, so another attempt can succeed.
    Retryable(String),
    /// Another process holds the lease, or retrying cannot help.
    Lost(MetadataError),
}

/// FNV-1a parameters for hashing the holder process ID.
const FNV_OFFSET_BASIS: u64 = 0xcbf2_9ce4_8422_2325;
const FNV_PRIME: u64 = 0x0000_0100_0000_01b3;

/// Mixes `value` with the SplitMix64 finalizer.
fn splitmix64(value: u64) -> u64 {
    let mut mixed = value.wrapping_add(0x9e37_79b9_7f4a_7c15);
    mixed = (mixed ^ (mixed >> 30)).wrapping_mul(0xbf58_476d_1ce4_e5b9);
    mixed = (mixed ^ (mixed >> 27)).wrapping_mul(0x94d0_49bb_1331_11eb);
    mixed ^ (mixed >> 31)
}

/// Returns a random delay between half of `interval` and all of it.
fn jittered_poll_delay(interval: Duration) -> Duration {
    interval - rand::random_range(Duration::ZERO..=interval / 2)
}
