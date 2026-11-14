// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! An in-memory inner store with only create-only conditions, a paused-time clock, and faults.

use std::path::Path;
use std::sync::atomic::{AtomicBool, AtomicI64, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use async_trait::async_trait;
use bytes::Bytes;
use dexzd_clock::WallClock;
use dexzd_object_store::{
    ByteRange, FaultAction, FaultInjector, InMemoryObjectStore, InMemoryObjectStoreOptions,
    ObjectContent, ObjectListing, ObjectMetadata, ObjectStore, ObjectStoreOperation,
    ObjectStoreResult, WriteCondition, WriteOutcome,
};
use rand::{RngExt, SeedableRng};
use rand_chacha::ChaCha8Rng;
use tokio::sync::Semaphore;
use tokio::time::Instant;

/// The reclamation age of most adapters under test; tests of the production age name it.
pub(crate) const TEST_MINIMUM_SUPERSEDED_AGE: Duration = Duration::from_secs(60);

/// Wall-clock time that advances with Tokio's paused clock.
pub(crate) struct TokioWallClock {
    origin: Instant,
    origin_unix_micros: i64,
}

impl TokioWallClock {
    pub(crate) fn new() -> Self {
        Self::starting_at(1_800_000_000_000_000)
    }

    /// Returns a clock that reads `origin_unix_micros` now.
    pub(crate) fn starting_at(origin_unix_micros: i64) -> Self {
        Self {
            origin: Instant::now(),
            origin_unix_micros,
        }
    }
}

impl WallClock for TokioWallClock {
    fn now_unix_micros(&self) -> i64 {
        self.origin_unix_micros
            + i64::try_from(self.origin.elapsed().as_micros()).expect("test time fits in i64")
    }
}

/// A store clock that moves only when a test advances it, independent of Tokio's clock.
///
/// Holding Tokio's clock still while this one advances models every process whose local clock
/// stands still, as in a frozen VM, while the store and other processes move on.
#[derive(Clone)]
pub(crate) struct ManualStoreClock {
    now_unix_micros: Arc<AtomicI64>,
}

impl ManualStoreClock {
    pub(crate) fn new() -> Self {
        Self {
            now_unix_micros: Arc::new(AtomicI64::new(1_800_000_000_000_000)),
        }
    }

    /// Moves the store clock forward by `duration`; Tokio's clock does not move.
    pub(crate) fn advance(&self, duration: Duration) {
        self.now_unix_micros.fetch_add(
            i64::try_from(duration.as_micros()).expect("the advance fits in i64"),
            Ordering::SeqCst,
        );
    }
}

impl WallClock for ManualStoreClock {
    fn now_unix_micros(&self) -> i64 {
        self.now_unix_micros.load(Ordering::SeqCst)
    }
}

/// A store clock that runs `rate` times faster than Tokio's clock, as if every process's local
/// clock ran that much slower than the store's.
pub(crate) struct FastStoreClock {
    origin: Instant,
    origin_unix_micros: i64,
    rate: i64,
}

impl FastStoreClock {
    pub(crate) fn new(rate: i64) -> Self {
        Self {
            origin: Instant::now(),
            origin_unix_micros: 1_800_000_000_000_000,
            rate,
        }
    }
}

impl WallClock for FastStoreClock {
    fn now_unix_micros(&self) -> i64 {
        let elapsed =
            i64::try_from(self.origin.elapsed().as_micros()).expect("test time fits in i64");
        self.origin_unix_micros + elapsed * self.rate
    }
}

/// One scripted fault: `action` for the next `remaining` matching calls after `skip` of them.
struct ScriptedFault {
    operation: ObjectStoreOperation,
    key_prefix: String,
    skip: u32,
    remaining: u32,
    action: FaultAction,
    /// When set, the fault matches only after a put of this exact key.
    armed_by_put_of: Option<String>,
}

/// Seeded random delays of unscripted calls: mostly short, with occasional long stalls.
#[derive(Debug, Clone, Copy)]
pub(crate) struct RandomDelays {
    /// Longest ordinary delay.
    pub(crate) maximum_delay: Duration,
    /// Probability that a call stalls instead.
    pub(crate) stall_probability: f64,
    /// Longest stall.
    pub(crate) maximum_stall: Duration,
}

impl RandomDelays {
    /// Delays every call by up to `maximum_delay`, without stalls.
    pub(crate) fn uniform(maximum_delay: Duration) -> Self {
        Self {
            maximum_delay,
            stall_probability: 0.0,
            maximum_stall: Duration::ZERO,
        }
    }
}

/// Scripted faults first, then optional seeded random delays.
pub(crate) struct TestFaults {
    scripted: Mutex<Vec<ScriptedFault>>,
    random_delays: Mutex<Option<(ChaCha8Rng, RandomDelays)>>,
    is_failing_every_delete: AtomicBool,
}

impl TestFaults {
    fn new() -> Self {
        Self {
            scripted: Mutex::new(Vec::new()),
            random_delays: Mutex::new(None),
            is_failing_every_delete: AtomicBool::new(false),
        }
    }

    /// Applies `action` to `times` calls of `operation` under `key_prefix` after skipping `skip`.
    pub(crate) fn inject(
        &self,
        operation: ObjectStoreOperation,
        key_prefix: &str,
        skip: u32,
        times: u32,
        action: FaultAction,
    ) {
        self.scripted
            .lock()
            .expect("fault lock is not poisoned")
            .push(ScriptedFault {
                operation,
                key_prefix: key_prefix.to_owned(),
                skip,
                remaining: times,
                action,
                armed_by_put_of: None,
            });
    }

    /// Applies `action` to the first `operation` under `key_prefix` after a put of `put_key`.
    pub(crate) fn inject_after_put(
        &self,
        put_key: &str,
        operation: ObjectStoreOperation,
        key_prefix: &str,
        action: FaultAction,
    ) {
        self.scripted
            .lock()
            .expect("fault lock is not poisoned")
            .push(ScriptedFault {
                operation,
                key_prefix: key_prefix.to_owned(),
                skip: 0,
                remaining: 1,
                action,
                armed_by_put_of: Some(put_key.to_owned()),
            });
    }

    /// Delays every unscripted call by a duration drawn from `delays` with `seed`.
    pub(crate) fn delay_randomly(&self, seed: u64, delays: RandomDelays) {
        *self
            .random_delays
            .lock()
            .expect("fault lock is not poisoned") = Some((ChaCha8Rng::seed_from_u64(seed), delays));
    }

    /// Fails every delete before its effect while `is_failing` is set.
    pub(crate) fn fail_every_delete(&self, is_failing: bool) {
        self.is_failing_every_delete
            .store(is_failing, Ordering::SeqCst);
    }
}

impl FaultInjector for TestFaults {
    fn action(&self, operation: ObjectStoreOperation, key: &str) -> FaultAction {
        if operation == ObjectStoreOperation::Delete
            && self.is_failing_every_delete.load(Ordering::SeqCst)
        {
            return FaultAction::FailBeforeEffect;
        }
        {
            let mut scripted = self.scripted.lock().expect("fault lock is not poisoned");
            for fault in scripted.iter_mut() {
                if operation == ObjectStoreOperation::Put
                    && fault.armed_by_put_of.as_deref() == Some(key)
                {
                    fault.armed_by_put_of = None;
                    continue;
                }
                if fault.armed_by_put_of.is_some()
                    || fault.operation != operation
                    || !key.starts_with(&fault.key_prefix)
                    || fault.remaining == 0
                {
                    continue;
                }
                if fault.skip > 0 {
                    fault.skip -= 1;
                    continue;
                }
                fault.remaining -= 1;
                return fault.action;
            }
        }
        let mut random_delays = self
            .random_delays
            .lock()
            .expect("fault lock is not poisoned");
        let Some((rng, delays)) = random_delays.as_mut() else {
            return FaultAction::Proceed;
        };
        let maximum = if delays.stall_probability > 0.0 && rng.random_bool(delays.stall_probability)
        {
            delays.maximum_stall
        } else {
            delays.maximum_delay
        };
        let maximum_millis = u64::try_from(maximum.as_millis()).expect("the delay fits in u64");
        FaultAction::Delay(Duration::from_millis(rng.random_range(0..=maximum_millis)))
    }
}

/// The physical store under an adapter, and its faults.
pub(crate) struct CreateOnlyStore {
    pub(crate) store: Arc<InMemoryObjectStore>,
    pub(crate) faults: Arc<TestFaults>,
}

impl CreateOnlyStore {
    /// Creates an empty store that rejects ReplaceIfEtagMatches and stamps writes with Tokio time.
    pub(crate) fn new() -> Self {
        Self::with_store_clock(TokioWallClock::new())
    }

    /// Creates an empty store like [`Self::new`] whose write timestamps come from `clock`.
    pub(crate) fn with_store_clock(clock: impl WallClock) -> Self {
        let faults = Arc::new(TestFaults::new());
        let store = Arc::new(InMemoryObjectStore::with_options(
            InMemoryObjectStoreOptions {
                injector: Some(Arc::clone(&faults) as Arc<dyn FaultInjector>),
                clock: Some(Arc::new(clock)),
                rejects_replace_if_etag_matches: true,
            },
        ));
        Self { store, faults }
    }

    /// Creates an empty store like [`Self::new`] whose listings repeat the first listing of each
    /// prefix for `lifetime`, as Tencent COS does; helpers that list then see stale listings too.
    pub(crate) fn with_stale_listings(lifetime: Duration) -> Self {
        Self::with_store_clock_and_stale_listings(TokioWallClock::new(), lifetime)
    }

    /// Creates an empty store like [`Self::with_stale_listings`] whose write timestamps come from
    /// `clock`.
    pub(crate) fn with_store_clock_and_stale_listings(
        clock: impl WallClock,
        lifetime: Duration,
    ) -> Self {
        let faults = Arc::new(TestFaults::new());
        let store = Arc::new(InMemoryObjectStore::with_options_and_stale_listings(
            InMemoryObjectStoreOptions {
                injector: Some(Arc::clone(&faults) as Arc<dyn FaultInjector>),
                clock: Some(Arc::new(clock)),
                rejects_replace_if_etag_matches: true,
            },
            lifetime,
        ));
        Self { store, faults }
    }

    /// Returns the store as the inner store of an adapter.
    pub(crate) fn inner(&self) -> Arc<dyn ObjectStore> {
        Arc::clone(&self.store) as Arc<dyn ObjectStore>
    }

    /// Returns every physical key under `prefix`, reading without faults.
    pub(crate) async fn physical_keys(&self, prefix: &str) -> Vec<String> {
        let mut keys = Vec::new();
        let mut start_after = None;
        loop {
            let listing = self
                .store
                .list(prefix, start_after.as_deref(), 1000)
                .await
                .expect("the physical listing succeeds");
            keys.extend(listing.objects.into_iter().map(|object| object.key));
            match listing.next_start_after {
                Some(next) => start_after = Some(next),
                None => return keys,
            }
        }
    }

    /// Returns the generations stored for `logical_key`, newest first, without its anchor.
    pub(crate) async fn generations(&self, logical_key: &str) -> Vec<u64> {
        let prefix = format!("{logical_key}.v/");
        self.physical_keys(&prefix)
            .await
            .iter()
            .filter(|key| **key != anchor_key(logical_key))
            .map(|key| {
                u64::MAX
                    - key
                        .strip_prefix(&prefix)
                        .expect("the key is under the version prefix")
                        .parse::<u64>()
                        .expect("the version suffix is an inverted generation")
            })
            .collect()
    }

    /// Waits on Tokio's paused clock until `logical_key` stores exactly `expected` generations.
    pub(crate) async fn wait_for_generations(&self, logical_key: &str, expected: &[u64]) {
        for _ in 0..100 {
            if self.generations(logical_key).await == expected {
                return;
            }
            tokio::time::sleep(Duration::from_millis(10)).await;
        }
        assert_eq!(self.generations(logical_key).await, expected);
    }
}

/// Asserts that the versions stored for `logical_key` are consecutive generations, as HEAD
/// probing keeps them once no stale writer is left; reads current objects, not a stale listing.
pub(crate) async fn assert_consecutive_generations(physical: &CreateOnlyStore, logical_key: &str) {
    physical.store.expire_stale_listings();
    let generations = physical.generations(logical_key).await;
    for pair in generations.windows(2) {
        assert_eq!(
            pair[0],
            pair[1] + 1,
            "the versions of {logical_key} have a gap: {generations:?}"
        );
    }
}

/// Returns the physical key of `generation` of `logical_key`.
pub(crate) fn version_key(logical_key: &str, generation: u64) -> String {
    format!("{logical_key}.v/{:020}", u64::MAX - generation)
}

/// Returns the physical key of the anchor of `logical_key`, which HEAD probing writes before it
/// first reclaims generation 1.
pub(crate) fn anchor_key(logical_key: &str) -> String {
    format!("{logical_key}.v/anchor")
}

/// A call on one physical key that waits until the test releases it, as a frozen process does.
pub(crate) struct FrozenCall {
    operation: ObjectStoreOperation,
    physical_key: String,
    reached: Semaphore,
    released: Semaphore,
}

impl FrozenCall {
    /// Waits until the frozen call has started.
    pub(crate) async fn wait_until_reached(&self) {
        self.reached
            .acquire()
            .await
            .expect("the semaphore stays open")
            .forget();
    }

    /// Lets the frozen call continue.
    pub(crate) fn release(&self) {
        self.released.add_permits(1);
    }
}

/// One process's view of the inner store, whose chosen call freezes without Tokio time passing.
pub(crate) struct FreezableStore {
    inner: Arc<dyn ObjectStore>,
    frozen_call: Mutex<Option<Arc<FrozenCall>>>,
}

impl FreezableStore {
    pub(crate) fn new(inner: Arc<dyn ObjectStore>) -> Arc<Self> {
        Arc::new(Self {
            inner,
            frozen_call: Mutex::new(None),
        })
    }

    /// Freezes the next `operation` on `physical_key` until the returned handle releases it.
    pub(crate) fn freeze_next(
        &self,
        operation: ObjectStoreOperation,
        physical_key: &str,
    ) -> Arc<FrozenCall> {
        let frozen = Arc::new(FrozenCall {
            operation,
            physical_key: physical_key.to_owned(),
            reached: Semaphore::new(0),
            released: Semaphore::new(0),
        });
        *self
            .frozen_call
            .lock()
            .expect("freeze lock is not poisoned") = Some(Arc::clone(&frozen));
        frozen
    }

    async fn wait_while_frozen(&self, operation: ObjectStoreOperation, key: &str) {
        let frozen = {
            let mut frozen_call = self
                .frozen_call
                .lock()
                .expect("freeze lock is not poisoned");
            if frozen_call
                .as_ref()
                .is_some_and(|frozen| frozen.operation == operation && frozen.physical_key == key)
            {
                frozen_call.take()
            } else {
                None
            }
        };
        if let Some(frozen) = frozen {
            frozen.reached.add_permits(1);
            frozen
                .released
                .acquire()
                .await
                .expect("the semaphore stays open")
                .forget();
        }
    }
}

#[async_trait]
impl ObjectStore for FreezableStore {
    async fn put(
        &self,
        key: &str,
        body: Bytes,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        self.wait_while_frozen(ObjectStoreOperation::Put, key).await;
        self.inner.put(key, body, condition).await
    }

    async fn put_file(
        &self,
        key: &str,
        path: &Path,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        self.inner.put_file(key, path, condition).await
    }

    async fn get(&self, key: &str) -> ObjectStoreResult<Option<ObjectContent>> {
        self.wait_while_frozen(ObjectStoreOperation::Get, key).await;
        self.inner.get(key).await
    }

    async fn get_range(
        &self,
        key: &str,
        range: ByteRange,
    ) -> ObjectStoreResult<Option<ObjectContent>> {
        self.wait_while_frozen(ObjectStoreOperation::Get, key).await;
        self.inner.get_range(key, range).await
    }

    async fn get_to_file(&self, key: &str, path: &Path) -> ObjectStoreResult<Option<u64>> {
        self.inner.get_to_file(key, path).await
    }

    async fn head(&self, key: &str) -> ObjectStoreResult<Option<ObjectMetadata>> {
        self.wait_while_frozen(ObjectStoreOperation::Head, key)
            .await;
        self.inner.head(key).await
    }

    async fn delete(&self, key: &str) -> ObjectStoreResult<()> {
        self.wait_while_frozen(ObjectStoreOperation::Delete, key)
            .await;
        self.inner.delete(key).await
    }

    async fn delete_many(&self, keys: &[String]) -> ObjectStoreResult<()> {
        self.inner.delete_many(keys).await
    }

    async fn list(
        &self,
        prefix: &str,
        start_after: Option<&str>,
        max_keys: u32,
    ) -> ObjectStoreResult<ObjectListing> {
        self.wait_while_frozen(ObjectStoreOperation::List, prefix)
            .await;
        self.inner.list(prefix, start_after, max_keys).await
    }

    fn supports_replace_if_etag_matches_on_every_key(&self) -> bool {
        self.inner.supports_replace_if_etag_matches_on_every_key()
    }
}

/// Returns the ETag of the object stored at `generation` of `logical_key`, read physically.
pub(crate) async fn physical_version_etag(
    physical: &CreateOnlyStore,
    logical_key: &str,
    generation: u64,
) -> String {
    let body = physical
        .store
        .get(&version_key(logical_key, generation))
        .await
        .expect("get")
        .expect("the version exists")
        .bytes;
    let version_id = u128::from_be_bytes(
        body[6..22]
            .try_into()
            .expect("a version header holds a 16-byte version ID"),
    );
    format!("\"g{generation}-{version_id:032x}\"")
}

/// Returns the generation of an ETag the adapter returned.
pub(crate) fn etag_generation(etag: &str) -> u64 {
    etag.strip_prefix("\"g")
        .and_then(|rest| rest.split_once('-'))
        .and_then(|(generation, _)| generation.parse().ok())
        .expect("the adapter returns g<generation>-<id> ETags")
}
