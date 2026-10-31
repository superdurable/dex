// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

pub(super) mod backfill;
mod oversized_documents;
mod registry;
mod slot_column;
pub(super) mod values;

use std::collections::BTreeMap;

use async_trait::async_trait;
use dexzd_protocol::dex::IndexType;
use dexzd_protocol::dexzd::v1::AttributeIndexRegistry;

use crate::SearchError;
use dexzd_protocol::attribute_index::is_system_search_field;

pub(super) use backfill::backfill_next_batch;
pub(super) use registry::{
    AttributeSlotSnapshot, initialize_attribute_slots, load_attribute_slot_snapshot,
    load_committed_attribute_slot_snapshot, lock_attribute_slots_shared,
    read_slot_registry_version,
};
pub use slot_column::AttributeSlotColumn;
use slot_column::slot_type;
pub(super) use values::{all_slot_columns, slot_column_expressions, text_term_hashes};

/// Supplies a current registry snapshot shared by SQL queries and projection.
#[async_trait]
pub trait AttributeIndexRegistryReader: Send + Sync {
    /// Returns the shared registry snapshot, loading it when no snapshot is cached.
    async fn read_attribute_index_registry(&self) -> Result<AttributeIndexRegistry, SearchError>;
    /// Refreshes external registrations outside request and database-lock paths.
    async fn refresh_attribute_index_registry(
        &self,
    ) -> Result<AttributeIndexRegistry, SearchError> {
        self.read_attribute_index_registry().await
    }
}

/// Stable typed column assigned to one registered application Attribute.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct AttributeSlot {
    /// Tenant owning the registration.
    pub tenant_id: u64,
    /// Registered Attribute index key.
    pub index_key: String,
    /// Immutable registered type.
    pub index_type: IndexType,
    /// Column selected in registration order within this tenant and type.
    pub slot_column: AttributeSlotColumn,
}

/// Assigns slots in append-only registration order, excluding every system field.
pub fn assign_attribute_slots(
    registry: &AttributeIndexRegistry,
) -> Result<Vec<AttributeSlot>, SearchError> {
    let mut occupancy = BTreeMap::new();
    let mut assignments = Vec::new();
    for registration in &registry.registrations {
        if is_system_search_field(&registration.index_key) {
            continue;
        }
        let index_type = IndexType::try_from(registration.index_type).map_err(|_| {
            SearchError::Internal(format!(
                "Attribute index {:?} has unknown type {}",
                registration.index_key, registration.index_type
            ))
        })?;
        let (_, capacity, _) = slot_type(index_type)?;
        let position = occupancy
            .entry((registration.tenant_id, index_type))
            .or_insert(0);
        *position += 1;
        if *position > capacity {
            let keys = registry
                .registrations
                .iter()
                .filter(|entry| {
                    entry.tenant_id == registration.tenant_id
                        && entry.index_type == registration.index_type
                        && !is_system_search_field(&entry.index_key)
                })
                .map(|entry| entry.index_key.as_str())
                .collect::<Vec<_>>();
            return Err(SearchError::Internal(format!(
                "PostgreSQL Attribute index capacity exceeded for tenant {} type {index_type:?}: maximum {capacity}, registered keys {keys:?}",
                registration.tenant_id
            )));
        }
        assignments.push(AttributeSlot {
            tenant_id: registration.tenant_id,
            index_key: registration.index_key.clone(),
            index_type,
            slot_column: AttributeSlotColumn::for_registration(index_type, *position)?,
        });
    }
    Ok(assignments)
}

#[cfg(test)]
mod tests;

#[cfg(test)]
mod integration;
