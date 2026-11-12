// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use super::AzureGcsCloud;
use super::version_token::version_token;
use crate::{
    ObjectListing, ObjectMetadata, ObjectStoreError, ObjectStoreErrorKind, ObjectStoreOperation,
    ObjectStoreResult,
};

/// Validates and collects the objects of one [`crate::ObjectStore::list`] call.
pub(super) struct ListingAccumulator<'a> {
    cloud: AzureGcsCloud,
    key_prefix: &'a str,
    prefix: &'a str,
    full_prefix: &'a str,
    pub(super) start_after: Option<String>,
    objects: Vec<ObjectMetadata>,
    previous_full_key: Option<String>,
}

impl<'a> ListingAccumulator<'a> {
    pub(super) fn new(
        cloud: AzureGcsCloud,
        key_prefix: &'a str,
        prefix: &'a str,
        full_prefix: &'a str,
        start_after: Option<String>,
    ) -> Self {
        Self {
            cloud,
            key_prefix,
            prefix,
            full_prefix,
            start_after,
            objects: Vec::new(),
            previous_full_key: None,
        }
    }

    pub(super) fn remaining(&self, max_keys: u32) -> usize {
        (max_keys as usize)
            .saturating_sub(self.objects.len())
            .max(1)
    }

    pub(super) fn is_empty(&self) -> bool {
        self.objects.is_empty()
    }

    pub(super) fn is_full(&self, max_keys: u32) -> bool {
        self.objects.len() >= max_keys as usize
    }

    /// Adds a listed object, rejecting results that are out of order or outside the prefix.
    pub(super) fn accept(
        &mut self,
        full_key: &str,
        size: u64,
        e_tag: Option<&str>,
        generation: Option<&str>,
        last_modified_unix_micros: Option<i64>,
    ) -> ObjectStoreResult<()> {
        if self.start_after.as_deref() == Some(full_key) {
            return Ok(());
        }
        let is_in_order = self
            .start_after
            .as_deref()
            .is_none_or(|after| full_key > after)
            && self
                .previous_full_key
                .as_deref()
                .is_none_or(|previous| full_key > previous);
        if !is_in_order || !full_key.starts_with(self.full_prefix) {
            return Err(ObjectStoreError::new(
                ObjectStoreOperation::List,
                self.prefix,
                ObjectStoreErrorKind::Permanent,
                format!(
                    "listing returned {full_key:?} out of ascending key order or outside the prefix"
                ),
            ));
        }
        let key = full_key
            .strip_prefix(self.key_prefix)
            .expect("the full prefix starts with the key prefix");
        let etag = version_token(
            self.cloud,
            e_tag,
            generation,
            ObjectStoreOperation::List,
            self.prefix,
        )?;
        self.objects.push(ObjectMetadata {
            key: key.to_owned(),
            size,
            etag,
            last_modified_unix_micros,
        });
        self.previous_full_key = Some(full_key.to_owned());
        Ok(())
    }

    /// Returns the page; `has_more` sets the cursor to the last returned key.
    pub(super) fn finish(self, has_more: bool) -> ObjectListing {
        let next_start_after = has_more
            .then(|| self.objects.last().map(|object| object.key.clone()))
            .flatten();
        ObjectListing {
            objects: self.objects,
            next_start_after,
        }
    }
}
