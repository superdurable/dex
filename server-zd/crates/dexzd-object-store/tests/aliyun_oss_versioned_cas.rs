// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Linearizability of `AliyunOssObjectStore` replacements under races, stale writers,
//! reclamation, and faults, over an in-memory create-only store on Tokio's paused clock.

#[path = "aliyun_oss/create_only_store.rs"]
mod create_only_store;
#[path = "aliyun_oss/replacement_history.rs"]
mod replacement_history;

use std::sync::Arc;
use std::time::Duration;

use bytes::Bytes;
use dexzd_clock::WallClock;
use dexzd_object_store::{
    AliyunOssObjectStore, FaultAction, MANIFEST_OBJECT_KEY, ObjectStore, ObjectStoreErrorKind,
    ObjectStoreOperation, WriteCondition, WriteOutcome,
};

use create_only_store::{
    CreateOnlyStore, RandomDelays, TokioWallClock, etag_generation, version_key,
};
use replacement_history::{ReplacementWorkload, run_replacement_workload};

const KEY: &str = MANIFEST_OBJECT_KEY;
/// Longer than the reclamation age, so the next confirmed write may reclaim older versions.
const RECLAIMABLE_GAP: Duration = Duration::from_secs(61);

async fn create(store: &dyn ObjectStore, body: &'static str) -> String {
    match store
        .put(
            KEY,
            Bytes::from_static(body.as_bytes()),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("create")
    {
        WriteOutcome::Written { etag } => etag,
        WriteOutcome::PreconditionFailed => panic!("the create must succeed"),
    }
}

async fn replace(store: &dyn ObjectStore, etag: &str, body: &'static str) -> String {
    match store
        .put(
            KEY,
            Bytes::from_static(body.as_bytes()),
            WriteCondition::ReplaceIfEtagMatches {
                etag: etag.to_owned(),
            },
        )
        .await
        .expect("replace")
    {
        WriteOutcome::Written { etag } => etag,
        WriteOutcome::PreconditionFailed => panic!("the replacement must succeed"),
    }
}

async fn current_body(store: &dyn ObjectStore) -> Bytes {
    store
        .get(KEY)
        .await
        .expect("get")
        .expect("the key exists")
        .bytes
}

/// Waits on the paused clock until a spawned write has created `generation`.
async fn wait_for_physical_version(physical: &CreateOnlyStore, generation: u64) {
    for _ in 0..1000 {
        if physical
            .store
            .head(&version_key(KEY, generation))
            .await
            .expect("head")
            .is_some()
        {
            return;
        }
        tokio::time::sleep(Duration::from_millis(1)).await;
    }
    panic!("generation {generation} was never created");
}

#[tokio::test(start_paused = true)]
async fn racing_replacements_have_one_winner_per_generation_without_faults() {
    for seed in 0..8 {
        let physical = CreateOnlyStore::new();
        physical
            .faults
            .delay_randomly(seed, RandomDelays::uniform(Duration::from_millis(50)));
        let workload = ReplacementWorkload {
            writers: 8,
            attempts: 20,
            maximum_pause: Duration::ZERO,
        };
        let history = run_replacement_workload(&physical, KEY, workload).await;
        history.assert_linearizable();
        assert!(history.written_count() >= 20, "seed {seed} made progress");
    }
}

#[tokio::test(start_paused = true)]
async fn racing_replacements_stay_linearizable_while_reclamation_races_stalled_writers() {
    for seed in 100..124 {
        let physical = CreateOnlyStore::new();
        // Stalls far beyond the reclamation age let reclamation run under a stalled writer.
        physical.faults.delay_randomly(
            seed,
            RandomDelays {
                maximum_delay: Duration::from_millis(50),
                stall_probability: 0.05,
                maximum_stall: Duration::from_secs(600),
            },
        );
        let workload = ReplacementWorkload {
            writers: 6,
            attempts: 30,
            maximum_pause: Duration::from_secs(20),
        };
        let history = run_replacement_workload(&physical, KEY, workload).await;
        history.assert_linearizable();
        assert!(history.written_count() >= 30, "seed {seed} made progress");
    }
}

#[tokio::test(start_paused = true)]
async fn racing_creates_of_an_absent_key_have_one_winner() {
    let physical = CreateOnlyStore::new();
    physical
        .faults
        .delay_randomly(7, RandomDelays::uniform(Duration::from_millis(20)));
    let store = Arc::new(AliyunOssObjectStore::new(physical.inner()));
    let mut creators = Vec::new();
    for creator in 0..16_u8 {
        let store = Arc::clone(&store);
        let creator_task = tokio::spawn(async move {
            store
                .put(KEY, Bytes::from(vec![creator]), WriteCondition::CreateOnly)
                .await
                .expect("create")
        });
        creators.push(creator_task);
    }
    let mut winners = Vec::new();
    for (creator, task) in creators.into_iter().enumerate() {
        if let WriteOutcome::Written { etag } = task.await.expect("the creator finishes") {
            winners.push((creator, etag));
        }
    }
    assert_eq!(winners.len(), 1, "exactly one create wins: {winners:?}");
    let (creator, etag) = &winners[0];
    let content = store.get(KEY).await.expect("get").expect("exists");
    assert_eq!(content.etag, *etag);
    assert_eq!(
        content.bytes.as_ref(),
        [u8::try_from(*creator).expect("fits")]
    );
}

#[tokio::test(start_paused = true)]
async fn a_stale_replacement_of_a_reclaimed_generation_is_rejected_and_removed() {
    let physical = CreateOnlyStore::new();
    let store = AliyunOssObjectStore::new(physical.inner());
    let first = create(&store, "one").await;
    let second = replace(&store, &first, "two").await;
    let third = replace(&store, &second, "three").await;
    replace(&store, &third, "four").await;
    // Reclamation removed generation 3, so a writer holding the ETag of 2 can recreate it.
    physical
        .store
        .delete(&version_key(KEY, 3))
        .await
        .expect("delete");
    let stale = store
        .put(
            KEY,
            Bytes::from_static(b"stale"),
            WriteCondition::ReplaceIfEtagMatches { etag: second },
        )
        .await
        .expect("the stale replacement completes");
    assert_eq!(stale, WriteOutcome::PreconditionFailed);
    assert_eq!(physical.generations(KEY).await, [4, 2, 1]);
    assert_eq!(current_body(&store).await, Bytes::from_static(b"four"));
}

#[tokio::test(start_paused = true)]
async fn a_stale_first_create_after_reclamation_is_rejected_and_removed() {
    let physical = CreateOnlyStore::new();
    let store = Arc::new(AliyunOssObjectStore::new(physical.inner()));
    // The stale creator lists no version, then its create arrives after everything below.
    physical.faults.inject(
        ObjectStoreOperation::Put,
        &version_key(KEY, 1),
        0,
        1,
        FaultAction::Delay(Duration::from_secs(600)),
    );
    let stale_creator_store = Arc::clone(&store);
    let stale_creator = tokio::spawn(async move {
        stale_creator_store
            .put(
                KEY,
                Bytes::from_static(b"stale"),
                WriteCondition::CreateOnly,
            )
            .await
    });
    tokio::time::sleep(Duration::from_secs(1)).await;
    let first = create(store.as_ref(), "one").await;
    let second = replace(store.as_ref(), &first, "two").await;
    tokio::time::sleep(RECLAIMABLE_GAP).await;
    replace(store.as_ref(), &second, "three").await;
    physical.wait_for_generations(KEY, &[3, 2]).await;
    let outcome = stale_creator
        .await
        .expect("the stale creator finishes")
        .expect("the stale create completes");
    assert_eq!(outcome, WriteOutcome::PreconditionFailed);
    assert_eq!(physical.generations(KEY).await, [3, 2]);
    assert_eq!(
        current_body(store.as_ref()).await,
        Bytes::from_static(b"three")
    );
}

#[tokio::test(start_paused = true)]
async fn a_writer_superseded_before_its_confirmation_still_reports_written() {
    let physical = CreateOnlyStore::new();
    let store = Arc::new(AliyunOssObjectStore::new(physical.inner()));
    let mut etag = create(store.as_ref(), "one").await;
    for body in ["two", "three", "four"] {
        etag = replace(store.as_ref(), &etag, body).await;
    }
    physical.faults.inject_after_put(
        &version_key(KEY, 5),
        ObjectStoreOperation::List,
        "manifest.v/",
        FaultAction::Delay(Duration::from_secs(30)),
    );
    let writer_b_store = Arc::clone(&store);
    let writer_b = tokio::spawn(async move {
        writer_b_store
            .put(
                KEY,
                Bytes::from_static(b"five"),
                WriteCondition::ReplaceIfEtagMatches { etag },
            )
            .await
    });
    wait_for_physical_version(&physical, 5).await;
    let fifth = store.get(KEY).await.expect("get").expect("exists");
    assert_eq!(fifth.bytes, Bytes::from_static(b"five"));
    replace(store.as_ref(), &fifth.etag, "six").await;
    // An aggressive reclamation removes generation 5 before writer B confirms it.
    physical
        .store
        .delete(&version_key(KEY, 5))
        .await
        .expect("delete");
    let outcome = writer_b
        .await
        .expect("writer B finishes")
        .expect("writer B's put completes");
    assert_eq!(
        outcome,
        WriteOutcome::Written {
            etag: fifth.etag.clone()
        }
    );
    assert_eq!(
        current_body(store.as_ref()).await,
        Bytes::from_static(b"six")
    );
}

#[tokio::test(start_paused = true)]
async fn a_superseded_writer_whose_successor_was_reclaimed_reports_an_unknown_outcome() {
    let physical = CreateOnlyStore::new();
    let store = Arc::new(AliyunOssObjectStore::new(physical.inner()));
    let first = create(store.as_ref(), "one").await;
    physical.faults.inject_after_put(
        &version_key(KEY, 2),
        ObjectStoreOperation::List,
        "manifest.v/",
        FaultAction::Delay(Duration::from_secs(30)),
    );
    let writer_store = Arc::clone(&store);
    let writer = tokio::spawn(async move {
        writer_store
            .put(
                KEY,
                Bytes::from_static(b"two"),
                WriteCondition::ReplaceIfEtagMatches { etag: first },
            )
            .await
    });
    wait_for_physical_version(&physical, 2).await;
    let second = store.get(KEY).await.expect("get").expect("exists").etag;
    let third = replace(store.as_ref(), &second, "three").await;
    replace(store.as_ref(), &third, "four").await;
    for generation in [2, 3] {
        physical
            .store
            .delete(&version_key(KEY, generation))
            .await
            .expect("delete");
    }
    let error = writer
        .await
        .expect("the writer finishes")
        .expect_err("the outcome cannot be decided");
    assert_eq!(error.kind, ObjectStoreErrorKind::OutcomeUnknown);
    assert_eq!(error.key, KEY);
}

#[tokio::test(start_paused = true)]
async fn replacements_with_an_etag_that_was_never_current_change_nothing() {
    let physical = CreateOnlyStore::new();
    let store = AliyunOssObjectStore::new(physical.inner());
    let first = create(&store, "one").await;
    let second = replace(&store, &first, "two").await;
    let before = physical.physical_keys("").await;
    let forged_id = format!(
        "\"g{}-{:032x}\"",
        etag_generation(&second),
        0xdead_beef_u128
    );
    let far_ahead = format!("\"g99-{:032x}\"", 0xfeed_u128);
    let uppercase = second.to_uppercase();
    for etag in [forged_id, far_ahead, uppercase, first] {
        assert_eq!(
            store
                .put(
                    KEY,
                    Bytes::from_static(b"forged"),
                    WriteCondition::ReplaceIfEtagMatches { etag }
                )
                .await
                .expect("the replacement completes"),
            WriteOutcome::PreconditionFailed
        );
    }
    assert_eq!(physical.physical_keys("").await, before);
    // An ETag of a reclaimed generation also fails before any write.
    physical
        .store
        .delete(&version_key(KEY, 1))
        .await
        .expect("delete");
    let reclaimed = format!("\"g1-{:032x}\"", 1_u128);
    assert_eq!(
        store
            .put(
                KEY,
                Bytes::from_static(b"late"),
                WriteCondition::ReplaceIfEtagMatches { etag: reclaimed }
            )
            .await
            .expect("the replacement completes"),
        WriteOutcome::PreconditionFailed
    );
    assert_eq!(physical.generations(KEY).await, [2]);
}

#[tokio::test(start_paused = true)]
async fn a_failed_confirmation_reports_an_unknown_outcome_that_callers_reconcile() {
    let physical = CreateOnlyStore::new();
    let store = AliyunOssObjectStore::new(physical.inner());
    let first = create(&store, "one").await;
    physical.faults.inject_after_put(
        &version_key(KEY, 2),
        ObjectStoreOperation::List,
        "manifest.v/",
        FaultAction::FailBeforeEffect,
    );
    let error = store
        .put(
            KEY,
            Bytes::from_static(b"two"),
            WriteCondition::ReplaceIfEtagMatches {
                etag: first.clone(),
            },
        )
        .await
        .expect_err("the confirmation fails");
    assert_eq!(error.kind, ObjectStoreErrorKind::OutcomeUnknown);
    // A caller reads the key back: its proposal landed, and its ETag replaces it.
    let landed = store.get(KEY).await.expect("get").expect("exists");
    assert_eq!(landed.bytes, Bytes::from_static(b"two"));
    replace(&store, &landed.etag, "three").await;

    physical.faults.inject(
        ObjectStoreOperation::Put,
        &version_key(KEY, 4),
        0,
        1,
        FaultAction::ApplyThenOutcomeUnknown,
    );
    let current = store.get(KEY).await.expect("get").expect("exists").etag;
    let error = store
        .put(
            KEY,
            Bytes::from_static(b"four"),
            WriteCondition::ReplaceIfEtagMatches { etag: current },
        )
        .await
        .expect_err("the create response is lost");
    assert_eq!(error.kind, ObjectStoreErrorKind::OutcomeUnknown);
    assert_eq!(current_body(&store).await, Bytes::from_static(b"four"));

    physical.faults.inject(
        ObjectStoreOperation::Put,
        &version_key(KEY, 5),
        0,
        1,
        FaultAction::FailBeforeEffect,
    );
    let current = store.get(KEY).await.expect("get").expect("exists").etag;
    let error = store
        .put(
            KEY,
            Bytes::from_static(b"five"),
            WriteCondition::ReplaceIfEtagMatches {
                etag: current.clone(),
            },
        )
        .await
        .expect_err("the create fails before its effect");
    assert_eq!(error.kind, ObjectStoreErrorKind::Transient);
    assert_eq!(current_body(&store).await, Bytes::from_static(b"four"));
    replace(&store, &current, "five").await;

    // A failed read of the next generation during confirmation is unknown, not lost.
    let current = store.get(KEY).await.expect("get").expect("exists").etag;
    physical.faults.inject_after_put(
        &version_key(KEY, 6),
        ObjectStoreOperation::List,
        "manifest.v/",
        FaultAction::Delay(Duration::from_secs(30)),
    );
    physical.faults.inject(
        ObjectStoreOperation::Get,
        &version_key(KEY, 7),
        0,
        1,
        FaultAction::FailBeforeEffect,
    );
    let error = {
        let first_writer = store.put(
            KEY,
            Bytes::from_static(b"six"),
            WriteCondition::ReplaceIfEtagMatches { etag: current },
        );
        let second_writer = async {
            wait_for_physical_version(&physical, 6).await;
            let sixth = store.get(KEY).await.expect("get").expect("exists").etag;
            replace(&store, &sixth, "seven").await;
        };
        let (outcome, ()) = tokio::join!(first_writer, second_writer);
        outcome.expect_err("the confirmation read fails")
    };
    assert_eq!(error.kind, ObjectStoreErrorKind::OutcomeUnknown);
    assert_eq!(current_body(&store).await, Bytes::from_static(b"seven"));
}

#[tokio::test(start_paused = true)]
async fn reclamation_keeps_two_versions_and_tombstones_after_the_minimum_age() {
    let physical = CreateOnlyStore::new();
    let store = AliyunOssObjectStore::new(physical.inner());
    let mut etag = create(&store, "one").await;
    for body in ["two", "three", "four"] {
        etag = replace(&store, &etag, body).await;
    }
    tokio::time::sleep(Duration::from_millis(100)).await;
    assert_eq!(
        physical.generations(KEY).await,
        [4, 3, 2, 1],
        "versions younger than the minimum age stay"
    );
    for body in ["five", "six"] {
        tokio::time::sleep(RECLAIMABLE_GAP).await;
        etag = replace(&store, &etag, body).await;
    }
    physical.wait_for_generations(KEY, &[6, 5]).await;
    tokio::time::sleep(RECLAIMABLE_GAP).await;
    store.delete(KEY).await.expect("delete");
    for body in ["eight", "nine", "ten", "eleven"] {
        tokio::time::sleep(RECLAIMABLE_GAP).await;
        etag = match store
            .put(
                KEY,
                Bytes::from_static(body.as_bytes()),
                if store.get(KEY).await.expect("get").is_none() {
                    WriteCondition::CreateOnly
                } else {
                    WriteCondition::ReplaceIfEtagMatches { etag: etag.clone() }
                },
            )
            .await
            .expect("write")
        {
            WriteOutcome::Written { etag } => etag,
            WriteOutcome::PreconditionFailed => panic!("the write must succeed"),
        };
    }
    assert_eq!(etag_generation(&etag), 11);
    physical.wait_for_generations(KEY, &[11, 10, 7]).await;
}

#[tokio::test(start_paused = true)]
async fn failed_reclamation_deletes_never_block_later_replacements() {
    let physical = CreateOnlyStore::new();
    let store = AliyunOssObjectStore::new(physical.inner());
    physical.faults.fail_every_delete(true);
    let mut etag = create(&store, "one").await;
    for body in ["two", "three", "four", "five"] {
        tokio::time::sleep(RECLAIMABLE_GAP).await;
        etag = replace(&store, &etag, body).await;
    }
    tokio::time::sleep(Duration::from_millis(100)).await;
    assert_eq!(physical.generations(KEY).await, [5, 4, 3, 2, 1]);
    assert_eq!(current_body(&store).await, Bytes::from_static(b"five"));
    physical.faults.fail_every_delete(false);
    tokio::time::sleep(RECLAIMABLE_GAP).await;
    replace(&store, &etag, "six").await;
    physical.wait_for_generations(KEY, &[6, 5]).await;
}

#[tokio::test(start_paused = true)]
async fn deleting_a_startup_probe_removes_every_version_and_stale_probes_are_removed() {
    let physical = CreateOnlyStore::new();
    let store = AliyunOssObjectStore::new(physical.inner());
    let clock = TokioWallClock::new();
    for key in ["probe/1-1", "probe/2-2"] {
        let WriteOutcome::Written { etag } = store
            .put(
                key,
                Bytes::from_static(b"probe"),
                WriteCondition::CreateOnly,
            )
            .await
            .expect("create")
        else {
            panic!("the probe create must succeed");
        };
        store
            .put(
                key,
                Bytes::from_static(b"replacement"),
                WriteCondition::ReplaceIfEtagMatches { etag },
            )
            .await
            .expect("replace");
    }
    store.delete("probe/2-2").await.expect("delete");
    assert!(physical.physical_keys("probe/2-2").await.is_empty());
    assert_eq!(physical.physical_keys("probe/1-1").await.len(), 2);
    tokio::time::sleep(Duration::from_secs(59 * 60)).await;
    store
        .put(
            "probe/3-3",
            Bytes::from_static(b"probe"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("create");
    assert_eq!(
        store
            .remove_stale_startup_probes(clock.now_unix_micros())
            .await
            .expect("cleanup"),
        0
    );
    tokio::time::sleep(Duration::from_secs(2 * 60)).await;
    assert_eq!(
        store
            .remove_stale_startup_probes(clock.now_unix_micros())
            .await
            .expect("cleanup"),
        1
    );
    let remaining = physical.physical_keys("probe/").await;
    assert_eq!(remaining, [version_key("probe/3-3", 1)]);
}
