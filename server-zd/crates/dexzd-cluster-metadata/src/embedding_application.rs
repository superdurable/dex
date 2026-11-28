// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::ops::RangeInclusive;

use dexzd_keyspace::EMBEDDING_APPLICATION_KEY_LAYOUT_VERSIONS;
use dexzd_protocol::dexzd::v1::Manifest;

use crate::MetadataError;

/// Identity of an application that embeds server-zd and keeps its own state in the cluster.
///
/// The cluster object records `name` and `key_layout_version` when the application creates the
/// cluster. A node opens the cluster only when both match, so server-zd and the application never
/// open each other's clusters. The manifest's version barrier records the application's apply
/// version, which must lie in `supported_versions` before and after a node takes the lease.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct EmbeddingApplication {
    /// Name recorded as the cluster's creating application.
    /// No default; it must not be empty.
    pub name: String,
    /// Key layout version of the application's state.
    /// No default; it must lie in [`EMBEDDING_APPLICATION_KEY_LAYOUT_VERSIONS`].
    pub key_layout_version: u32,
    /// Application apply versions this binary can apply.
    /// No default; a non-empty range starting at 1 or later.
    pub supported_versions: RangeInclusive<u32>,
    /// Application apply version this binary writes, recorded when it creates the cluster.
    /// No default; it must lie in `supported_versions`.
    pub current_version: u32,
}

impl EmbeddingApplication {
    /// Checks the identity's own rules before a node uses it.
    ///
    /// Returns [`MetadataError::InvalidConfig`] when `name` is empty, `key_layout_version` lies
    /// outside [`EMBEDDING_APPLICATION_KEY_LAYOUT_VERSIONS`], `supported_versions` is empty or
    /// starts at 0, or `current_version` lies outside `supported_versions`.
    pub fn validate(&self) -> Result<(), MetadataError> {
        let invalid = |detail: String| MetadataError::InvalidConfig {
            field: "embedding application",
            detail,
        };
        let name = &self.name;
        if name.is_empty() {
            return Err(invalid("the application name is empty".to_owned()));
        }
        if !EMBEDDING_APPLICATION_KEY_LAYOUT_VERSIONS.contains(&self.key_layout_version) {
            return Err(invalid(format!(
                "key layout version {} of application {name:?} is below the reserved range \
                 starting at {}",
                self.key_layout_version,
                EMBEDDING_APPLICATION_KEY_LAYOUT_VERSIONS.start()
            )));
        }
        if self.supported_versions.is_empty() || *self.supported_versions.start() == 0 {
            return Err(invalid(format!(
                "supported versions {:?} of application {name:?} must be a non-empty range \
                 starting at 1 or later",
                self.supported_versions
            )));
        }
        if !self.supported_versions.contains(&self.current_version) {
            return Err(invalid(format!(
                "current version {} of application {name:?} is outside its supported versions \
                 {:?}",
                self.current_version, self.supported_versions
            )));
        }
        Ok(())
    }

    /// Checks the application version that the manifest's version barrier records.
    ///
    /// Returns the recorded version, or [`MetadataError::UnsupportedApplicationVersion`] when it
    /// lies outside `supported_versions`; a manifest without a version barrier records 0.
    pub fn check_recorded_version(&self, manifest: &Manifest) -> Result<u32, MetadataError> {
        let recorded = manifest
            .version_barrier
            .as_ref()
            .map_or(0, |barrier| barrier.application_version);
        if self.supported_versions.contains(&recorded) {
            return Ok(recorded);
        }
        Err(MetadataError::UnsupportedApplicationVersion {
            application_name: self.name.clone(),
            recorded,
            oldest_supported: *self.supported_versions.start(),
            newest_supported: *self.supported_versions.end(),
        })
    }
}
