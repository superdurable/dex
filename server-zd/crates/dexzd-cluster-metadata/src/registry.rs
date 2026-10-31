// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::{BTreeMap, BTreeSet};

use bytes::Bytes;
use dexzd_clock::WallClock;
use dexzd_object_store::{ObjectStore, ObjectStoreErrorKind, WriteCondition, WriteOutcome};
use dexzd_protocol::dex::IndexType;
use dexzd_protocol::dexzd::v1::{AttributeIndexRegistration, AttributeIndexRegistry};
use prost::Message;

use crate::MetadataError;

const REGISTRY_KEY: &str = "registry/attribute-indexes";

/// Maximum distinct Attribute index keys per tenant and type.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct AttributeIndexCapacity {
    maximum_keys_by_type: BTreeMap<IndexType, usize>,
}

impl AttributeIndexCapacity {
    /// Uses PostgreSQL's ten Keyword slots and three slots for every other supported type.
    pub fn postgres() -> Self {
        Self {
            maximum_keys_by_type: [
                (IndexType::Keyword, 10),
                (IndexType::Int, 3),
                (IndexType::Double, 3),
                (IndexType::Bool, 3),
                (IndexType::Datetime, 3),
                (IndexType::KeywordArray, 3),
                (IndexType::Text, 3),
            ]
            .into_iter()
            .collect(),
        }
    }
}

/// One attribute index declaration supplied by an application.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct AttributeIndexDeclaration {
    /// Index key within a tenant.
    pub index_key: String,
    /// Numeric value of the public Dex IndexType enum.
    pub index_type: i32,
}

/// Reads the current attribute index registry.
pub async fn read_attribute_index_registry(
    store: &dyn ObjectStore,
) -> Result<AttributeIndexRegistry, MetadataError> {
    match store.get(REGISTRY_KEY).await? {
        Some(content) => {
            AttributeIndexRegistry::decode(content.bytes).map_err(|source| MetadataError::Decode {
                key: REGISTRY_KEY,
                source,
            })
        }
        None => Ok(AttributeIndexRegistry::default()),
    }
}

/// Registers new keys and rejects a type change for an existing tenant key.
pub async fn register_attribute_indexes(
    store: &dyn ObjectStore,
    clock: &dyn WallClock,
    tenant_id: u64,
    declarations: &[AttributeIndexDeclaration],
    capacity: Option<&AttributeIndexCapacity>,
) -> Result<AttributeIndexRegistry, MetadataError> {
    loop {
        let current_object = store.get(REGISTRY_KEY).await?;
        let current = match &current_object {
            Some(content) => {
                AttributeIndexRegistry::decode(content.bytes.clone()).map_err(|source| {
                    MetadataError::Decode {
                        key: REGISTRY_KEY,
                        source,
                    }
                })?
            }
            None => AttributeIndexRegistry::default(),
        };
        let mut proposed = current.clone();
        for declaration in declarations {
            if let Some(existing) = proposed.registrations.iter().find(|existing| {
                existing.tenant_id == tenant_id && existing.index_key == declaration.index_key
            }) {
                if existing.index_type != declaration.index_type {
                    return Err(MetadataError::AttributeIndexConflict {
                        tenant_id,
                        index_key: declaration.index_key.clone(),
                        existing_type: existing.index_type,
                        requested_type: declaration.index_type,
                    });
                }
                continue;
            }
            proposed.registrations.push(AttributeIndexRegistration {
                tenant_id,
                index_key: declaration.index_key.clone(),
                index_type: declaration.index_type,
                registered_at_unix_micros: clock.now_unix_micros(),
            });
        }
        if proposed == current {
            return Ok(current);
        }
        if let Some(capacity) = capacity {
            validate_added_attribute_indexes(&current, &proposed, tenant_id, capacity)?;
        }
        proposed.version = current
            .version
            .checked_add(1)
            .ok_or(MetadataError::Overflow {
                field: "attribute index registry version",
            })?;
        let condition = current_object.map_or(WriteCondition::CreateOnly, |content| {
            WriteCondition::ReplaceIfEtagMatches { etag: content.etag }
        });
        let result = store
            .put(
                REGISTRY_KEY,
                Bytes::from(proposed.encode_to_vec()),
                condition,
            )
            .await;
        match result {
            Ok(WriteOutcome::Written { .. }) => return Ok(proposed),
            Ok(WriteOutcome::PreconditionFailed) => {
                if read_attribute_index_registry(store).await? == proposed {
                    return Ok(proposed);
                }
            }
            Err(error) if error.kind == ObjectStoreErrorKind::OutcomeUnknown => {
                if read_attribute_index_registry(store).await? == proposed {
                    return Ok(proposed);
                }
            }
            Err(error) => return Err(error.into()),
        }
    }
}

/// Checks existing registry occupancy, including registries loaded during startup.
pub fn validate_attribute_index_capacity(
    registry: &AttributeIndexRegistry,
    capacity: &AttributeIndexCapacity,
) -> Result<(), MetadataError> {
    let mut keys_by_type: BTreeMap<(u64, IndexType), BTreeSet<&str>> = BTreeMap::new();
    for registration in &registry.registrations {
        let index_type = stored_index_type(registration)?;
        keys_by_type
            .entry((registration.tenant_id, index_type))
            .or_default()
            .insert(&registration.index_key);
    }
    for ((tenant_id, index_type), keys) in keys_by_type {
        if let Some(&limit) = capacity.maximum_keys_by_type.get(&index_type)
            && keys.len() > limit
        {
            return Err(MetadataError::AttributeIndexCapacityExceeded {
                tenant_id,
                index_type,
                limit,
                registered_keys: keys.into_iter().map(str::to_owned).collect(),
                rejected_keys: Vec::new(),
            });
        }
    }
    Ok(())
}

fn validate_added_attribute_indexes(
    current: &AttributeIndexRegistry,
    proposed: &AttributeIndexRegistry,
    tenant_id: u64,
    capacity: &AttributeIndexCapacity,
) -> Result<(), MetadataError> {
    let mut added_keys_by_type: BTreeMap<IndexType, BTreeSet<&str>> = BTreeMap::new();
    for registration in &proposed.registrations[current.registrations.len()..] {
        added_keys_by_type
            .entry(stored_index_type(registration)?)
            .or_default()
            .insert(&registration.index_key);
    }
    for (index_type, added_keys) in added_keys_by_type {
        let Some(&limit) = capacity.maximum_keys_by_type.get(&index_type) else {
            continue;
        };
        let registered_keys: BTreeSet<_> = current
            .registrations
            .iter()
            .filter(|registration| {
                registration.tenant_id == tenant_id && registration.index_type == index_type as i32
            })
            .map(|registration| registration.index_key.as_str())
            .collect();
        if registered_keys.len() + added_keys.len() > limit {
            return Err(MetadataError::AttributeIndexCapacityExceeded {
                tenant_id,
                index_type,
                limit,
                registered_keys: registered_keys.into_iter().map(str::to_owned).collect(),
                rejected_keys: added_keys.into_iter().map(str::to_owned).collect(),
            });
        }
    }
    Ok(())
}

fn stored_index_type(
    registration: &AttributeIndexRegistration,
) -> Result<IndexType, MetadataError> {
    IndexType::try_from(registration.index_type).map_err(|_| MetadataError::InvalidMetadata {
        key: REGISTRY_KEY,
        detail: format!(
            "attribute index {:?} has stored type {}",
            registration.index_key, registration.index_type
        ),
    })
}
