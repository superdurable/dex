// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Physical keys: generation `g` of key `K` lives at `K.v/<u64::MAX - g>`, newest first.
//!
//! The crate README proves the mapping injective.

use crate::{
    MUTABLE_OBJECT_KEY_PREFIXES, MUTABLE_OBJECT_KEYS, ObjectStoreError, ObjectStoreErrorKind,
    ObjectStoreOperation, ObjectStoreResult, is_mutable_object_key,
};

/// Separates a logical key from its version suffix; no logical mutable key contains it.
const VERSION_KEY_SEPARATOR: &str = ".v/";

/// Digits of `u64::MAX`, the width of an inverted generation.
const INVERTED_GENERATION_DIGITS: usize = 20;

/// Bytes that a physical version key adds to its logical mutable key.
///
/// A caller that limits key lengths adds this to the logical key when the store is an
/// [`crate::AliyunOssObjectStore`].
pub const VERSION_OBJECT_KEY_SUFFIX_BYTES: usize =
    VERSION_KEY_SEPARATOR.len() + INVERTED_GENERATION_DIGITS;

/// How the adapter stores a logical key.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(super) enum LogicalKeyLayout {
    /// The physical key equals the logical key.
    Plain,
    /// The logical key is a mutable object key stored as create-only versions.
    Versioned,
}

/// Returns how `key` is stored; rejects keys that would make the physical layout ambiguous.
pub(super) fn logical_key_layout(
    operation: ObjectStoreOperation,
    key: &str,
) -> ObjectStoreResult<LogicalKeyLayout> {
    if is_mutable_object_key(key) {
        if key.contains(VERSION_KEY_SEPARATOR) {
            return Err(ObjectStoreError::new(
                operation,
                key,
                ObjectStoreErrorKind::Permanent,
                "a mutable object key must not contain \".v/\"",
            ));
        }
        return Ok(LogicalKeyLayout::Versioned);
    }
    if is_reserved_physical_key(key) {
        return Err(ObjectStoreError::new(
            operation,
            key,
            ObjectStoreErrorKind::Permanent,
            "the key is reserved for the versions of a mutable object key",
        ));
    }
    Ok(LogicalKeyLayout::Plain)
}

/// Returns the logical key whose version is stored at `physical_key`, if it names a version.
pub(super) fn versioned_logical_key(physical_key: &str) -> Option<&str> {
    // A version suffix holds no ".v/", so the last occurrence is the separator.
    let separator = physical_key.rfind(VERSION_KEY_SEPARATOR)?;
    let logical_key = &physical_key[..separator];
    (is_mutable_object_key(logical_key) && !logical_key.contains(VERSION_KEY_SEPARATOR))
        .then_some(logical_key)
}

/// Returns whether `key` lies under the version prefix of some mutable object key.
pub(super) fn is_reserved_physical_key(key: &str) -> bool {
    key.match_indices(VERSION_KEY_SEPARATOR)
        .any(|(separator, _)| is_mutable_object_key(&key[..separator]))
}

/// Returns whether a listing of `prefix` may include versioned keys; false positives only slow it.
pub(super) fn may_list_versioned_keys(prefix: &str) -> bool {
    MUTABLE_OBJECT_KEYS
        .iter()
        .chain(MUTABLE_OBJECT_KEY_PREFIXES.iter())
        .any(|key| key.starts_with(prefix) || prefix.starts_with(key))
}

/// Returns the prefix that every version of `logical_key` starts with.
pub(super) fn version_prefix(logical_key: &str) -> String {
    format!("{logical_key}{VERSION_KEY_SEPARATOR}")
}

/// Returns the physical key of generation `generation` (at least 1) of `logical_key`.
pub(super) fn version_object_key(logical_key: &str, generation: u64) -> String {
    format!(
        "{logical_key}{VERSION_KEY_SEPARATOR}{:0width$}",
        u64::MAX - generation,
        width = INVERTED_GENERATION_DIGITS
    )
}

/// Returns the generation of a well-formed version key under `prefix`, or `None`.
pub(super) fn version_generation(physical_key: &str, prefix: &str) -> Option<u64> {
    let digits = physical_key.strip_prefix(prefix)?;
    if digits.len() != INVERTED_GENERATION_DIGITS
        || !digits.bytes().all(|byte| byte.is_ascii_digit())
    {
        return None;
    }
    let inverted_generation: u64 = digits.parse().ok()?;
    let generation = u64::MAX - inverted_generation;
    (generation >= 1).then_some(generation)
}

/// Returns the error for a version key under `prefix` that [`version_generation`] rejects.
pub(super) fn malformed_version_key(
    operation: ObjectStoreOperation,
    logical_key: &str,
    physical_key: &str,
) -> ObjectStoreError {
    ObjectStoreError::new(
        operation,
        logical_key,
        ObjectStoreErrorKind::Corrupt,
        format!("version object key {physical_key:?} is malformed"),
    )
}
