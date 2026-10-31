// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::Arc;

use async_trait::async_trait;
use dexzd_clock::WallClock;
use dexzd_cluster_metadata::{
    AttributeIndexCapacity, AttributeIndexDeclaration, MetadataError,
    read_attribute_index_registry, register_attribute_indexes,
};
use dexzd_keyspace::TenantId;
use dexzd_object_store::ObjectStore;
use dexzd_protocol::dex::{ErrorSubStatus, IndexType, SyncAttributeIndexRequest};
use dexzd_protocol::dexzd::v1::AttributeIndexRegistry;
use dexzd_search::SearchError;
use dexzd_search::sql_visibility::AttributeIndexRegistryReader;
use dexzd_visibility_query::FieldRegistry;
use tokio::sync::Mutex;

use crate::FlowOperationError;

/// Determines whether background projection may refresh externally registered Attribute indexes.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum AttributeIndexRegistryRefresh {
    /// Reuses the registry loaded locally and updated by registration requests.
    LocalRegistrations,
    /// Allows the SQL projection's background refresh to discover other nodes' registrations.
    ExternalRegistrations,
}

/// Registers Attribute index declarations and supplies them to search.
///
/// Caches the registry; each registration replaces the cache.
pub struct AttributeIndexes {
    object_store: Arc<dyn ObjectStore>,
    clock: Arc<dyn WallClock>,
    capacity: Option<AttributeIndexCapacity>,
    cached_registry: Mutex<Option<AttributeIndexRegistry>>,
    registry_refresh: AttributeIndexRegistryRefresh,
}

impl AttributeIndexes {
    /// Creates a registry client that reads and writes `object_store`.
    pub fn new(
        object_store: Arc<dyn ObjectStore>,
        clock: Arc<dyn WallClock>,
        capacity: Option<AttributeIndexCapacity>,
    ) -> Self {
        Self::new_with_registry_refresh(
            object_store,
            clock,
            capacity,
            AttributeIndexRegistryRefresh::LocalRegistrations,
        )
    }

    /// Creates a registry client with an explicit policy for background refreshes.
    pub fn new_with_registry_refresh(
        object_store: Arc<dyn ObjectStore>,
        clock: Arc<dyn WallClock>,
        capacity: Option<AttributeIndexCapacity>,
        registry_refresh: AttributeIndexRegistryRefresh,
    ) -> Self {
        Self {
            object_store,
            clock,
            capacity,
            cached_registry: Mutex::new(None),
            registry_refresh,
        }
    }

    /// Validates and registers the declarations of `request` for `tenant_id`.
    ///
    /// Returns `InvalidArgument` for an empty name or unknown type, `FailedPrecondition` when a
    /// name is registered with another type or capacity is exhausted, and `Unavailable` when object storage fails.
    pub async fn sync_attribute_indexes(
        &self,
        tenant_id: TenantId,
        request: &SyncAttributeIndexRequest,
    ) -> Result<(), FlowOperationError> {
        let mut declarations = Vec::with_capacity(request.attribute_indexes.len());
        for (index_key, index_type) in &request.attribute_indexes {
            if index_key.trim().is_empty() {
                return Err(FlowOperationError::invalid_argument(
                    "attribute index name must not be empty",
                ));
            }
            let is_known_type = *index_type > IndexType::Unspecified as i32
                && *index_type <= IndexType::Datetime as i32;
            if !is_known_type {
                return Err(FlowOperationError::invalid_argument(format!(
                    "attribute index {index_key:?} has invalid type {index_type}"
                )));
            }
            declarations.push(AttributeIndexDeclaration {
                index_key: index_key.clone(),
                index_type: *index_type,
            });
        }
        let registry = register_attribute_indexes(
            self.object_store.as_ref(),
            self.clock.as_ref(),
            tenant_id.0,
            &declarations,
            self.capacity.as_ref(),
        )
        .await
        .map_err(registry_error)?;
        self.cache_registry(registry).await;
        Ok(())
    }

    /// Returns the registered application fields of `tenant_id` for query type checking.
    ///
    /// Returns `Unavailable` when the registry cannot be read.
    pub async fn field_registry(
        &self,
        tenant_id: TenantId,
    ) -> Result<FieldRegistry, FlowOperationError> {
        let registry = self.registry_snapshot().await.map_err(registry_error)?;
        let mut fields = FieldRegistry::new();
        for registration in registry
            .registrations
            .iter()
            .filter(|registration| registration.tenant_id == tenant_id.0)
        {
            let index_type = IndexType::try_from(registration.index_type).map_err(|_| {
                FlowOperationError::Internal {
                    detail: format!(
                        "attribute index {:?} has stored type {}",
                        registration.index_key, registration.index_type
                    ),
                }
            })?;
            fields.insert(registration.index_key.clone(), index_type);
        }
        Ok(fields)
    }
    async fn registry_snapshot(&self) -> Result<AttributeIndexRegistry, MetadataError> {
        if let Some(registry) = self.cached_registry.lock().await.as_ref() {
            return Ok(registry.clone());
        }
        let registry = read_attribute_index_registry(self.object_store.as_ref()).await?;
        Ok(self.cache_registry(registry).await)
    }

    async fn cache_registry(&self, registry: AttributeIndexRegistry) -> AttributeIndexRegistry {
        let mut cached = self.cached_registry.lock().await;
        if cached
            .as_ref()
            .is_none_or(|existing| existing.version < registry.version)
        {
            *cached = Some(registry);
        }
        cached
            .as_ref()
            .expect("the registry was loaded before caching")
            .clone()
    }
}

fn registry_error(error: MetadataError) -> FlowOperationError {
    match error {
        MetadataError::AttributeIndexConflict { .. } => {
            FlowOperationError::failed_precondition(error.to_string())
        }
        MetadataError::AttributeIndexCapacityExceeded { .. } => FlowOperationError::Rejected {
            code: tonic::Code::FailedPrecondition,
            sub_status: ErrorSubStatus::AttributeIndexCapacityExceeded,
            message: error.to_string(),
        },
        MetadataError::Store(_) => FlowOperationError::Unavailable {
            detail: error.to_string(),
        },
        other => FlowOperationError::Internal {
            detail: other.to_string(),
        },
    }
}

#[async_trait]
impl AttributeIndexRegistryReader for AttributeIndexes {
    async fn read_attribute_index_registry(&self) -> Result<AttributeIndexRegistry, SearchError> {
        self.registry_snapshot().await.map_err(|error| {
            SearchError::Unavailable(format!("read Attribute index registry: {error}"))
        })
    }

    async fn refresh_attribute_index_registry(
        &self,
    ) -> Result<AttributeIndexRegistry, SearchError> {
        if self.registry_refresh == AttributeIndexRegistryRefresh::LocalRegistrations {
            return self.read_attribute_index_registry().await;
        }
        let registry = read_attribute_index_registry(self.object_store.as_ref())
            .await
            .map_err(|error| {
                SearchError::Unavailable(format!("refresh Attribute index registry: {error}"))
            })?;
        Ok(self.cache_registry(registry).await)
    }
}
