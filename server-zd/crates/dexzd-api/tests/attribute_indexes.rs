// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeMap;
use std::sync::Arc;

use dexzd_api::AttributeIndexes;
use dexzd_clock::SystemWallClock;
use dexzd_cluster_metadata::{AttributeIndexCapacity, read_attribute_index_registry};
use dexzd_keyspace::TenantId;
use dexzd_object_store::InMemoryObjectStore;
use dexzd_protocol::dex::{
    ErrorSubStatus, IndexType, ServiceErrorResponse, SyncAttributeIndexRequest,
};
use prost::Message;
use tonic::Code;

#[tokio::test]
async fn capacity_maps_to_failed_precondition_and_dex_sub_status() {
    let store = Arc::new(InMemoryObjectStore::new());
    let indexes = AttributeIndexes::new(
        store.clone(),
        Arc::new(SystemWallClock),
        Some(AttributeIndexCapacity::postgres()),
    );
    let request = SyncAttributeIndexRequest {
        attribute_indexes: (0..10)
            .map(|index| (format!("key-{index}"), IndexType::Keyword as i32))
            .collect(),
    };
    indexes
        .sync_attribute_indexes(TenantId(7), &request)
        .await
        .unwrap();
    let registry = read_attribute_index_registry(store.as_ref()).await.unwrap();
    indexes
        .sync_attribute_indexes(TenantId(7), &request)
        .await
        .unwrap();
    let error = indexes
        .sync_attribute_indexes(
            TenantId(7),
            &SyncAttributeIndexRequest {
                attribute_indexes: BTreeMap::from([(
                    "extra".to_owned(),
                    IndexType::Keyword as i32,
                )]),
            },
        )
        .await
        .unwrap_err()
        .into_status();
    assert_eq!(error.code(), Code::FailedPrecondition);
    let envelope = StatusDetails::decode(error.details()).unwrap();
    let details = ServiceErrorResponse::decode(envelope.details[0].value.as_slice()).unwrap();
    assert_eq!(
        details.sub_status,
        ErrorSubStatus::AttributeIndexCapacityExceeded as i32
    );
    assert!(error.message().contains("tenant 7"));
    assert!(error.message().contains("10"));
    assert!(error.message().contains("key-0"));
    assert_eq!(
        registry,
        read_attribute_index_registry(store.as_ref()).await.unwrap()
    );
}

#[tokio::test]
async fn no_capacity_allows_more_than_postgres_slots() {
    let indexes = AttributeIndexes::new(
        Arc::new(InMemoryObjectStore::new()),
        Arc::new(SystemWallClock),
        None,
    );
    indexes
        .sync_attribute_indexes(
            TenantId(7),
            &SyncAttributeIndexRequest {
                attribute_indexes: (0..11)
                    .map(|index| (format!("key-{index}"), IndexType::Keyword as i32))
                    .collect(),
            },
        )
        .await
        .unwrap();
}

#[derive(Clone, PartialEq, Message)]
struct StatusDetails {
    #[prost(message, repeated, tag = "3")]
    details: Vec<prost_types::Any>,
}

#[tokio::test]
async fn background_refresh_reuses_request_snapshots_and_discovers_external_registrations() {
    use dexzd_api::AttributeIndexRegistryRefresh;
    use dexzd_search::sql_visibility::AttributeIndexRegistryReader;
    use dexzd_visibility_query::parse_and_check;
    let store = Arc::new(InMemoryObjectStore::new());
    let indexes = AttributeIndexes::new_with_registry_refresh(
        store.clone(),
        Arc::new(SystemWallClock),
        None,
        AttributeIndexRegistryRefresh::ExternalRegistrations,
    );
    indexes
        .sync_attribute_indexes(
            TenantId(7),
            &SyncAttributeIndexRequest {
                attribute_indexes: BTreeMap::from([("first".into(), IndexType::Int as i32)]),
            },
        )
        .await
        .unwrap();
    let external = AttributeIndexes::new(store, Arc::new(SystemWallClock), None);
    external
        .sync_attribute_indexes(
            TenantId(7),
            &SyncAttributeIndexRequest {
                attribute_indexes: BTreeMap::from([("second".into(), IndexType::Int as i32)]),
            },
        )
        .await
        .unwrap();
    let cached = indexes.field_registry(TenantId(7)).await.unwrap();
    assert!(parse_and_check("second = 2", &cached).is_err());
    indexes.refresh_attribute_index_registry().await.unwrap();
    let refreshed = indexes.field_registry(TenantId(7)).await.unwrap();
    assert!(parse_and_check("second = 2", &refreshed).is_ok());
}
