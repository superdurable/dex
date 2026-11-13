// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The structure that HEAD probing keeps for each versioned key: consecutive first versions that
//! name their predecessors, and an anchor once generation 1 is gone.

use std::collections::BTreeMap;
use std::fmt;

use dexzd_object_store::{
    ByteRange, ObjectMetadata, ObjectStore, S3ObjectStore, STARTUP_PROBE_OBJECT_KEY_PREFIX,
};

const VERSION_SEPARATOR: &str = ".v/";
const ANCHOR_SUFFIX: &str = "anchor";
const INVERTED_GENERATION_DIGITS: usize = 20;
const HEADER_MAGIC: &[u8; 4] = b"DXZV";
const HEADER_LENGTH: u64 = 38;
const PREDECESSOR_FLAG: u8 = 0b10;

/// Counts and every structural violation of a layout check.
#[derive(Debug, Default)]
pub(crate) struct LayoutReport {
    pub(crate) keys: usize,
    pub(crate) versions: usize,
    pub(crate) anchored_keys: usize,
    pub(crate) violations: Vec<String>,
}

impl fmt::Display for LayoutReport {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(
            formatter,
            "{} versioned keys with {} versions, {} anchored after their first generations went; {} violations",
            self.keys,
            self.versions,
            self.anchored_keys,
            self.violations.len()
        )
    }
}

/// The objects listed under one versioned key's prefix.
#[derive(Default)]
struct ListedVersions {
    generations: Vec<u64>,
    has_anchor: bool,
    others: Vec<String>,
}

/// One version header: its ID, and the predecessor's ID when it names one.
struct VersionIdentity {
    version_id: u128,
    predecessor_id: Option<u128>,
}

/// Checks every versioned key except startup probes; `objects` is a listing of the prefix, and
/// every key of `required_keys` must be among them.
///
/// A listing may lag, so HEAD requests decide which generations exist.
pub(crate) async fn check_versioned_layout(
    store: &S3ObjectStore,
    objects: &[ObjectMetadata],
    required_keys: &[String],
) -> LayoutReport {
    let mut listed: BTreeMap<String, ListedVersions> = BTreeMap::new();
    for object in objects {
        let Some(separator) = object.key.rfind(VERSION_SEPARATOR) else {
            continue;
        };
        let (logical_key, suffix) = (
            &object.key[..separator],
            &object.key[separator + VERSION_SEPARATOR.len()..],
        );
        if logical_key.starts_with(STARTUP_PROBE_OBJECT_KEY_PREFIX) {
            continue;
        }
        let entry = listed.entry(logical_key.to_owned()).or_default();
        if suffix == ANCHOR_SUFFIX {
            entry.has_anchor = true;
        } else if let Some(generation) = generation_of(suffix) {
            entry.generations.push(generation);
        } else {
            entry.others.push(object.key.clone());
        }
    }
    let mut report = LayoutReport::default();
    for required_key in required_keys {
        if !listed.contains_key(required_key) {
            report.violations.push(format!(
                "{required_key}: the soak's processes wrote this key, but no version of it is listed"
            ));
        }
    }
    for (logical_key, versions) in listed {
        report.keys += 1;
        for other in &versions.others {
            report
                .violations
                .push(format!("{logical_key}: unexpected object {other}"));
        }
        check_key(store, &logical_key, versions, &mut report).await;
    }
    report
}

async fn check_key(
    store: &S3ObjectStore,
    logical_key: &str,
    versions: ListedVersions,
    report: &mut LayoutReport,
) {
    let mut generations = versions.generations;
    generations.sort_unstable();
    generations.dedup();
    let mut present = Vec::new();
    for generation in generations {
        match store.head(&version_key(logical_key, generation)).await {
            Ok(Some(_)) => present.push(generation),
            Ok(None) => {}
            Err(error) => {
                report.violations.push(format!(
                    "{logical_key}: HEAD of generation {generation} failed: {error}"
                ));
                return;
            }
        }
    }
    present.sort_unstable();
    let Some((&oldest, &newest)) = present.first().zip(present.last()) else {
        report.violations.push(format!(
            "{logical_key}: listed, but no listed generation exists"
        ));
        return;
    };
    // A lagging listing can miss the newest generations.
    let mut newest = newest;
    loop {
        match store.head(&version_key(logical_key, newest + 1)).await {
            Ok(Some(_)) => {
                newest += 1;
                present.push(newest);
            }
            Ok(None) => break,
            Err(error) => {
                report.violations.push(format!(
                    "{logical_key}: HEAD of generation {} failed: {error}",
                    newest + 1
                ));
                return;
            }
        }
    }
    report.versions += present.len();
    if present.len() as u64 != newest - oldest + 1 {
        report.violations.push(format!(
            "{logical_key}: generations {oldest} to {newest} have gaps: {present:?}"
        ));
    }
    let has_anchor = versions.has_anchor
        || match store.head(&anchor_key(logical_key)).await {
            Ok(anchor) => anchor.is_some(),
            Err(error) => {
                report
                    .violations
                    .push(format!("{logical_key}: HEAD of the anchor failed: {error}"));
                return;
            }
        };
    if oldest > 1 {
        if has_anchor {
            report.anchored_keys += 1;
        } else {
            report.violations.push(format!(
                "{logical_key}: generation 1 is gone, but the key has no anchor"
            ));
        }
    }
    let mut previous: Option<(u64, VersionIdentity)> = None;
    for generation in present {
        let identity = match read_identity(store, logical_key, generation).await {
            Ok(identity) => identity,
            Err(failure) => {
                report.violations.push(failure);
                return;
            }
        };
        match &previous {
            None if generation == 1 && identity.predecessor_id.is_some() => {
                report
                    .violations
                    .push(format!("{logical_key}: generation 1 names a predecessor"));
            }
            Some((previous_generation, previous_identity))
                if *previous_generation + 1 == generation
                    && identity.predecessor_id != Some(previous_identity.version_id) =>
            {
                report.violations.push(format!(
                    "{logical_key}: generation {generation} does not name generation \
                     {previous_generation} as its predecessor, so one of them was recreated"
                ));
            }
            _ => {}
        }
        previous = Some((generation, identity));
    }
}

async fn read_identity(
    store: &S3ObjectStore,
    logical_key: &str,
    generation: u64,
) -> Result<VersionIdentity, String> {
    let key = version_key(logical_key, generation);
    let content = store
        .get_range(
            &key,
            ByteRange {
                start: 0,
                end_inclusive: HEADER_LENGTH - 1,
            },
        )
        .await
        .map_err(|error| format!("{key}: reading the header failed: {error}"))?
        .ok_or_else(|| format!("{key}: the version disappeared during the check"))?;
    let header = content.bytes.as_ref();
    if header.len() < HEADER_LENGTH as usize || &header[..4] != HEADER_MAGIC {
        return Err(format!("{key}: the body has no version header"));
    }
    let id = |range: std::ops::Range<usize>| {
        u128::from_be_bytes(
            header[range]
                .try_into()
                .expect("a 16-byte slice converts to a 16-byte array"),
        )
    };
    Ok(VersionIdentity {
        version_id: id(6..22),
        predecessor_id: (header[5] & PREDECESSOR_FLAG != 0).then(|| id(22..38)),
    })
}

fn generation_of(suffix: &str) -> Option<u64> {
    if suffix.len() != INVERTED_GENERATION_DIGITS || !suffix.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    suffix
        .parse::<u64>()
        .ok()
        .map(|inverted| u64::MAX - inverted)
}

fn version_key(logical_key: &str, generation: u64) -> String {
    format!(
        "{logical_key}{VERSION_SEPARATOR}{:0width$}",
        u64::MAX - generation,
        width = INVERTED_GENERATION_DIGITS
    )
}

fn anchor_key(logical_key: &str) -> String {
    format!("{logical_key}{VERSION_SEPARATOR}{ANCHOR_SUFFIX}")
}
