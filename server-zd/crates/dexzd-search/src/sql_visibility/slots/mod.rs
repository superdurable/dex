// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

mod backfill;
mod values;

use std::collections::BTreeMap;

use async_trait::async_trait;
use dexzd_protocol::dex::IndexType;
use dexzd_protocol::dexzd::v1::AttributeIndexRegistry;

use super::row::is_system_field;
use crate::SearchError;

pub(super) use backfill::{backfill_attribute_slots, lock_attribute_slots, read_ready_slots};
pub(super) use values::{all_slot_columns, row_slot_values, text_term_hashes};

/// Supplies a current registry snapshot shared by SQL queries and projection.
#[async_trait]
pub trait AttributeIndexRegistryReader: Send + Sync {
    /// Reads registrations, refreshing externally written registry changes.
    async fn read_attribute_index_registry(&self) -> Result<AttributeIndexRegistry, SearchError>;
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
    pub slot_column: String,
}

/// Assigns slots in append-only registration order, excluding every system field.
pub fn assign_attribute_slots(
    registry: &AttributeIndexRegistry,
) -> Result<Vec<AttributeSlot>, SearchError> {
    let mut occupancy = BTreeMap::new();
    let mut assignments = Vec::new();
    for registration in &registry.registrations {
        if is_system_field(&registration.index_key) {
            continue;
        }
        let index_type = IndexType::try_from(registration.index_type).map_err(|_| {
            SearchError::Internal(format!(
                "Attribute index {:?} has unknown type {}",
                registration.index_key, registration.index_type
            ))
        })?;
        let (prefix, capacity) = slot_type(index_type)?;
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
                        && !is_system_field(&entry.index_key)
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
            slot_column: format!("{prefix}_{position:02}"),
        });
    }
    Ok(assignments)
}

fn slot_type(index_type: IndexType) -> Result<(&'static str, usize), SearchError> {
    match index_type {
        IndexType::Keyword => Ok(("keyword", 10)),
        IndexType::Int => Ok(("int", 3)),
        IndexType::Datetime => Ok(("datetime", 3)),
        IndexType::Double => Ok(("double", 3)),
        IndexType::Bool => Ok(("bool", 3)),
        IndexType::KeywordArray => Ok(("keyword_array", 3)),
        IndexType::Text => Ok(("text_terms", 3)),
        IndexType::Unspecified => Err(SearchError::Internal(
            "unspecified Attribute index type in registry".into(),
        )),
    }
}

#[cfg(test)]
mod tests;

#[cfg(test)]
mod integration;
