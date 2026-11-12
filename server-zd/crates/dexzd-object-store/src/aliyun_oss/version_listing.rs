// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Listings by logical key: versions collapse into their newest, and deleted keys stay hidden.

use std::collections::BTreeMap;

use crate::aliyun_oss::version_keys::{
    is_reserved_physical_key, may_list_versioned_keys, versioned_logical_key,
};
use crate::aliyun_oss::version_reads::head_versioned;
use crate::{
    ObjectListing, ObjectMetadata, ObjectStore, ObjectStoreError, ObjectStoreErrorKind,
    ObjectStoreOperation, ObjectStoreResult, is_mutable_object_key,
};

/// Keys read per inner listing request.
const PHYSICAL_PAGE_KEYS: u32 = 1000;

/// One logical key found in a physical listing.
enum ListedKey {
    /// A plain object, with its metadata.
    Plain(ObjectMetadata),
    /// A mutable object key with at least one version; its newest version is read later.
    Versioned,
}

/// Lists logical keys; a prefix that may hold versioned keys reads all its later objects per page.
pub(super) async fn list_logical_keys(
    inner: &dyn ObjectStore,
    prefix: &str,
    start_after: Option<&str>,
    max_keys: u32,
) -> ObjectStoreResult<ObjectListing> {
    if !(1..=1000).contains(&max_keys) {
        return Err(ObjectStoreError::new(
            ObjectStoreOperation::List,
            prefix,
            ObjectStoreErrorKind::Permanent,
            "max_keys must be 1..=1000",
        ));
    }
    if !may_list_versioned_keys(prefix) {
        return inner.list(prefix, start_after, max_keys).await;
    }
    let listed_keys = list_physical_keys(inner, prefix, start_after).await?;
    let mut objects = Vec::new();
    let mut has_more_keys = false;
    for (logical_key, listed_key) in listed_keys {
        let metadata = match listed_key {
            ListedKey::Plain(metadata) => Some(metadata),
            ListedKey::Versioned => {
                head_versioned(inner, ObjectStoreOperation::List, &logical_key).await?
            }
        };
        let Some(metadata) = metadata else {
            continue;
        };
        if objects.len() == max_keys as usize {
            has_more_keys = true;
            break;
        }
        objects.push(metadata);
    }
    let next_start_after = if has_more_keys {
        objects.last().map(|object| object.key.clone())
    } else {
        None
    };
    Ok(ObjectListing {
        objects,
        next_start_after,
    })
}

/// Reads every physical object under `prefix` after `start_after`; `K.v/...` sorts after `K`.
async fn list_physical_keys(
    inner: &dyn ObjectStore,
    prefix: &str,
    start_after: Option<&str>,
) -> ObjectStoreResult<BTreeMap<String, ListedKey>> {
    let mut listed_keys = BTreeMap::new();
    let mut physical_start_after = start_after.map(str::to_owned);
    loop {
        let page = inner
            .list(prefix, physical_start_after.as_deref(), PHYSICAL_PAGE_KEYS)
            .await?;
        for object in page.objects {
            let is_after_start = |key: &str| start_after.is_none_or(|start| key > start);
            if let Some(logical_key) = versioned_logical_key(&object.key) {
                if logical_key.starts_with(prefix) && is_after_start(logical_key) {
                    listed_keys
                        .entry(logical_key.to_owned())
                        .or_insert(ListedKey::Versioned);
                }
            } else if !is_mutable_object_key(&object.key) && !is_reserved_physical_key(&object.key)
            {
                // Objects stored at a mutable key itself are invisible, as they are to reads.
                listed_keys.insert(object.key.clone(), ListedKey::Plain(object));
            }
        }
        match page.next_start_after {
            Some(next) => physical_start_after = Some(next),
            None => return Ok(listed_keys),
        }
    }
}
