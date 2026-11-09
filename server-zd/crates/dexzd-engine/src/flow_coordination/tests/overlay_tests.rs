// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::cell::Cell;
use std::collections::{BTreeMap, VecDeque};

use dexzd_apply::{ApplyOutcome, FlowStateReader, StateReadError, StateRow, StateWrite};
use dexzd_keyspace::ColumnFamily;
use dexzd_protocol::dexzd::v1::FlowCommand;

use crate::flow_coordination::speculative_overlay::{
    CommittedMatch, OverlayEntry, SpeculativeOverlay, take_examined_overlay_write_count_for_test,
};
use crate::flow_coordination::tests::model_applier::ok_reply;
use crate::flow_coordination::tests::test_support::{MemoryView, apply_writes_to};

const FLOW: ColumnFamily = ColumnFamily::Flow;

fn key(text: &str) -> Vec<u8> {
    text.as_bytes().to_vec()
}

fn put(text: &str, value: &str) -> StateWrite {
    StateWrite::Put {
        column_family: FLOW,
        key: key(text),
        value: key(value),
    }
}

fn delete(text: &str) -> StateWrite {
    StateWrite::Delete {
        column_family: FLOW,
        key: key(text),
    }
}

fn delete_range(start: &str, end: &str) -> StateWrite {
    StateWrite::DeleteRange {
        column_family: FLOW,
        start: key(start),
        end: key(end),
    }
}

fn entry(command_number: u8, writes: Vec<StateWrite>) -> OverlayEntry {
    OverlayEntry::new(
        &FlowCommand {
            command_id: vec![command_number],
            ..FlowCommand::default()
        },
        ApplyOutcome {
            writes,
            effects: Vec::new(),
            reply: ok_reply(),
            run_id: None,
        },
    )
}

fn committed(rows: &[(&str, &str)]) -> MemoryView {
    MemoryView::from_rows(
        rows.iter()
            .map(|(row_key, value)| ((FLOW, key(row_key)), key(value)))
            .collect::<BTreeMap<_, _>>(),
    )
}

fn texts(rows: Vec<(Vec<u8>, Vec<u8>)>) -> Vec<(String, String)> {
    rows.into_iter()
        .map(|(row_key, value)| {
            (
                String::from_utf8(row_key).unwrap(),
                String::from_utf8(value).unwrap(),
            )
        })
        .collect()
}

fn pairs(rows: &[(&str, &str)]) -> Vec<(String, String)> {
    rows.iter()
        .map(|(row_key, value)| ((*row_key).to_owned(), (*value).to_owned()))
        .collect()
}

#[test]
fn get_returns_newest_overlay_decision_then_committed_value() {
    let base = committed(&[
        ("a", "committed-a"),
        ("b", "committed-b"),
        ("c", "committed-c"),
    ]);
    let mut overlay = SpeculativeOverlay::default();
    overlay.push(entry(1, vec![put("a", "first-a"), delete("b")]));
    overlay.push(entry(2, vec![put("a", "second-a"), put("b", "revived-b")]));
    overlay.push(entry(3, vec![delete_range("c", "d")]));
    let reader = overlay.reader(&base);

    assert_eq!(reader.get(FLOW, b"a").unwrap(), Some(key("second-a")));
    assert_eq!(reader.get(FLOW, b"b").unwrap(), Some(key("revived-b")));
    assert_eq!(reader.get(FLOW, b"c").unwrap(), None);
    assert_eq!(reader.get(FLOW, b"z").unwrap(), None);
    assert_eq!(
        reader.get(ColumnFamily::History, b"a").unwrap(),
        None,
        "writes of another column family are invisible"
    );
}

#[test]
fn put_after_delete_range_in_one_outcome_survives() {
    let base = committed(&[("k1", "old"), ("k2", "old")]);
    let mut overlay = SpeculativeOverlay::default();
    overlay.push(entry(1, vec![delete_range("k", "l"), put("k2", "new")]));
    let reader = overlay.reader(&base);

    assert_eq!(reader.get(FLOW, b"k1").unwrap(), None);
    assert_eq!(reader.get(FLOW, b"k2").unwrap(), Some(key("new")));
    assert_eq!(
        texts(reader.scan(FLOW, b"k", b"l", 10).unwrap()),
        pairs(&[("k2", "new")])
    );
}

#[test]
fn scan_merges_overlay_and_committed_rows_in_key_order() {
    let base = committed(&[("a", "1"), ("c", "3"), ("e", "5"), ("g", "7")]);
    let mut overlay = SpeculativeOverlay::default();
    overlay.push(entry(1, vec![put("b", "2"), put("c", "33"), delete("e")]));
    overlay.push(entry(2, vec![put("f", "6"), put("h", "8")]));
    let reader = overlay.reader(&base);

    assert_eq!(
        texts(reader.scan(FLOW, b"a", b"z", 100).unwrap()),
        pairs(&[
            ("a", "1"),
            ("b", "2"),
            ("c", "33"),
            ("f", "6"),
            ("g", "7"),
            ("h", "8")
        ])
    );
    assert_eq!(
        texts(reader.scan(FLOW, b"b", b"g", 100).unwrap()),
        pairs(&[("b", "2"), ("c", "33"), ("f", "6")])
    );
    assert_eq!(
        texts(reader.scan(FLOW, b"a", b"z", 3).unwrap()),
        pairs(&[("a", "1"), ("b", "2"), ("c", "33")])
    );
    assert!(reader.scan(FLOW, b"a", b"z", 0).unwrap().is_empty());
}

#[test]
fn scan_keeps_paging_committed_rows_when_overlay_deletes_a_whole_page() {
    let rows: Vec<(String, String)> = (0..10)
        .map(|index| (format!("k{index}"), format!("v{index}")))
        .collect();
    let row_refs: Vec<(&str, &str)> = rows
        .iter()
        .map(|(row_key, value)| (row_key.as_str(), value.as_str()))
        .collect();
    let base = committed(&row_refs);
    let mut overlay = SpeculativeOverlay::default();
    overlay.push(entry(1, vec![delete_range("k0", "k6")]));
    let reader = overlay.reader(&base);

    assert_eq!(
        texts(reader.scan(FLOW, b"k", b"l", 3).unwrap()),
        pairs(&[("k6", "v6"), ("k7", "v7"), ("k8", "v8")])
    );
    assert_eq!(
        texts(reader.scan(FLOW, b"k", b"l", 2).unwrap()),
        pairs(&[("k6", "v6"), ("k7", "v7")])
    );
}

#[test]
fn take_committed_matches_only_the_head_entry() {
    let mut overlay = SpeculativeOverlay::default();
    overlay.push(entry(1, vec![put("a", "1")]));
    overlay.push(entry(2, vec![put("b", "2")]));
    let bytes_before = overlay.total_bytes();
    assert!(bytes_before > 0);

    assert!(matches!(
        overlay.take_committed(&[2]),
        CommittedMatch::OutOfOrder
    ));
    assert!(matches!(
        overlay.take_committed(&[9]),
        CommittedMatch::Foreign
    ));
    assert!(matches!(
        overlay.take_committed(&[1]),
        CommittedMatch::Head(_)
    ));
    assert_eq!(overlay.len(), 1);
    assert!(overlay.total_bytes() < bytes_before);
    assert!(matches!(
        overlay.take_committed(&[2]),
        CommittedMatch::Head(_)
    ));
    assert!(overlay.is_empty());
    assert_eq!(overlay.total_bytes(), 0);
}

#[test]
fn committing_the_head_keeps_newer_writes_of_the_same_key() {
    let base = committed(&[("a", "committed-a"), ("b", "committed-b")]);
    let mut overlay = SpeculativeOverlay::default();
    overlay.push(entry(1, vec![put("a", "first-a"), put("b", "first-b")]));
    overlay.push(entry(2, vec![put("a", "second-a")]));

    assert!(matches!(
        overlay.take_committed(&[1]),
        CommittedMatch::Head(_)
    ));
    let reader = overlay.reader(&base);
    assert_eq!(reader.get(FLOW, b"a").unwrap(), Some(key("second-a")));
    assert_eq!(
        reader.get(FLOW, b"b").unwrap(),
        Some(key("committed-b")),
        "a key written only by the committed head falls back to committed state"
    );
    assert_eq!(
        texts(reader.scan(FLOW, b"a", b"z", 10).unwrap()),
        pairs(&[("a", "second-a"), ("b", "committed-b")])
    );
}

#[test]
fn committing_a_range_delete_reveals_committed_rows() {
    let base = committed(&[("k1", "v1"), ("k2", "v2"), ("k3", "v3")]);
    let mut overlay = SpeculativeOverlay::default();
    overlay.push(entry(1, vec![delete_range("k", "l")]));
    overlay.push(entry(2, vec![put("k2", "new")]));
    assert_eq!(
        texts(overlay.reader(&base).scan(FLOW, b"k", b"l", 10).unwrap()),
        pairs(&[("k2", "new")])
    );

    assert!(matches!(
        overlay.take_committed(&[1]),
        CommittedMatch::Head(_)
    ));
    assert_eq!(
        texts(overlay.reader(&base).scan(FLOW, b"k", b"l", 10).unwrap()),
        pairs(&[("k1", "v1"), ("k2", "new"), ("k3", "v3")])
    );
}

#[test]
fn committing_an_entry_that_writes_one_key_twice_clears_it() {
    let base = committed(&[("a", "committed-a")]);
    let mut overlay = SpeculativeOverlay::default();
    overlay.push(entry(
        1,
        vec![
            StateWrite::SingleDelete {
                column_family: FLOW,
                key: key("a"),
            },
            put("a", "rewritten-a"),
        ],
    ));
    assert_eq!(
        overlay.reader(&base).get(FLOW, b"a").unwrap(),
        Some(key("rewritten-a"))
    );

    assert!(matches!(
        overlay.take_committed(&[1]),
        CommittedMatch::Head(_)
    ));
    assert_eq!(
        overlay.reader(&base).get(FLOW, b"a").unwrap(),
        Some(key("committed-a"))
    );
}

#[test]
fn foreign_and_out_of_order_commits_leave_reads_unchanged() {
    let base = committed(&[("a", "committed-a")]);
    let mut overlay = SpeculativeOverlay::default();
    overlay.push(entry(1, vec![put("a", "first-a")]));
    overlay.push(entry(2, vec![put("b", "second-b")]));

    assert!(matches!(
        overlay.take_committed(&[2]),
        CommittedMatch::OutOfOrder
    ));
    assert!(matches!(
        overlay.take_committed(&[9]),
        CommittedMatch::Foreign
    ));
    assert_eq!(
        texts(overlay.reader(&base).scan(FLOW, b"a", b"z", 10).unwrap()),
        pairs(&[("a", "first-a"), ("b", "second-b")])
    );
}

#[test]
fn indexed_reads_match_the_linear_walk_on_random_operations() {
    for seed in 0..24 {
        check_random_operations(seed);
    }
}

#[test]
fn indexed_scan_examines_writes_independent_of_overlay_depth() {
    let shallow = count_scan_work(32);
    let deep = count_scan_work(128);

    assert_eq!(
        shallow.indexed, deep.indexed,
        "rewriting the same keys in more entries must not add indexed work"
    );
    assert!(
        deep.indexed <= 2 * (SCANNED_COMMITTED_ROWS + OVERLAY_DISTINCT_KEYS),
        "indexed work {} is not linear in the scanned rows",
        deep.indexed
    );
    assert!(
        deep.linear > 3 * shallow.linear,
        "linear walk grows with overlay depth: {} versus {}",
        deep.linear,
        shallow.linear
    );
    assert!(
        deep.linear >= 1_000 * deep.indexed,
        "linear walk {} versus indexed {}",
        deep.linear,
        deep.indexed
    );
}

const SCANNED_COMMITTED_ROWS: usize = 4_000;
const OVERLAY_DISTINCT_KEYS: usize = 200;
const OVERLAY_WRITES_PER_ENTRY: usize = 50;

/// Overlay write records examined by one scan, through the index and through the linear walk.
struct ScanWork {
    indexed: usize,
    linear: usize,
}

/// Scans every committed row under `entry_count` overlay entries that rewrite the same keys.
fn count_scan_work(entry_count: usize) -> ScanWork {
    let row_key = |index: usize| format!("row/{index:05}").into_bytes();
    let base = MemoryView::from_rows(
        (0..SCANNED_COMMITTED_ROWS)
            .map(|index| ((FLOW, row_key(index)), b"committed".to_vec()))
            .collect(),
    );
    let key_spacing = SCANNED_COMMITTED_ROWS / OVERLAY_DISTINCT_KEYS;
    let mut overlay = SpeculativeOverlay::default();
    let mut entries = Vec::new();
    for entry_index in 0..entry_count {
        let mut writes: Vec<StateWrite> = (0..OVERLAY_WRITES_PER_ENTRY)
            .map(|write_index| {
                let slot =
                    (entry_index * OVERLAY_WRITES_PER_ENTRY + write_index) % OVERLAY_DISTINCT_KEYS;
                StateWrite::Put {
                    column_family: FLOW,
                    key: row_key(slot * key_spacing),
                    value: format!("entry-{entry_index}").into_bytes(),
                }
            })
            .collect();
        if entry_index == 0 {
            writes.push(StateWrite::DeleteRange {
                column_family: ColumnFamily::History,
                start: b"a".to_vec(),
                end: b"z".to_vec(),
            });
        }
        overlay.push(numbered_entry(
            u64::try_from(entry_index).unwrap(),
            writes.clone(),
        ));
        entries.push(writes);
    }

    let limit = 2 * SCANNED_COMMITTED_ROWS;
    take_examined_overlay_write_count_for_test();
    let indexed_rows = overlay
        .reader(&base)
        .scan(FLOW, b"row/", b"row0", limit)
        .unwrap();
    let indexed = take_examined_overlay_write_count_for_test();
    let linear_reference = LinearOverlayReference::new(entries, &base);
    let linear_rows = linear_reference
        .scan(FLOW, b"row/", b"row0", limit)
        .unwrap();
    assert_eq!(indexed_rows, linear_rows);
    assert_eq!(indexed_rows.len(), SCANNED_COMMITTED_ROWS);
    ScanWork {
        indexed,
        linear: linear_reference.examined_write_count.get(),
    }
}

fn numbered_entry(command_number: u64, writes: Vec<StateWrite>) -> OverlayEntry {
    OverlayEntry::new(
        &FlowCommand {
            command_id: command_number.to_be_bytes().to_vec(),
            ..FlowCommand::default()
        },
        ApplyOutcome {
            writes,
            effects: Vec::new(),
            reply: ok_reply(),
            run_id: None,
        },
    )
}

/// Keys with prefix relations, so scans start and end between related keys.
const RANDOM_KEYS: [&[u8]; 10] = [
    b"", b"k", b"k\0", b"k0", b"k00", b"k01", b"k1", b"ka", b"l", b"m",
];
const RANDOM_FAMILIES: [ColumnFamily; 2] = [ColumnFamily::Flow, ColumnFamily::History];

/// Interleaves random pushes and head commits, checking every read against two oracles.
fn check_random_operations(seed: u64) {
    let mut random = SplitMix64(seed);
    let base_rows: BTreeMap<(ColumnFamily, Vec<u8>), Vec<u8>> = RANDOM_FAMILIES
        .iter()
        .flat_map(|family| RANDOM_KEYS.iter().map(move |row_key| (*family, *row_key)))
        .filter(|_| random.below(2) == 0)
        .map(|(family, row_key)| ((family, row_key.to_vec()), b"committed".to_vec()))
        .collect();
    let base = MemoryView::from_rows(base_rows.clone());
    let mut overlay = SpeculativeOverlay::default();
    let mut pending: VecDeque<(u64, Vec<StateWrite>)> = VecDeque::new();
    let mut next_command_number = 0_u64;

    for step in 0..300 {
        if !pending.is_empty() && random.below(5) < 2 {
            let (command_number, _) = pending.pop_front().unwrap();
            assert!(matches!(
                overlay.take_committed(&command_number.to_be_bytes()),
                CommittedMatch::Head(_)
            ));
        } else {
            let writes = random_writes(&mut random, next_command_number);
            overlay.push(numbered_entry(next_command_number, writes.clone()));
            pending.push_back((next_command_number, writes));
            next_command_number += 1;
        }
        let mut materialized_rows = base_rows.clone();
        for (_, writes) in &pending {
            apply_writes_to(&mut materialized_rows, writes);
        }
        let materialized = MemoryView::from_rows(materialized_rows);
        let linear_reference = LinearOverlayReference::new(
            pending.iter().map(|(_, writes)| writes.clone()).collect(),
            &base,
        );
        let context = format!("seed {seed} step {step}");
        let snapshot = overlay.clone();
        for reader in [overlay.reader(&base), snapshot.reader(&base)] {
            for family in RANDOM_FAMILIES {
                for row_key in RANDOM_KEYS {
                    let actual = reader.get(family, row_key).unwrap();
                    assert_eq!(
                        actual,
                        materialized.get(family, row_key).unwrap(),
                        "{context}"
                    );
                    assert_eq!(
                        actual,
                        linear_reference.get(family, row_key).unwrap(),
                        "{context}"
                    );
                }
            }
            for _ in 0..12 {
                let family = RANDOM_FAMILIES[random.below(RANDOM_FAMILIES.len())];
                let start = RANDOM_KEYS[random.below(RANDOM_KEYS.len())];
                let end = RANDOM_KEYS[random.below(RANDOM_KEYS.len())];
                let limit = [0, 1, 2, 3, 100][random.below(5)];
                let actual = reader.scan(family, start, end, limit).unwrap();
                let materialized_scan = if start < end {
                    materialized.scan(family, start, end, limit).unwrap()
                } else {
                    Vec::new()
                };
                assert_eq!(
                    actual, materialized_scan,
                    "{context} scan {start:?}..{end:?} limit {limit}"
                );
                assert_eq!(
                    actual,
                    linear_reference.scan(family, start, end, limit).unwrap(),
                    "{context} scan {start:?}..{end:?} limit {limit}"
                );
            }
        }
    }
}

fn random_writes(random: &mut SplitMix64, command_number: u64) -> Vec<StateWrite> {
    (0..random.below(6))
        .map(|write_index| {
            let column_family = RANDOM_FAMILIES[random.below(RANDOM_FAMILIES.len())];
            let row_key = RANDOM_KEYS[random.below(RANDOM_KEYS.len())].to_vec();
            match random.below(20) {
                0..=10 => StateWrite::Put {
                    column_family,
                    key: row_key,
                    value: format!("c{command_number}w{write_index}").into_bytes(),
                },
                11..=14 => StateWrite::Delete {
                    column_family,
                    key: row_key,
                },
                15..=16 => StateWrite::SingleDelete {
                    column_family,
                    key: row_key,
                },
                _ => StateWrite::DeleteRange {
                    column_family,
                    start: row_key,
                    end: RANDOM_KEYS[random.below(RANDOM_KEYS.len())].to_vec(),
                },
            }
        })
        .collect()
}

/// Seeded generator for the randomized tests, so failures reproduce from the seed.
struct SplitMix64(u64);

impl SplitMix64 {
    fn below(&mut self, bound: usize) -> usize {
        self.0 = self.0.wrapping_add(0x9E37_79B9_7F4A_7C15);
        let mut mixed = self.0;
        mixed = (mixed ^ (mixed >> 30)).wrapping_mul(0xBF58_476D_1CE4_E5B9);
        mixed = (mixed ^ (mixed >> 27)).wrapping_mul(0x94D0_49BB_1331_11EB);
        mixed ^= mixed >> 31;
        usize::try_from(mixed % u64::try_from(bound).unwrap()).unwrap()
    }
}

/// The overlay read path before the key index: each lookup walks every write, newest first.
struct LinearOverlayReference<'a> {
    entries: Vec<Vec<StateWrite>>,
    committed: &'a MemoryView,
    examined_write_count: Cell<usize>,
}

enum LinearResolution<'a> {
    Untouched,
    Present(&'a Vec<u8>),
    Deleted,
}

impl<'a> LinearOverlayReference<'a> {
    fn new(entries: Vec<Vec<StateWrite>>, committed: &'a MemoryView) -> Self {
        Self {
            entries,
            committed,
            examined_write_count: Cell::new(0),
        }
    }

    fn resolve(&self, column_family: ColumnFamily, key: &[u8]) -> LinearResolution<'_> {
        for writes in self.entries.iter().rev() {
            for write in writes.iter().rev() {
                self.examined_write_count
                    .set(self.examined_write_count.get() + 1);
                match write {
                    StateWrite::Put {
                        column_family: family,
                        key: written,
                        value,
                    } if *family == column_family && written.as_slice() == key => {
                        return LinearResolution::Present(value);
                    }
                    StateWrite::Delete {
                        column_family: family,
                        key: written,
                    }
                    | StateWrite::SingleDelete {
                        column_family: family,
                        key: written,
                    } if *family == column_family && written.as_slice() == key => {
                        return LinearResolution::Deleted;
                    }
                    StateWrite::DeleteRange {
                        column_family: family,
                        start,
                        end,
                    } if *family == column_family
                        && key >= start.as_slice()
                        && key < end.as_slice() =>
                    {
                        return LinearResolution::Deleted;
                    }
                    _ => {}
                }
            }
        }
        LinearResolution::Untouched
    }

    fn present_rows(
        &self,
        column_family: ColumnFamily,
        start: &[u8],
        end: &[u8],
    ) -> BTreeMap<Vec<u8>, Vec<u8>> {
        let mut rows = BTreeMap::new();
        for writes in &self.entries {
            for write in writes {
                if let StateWrite::Put {
                    column_family: family,
                    key,
                    ..
                } = write
                    && *family == column_family
                    && key.as_slice() >= start
                    && key.as_slice() < end
                    && !rows.contains_key(key)
                    && let LinearResolution::Present(value) = self.resolve(column_family, key)
                {
                    rows.insert(key.clone(), value.clone());
                }
            }
        }
        rows
    }
}

impl FlowStateReader for LinearOverlayReference<'_> {
    fn get(
        &self,
        column_family: ColumnFamily,
        key: &[u8],
    ) -> Result<Option<Vec<u8>>, StateReadError> {
        match self.resolve(column_family, key) {
            LinearResolution::Present(value) => Ok(Some(value.clone())),
            LinearResolution::Deleted => Ok(None),
            LinearResolution::Untouched => self.committed.get(column_family, key),
        }
    }

    fn scan(
        &self,
        column_family: ColumnFamily,
        start: &[u8],
        end: &[u8],
        limit: usize,
    ) -> Result<Vec<StateRow>, StateReadError> {
        let mut rows = Vec::new();
        if limit == 0 || start >= end {
            return Ok(rows);
        }
        let mut overlay_rows = self
            .present_rows(column_family, start, end)
            .into_iter()
            .peekable();
        let mut page_start = start.to_vec();
        let mut is_committed_exhausted = false;
        while rows.len() < limit && !is_committed_exhausted {
            let page = self
                .committed
                .scan(column_family, &page_start, end, limit)?;
            is_committed_exhausted = page.len() < limit;
            for (key, value) in page {
                page_start.clone_from(&key);
                page_start.push(0);
                while rows.len() < limit
                    && overlay_rows
                        .peek()
                        .is_some_and(|(overlay_key, _)| *overlay_key < key)
                {
                    rows.push(overlay_rows.next().unwrap());
                }
                if rows.len() >= limit {
                    break;
                }
                if matches!(
                    self.resolve(column_family, &key),
                    LinearResolution::Untouched
                ) {
                    rows.push((key, value));
                }
            }
        }
        while rows.len() < limit {
            let Some(row) = overlay_rows.next() else {
                break;
            };
            rows.push(row);
        }
        Ok(rows)
    }
}
