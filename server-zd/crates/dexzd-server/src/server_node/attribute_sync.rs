// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeMap;
use std::sync::Arc;
use std::time::Duration;

use dexzd_attribute_store::{
    AttributeMapTableSettings, AttributeStore, AttributeStoreError, AttributeStoreFailureScope,
    AttributeStoreSettings, ColumnNaming, FencingToken,
};
use dexzd_clock::WallClock;
use dexzd_engine::{
    AttributeSyncConfig, AttributeSyncDependencies, AttributeSyncFence, AttributeSyncItem,
    AttributeSyncProjector, AttributeSyncProjectorHandle, AttributeSyncWaker,
    AttributeSyncWriteError, AttributeSyncWriter, EffectRuntime, EngineNode,
};
use dexzd_keyspace::PartitionHasher;
use futures::future::BoxFuture;
use tokio::task::JoinHandle;
use tokio_util::sync::CancellationToken;

use crate::ServerError;
use crate::config::{AttributeStoreColumnNaming, AttributeStoreConfig};

/// Every configured Attribute Store, connected and checked before the engine starts.
pub(crate) struct AttributeStores {
    config: AttributeSyncConfig,
    stores: Arc<PostgresAttributeStores>,
}

/// What the projector needs once the engine serves.
pub(crate) struct AttributeSyncStart<'a> {
    pub(crate) engine: &'a EngineNode,
    pub(crate) node_id: &'a str,
    pub(crate) runtime: Arc<dyn EffectRuntime>,
    pub(crate) partition_hasher: PartitionHasher,
    pub(crate) clock: Arc<dyn WallClock>,
    pub(crate) metrics: Arc<dexzd_metrics::NodeMetrics>,
    pub(crate) waker: AttributeSyncWaker,
}

/// The running projector and schema refreshers; only the lease owner may write, so stop it first.
pub(crate) struct AttributeSyncService {
    projector: AttributeSyncProjectorHandle,
    stop_schema_refresh: CancellationToken,
    schema_refreshers: Vec<JoinHandle<()>>,
}

struct PostgresAttributeStores {
    by_name: BTreeMap<String, Arc<AttributeStore>>,
}

impl AttributeStores {
    /// Connects to every store and validates its tables and fencing table; any failure fails startup.
    ///
    /// Returns `None` when no store is configured.
    pub(crate) async fn connect(
        attribute_store: &AttributeStoreConfig,
        sync_config: Option<&AttributeSyncConfig>,
    ) -> Result<Option<Self>, ServerError> {
        let Some(sync_config) = sync_config else {
            return Ok(None);
        };
        let mut by_name = BTreeMap::new();
        for (store_name, store) in &attribute_store.stores {
            let settings = AttributeStoreSettings {
                name: store_name.clone(),
                dsn: store.dsn.clone(),
                table_name: store.table_name.clone(),
                row_key: (!store.row_key.is_empty()).then(|| store.row_key.clone()),
                column_naming: match store.column_naming {
                    AttributeStoreColumnNaming::Exact => ColumnNaming::AsIs,
                    AttributeStoreColumnNaming::SnakeCase => ColumnNaming::SnakeCase,
                },
                attribute_maps: store
                    .attribute_maps
                    .iter()
                    .map(|(map_name, map_table)| {
                        (
                            map_name.clone(),
                            AttributeMapTableSettings {
                                table_name: map_table.table_name.clone(),
                                instance_key_column: map_table.instance_key_column.clone(),
                            },
                        )
                    })
                    .collect(),
                fencing_table_name: store.fencing_table_name.clone(),
                schema_sync_interval: attribute_store.effective_schema_sync_interval(),
                transaction_timeout: sync_config.transaction_timeout,
            };
            let connected = AttributeStore::connect_and_validate(settings)
                .await
                .map_err(|source| ServerError::AttributeStore { source })?;
            tracing::info!(
                store_name,
                "connected to the Attribute Store and checked its tables"
            );
            by_name.insert(store_name.clone(), Arc::new(connected));
        }
        Ok(Some(Self {
            config: sync_config.clone(),
            stores: Arc::new(PostgresAttributeStores { by_name }),
        }))
    }

    /// Starts the schema refreshers and the projector under the engine's lease and fence.
    pub(crate) fn start(self, start: AttributeSyncStart<'_>) -> AttributeSyncService {
        let stop_schema_refresh = CancellationToken::new();
        let schema_refreshers = self
            .stores
            .by_name
            .values()
            .map(|store| store.spawn_schema_refresher(stop_schema_refresh.clone()))
            .collect();
        let fence = AttributeSyncFence::for_node_owner(
            start.engine.cluster_id(),
            start.node_id,
            start.engine.node_epoch(),
        );
        tracing::info!(
            cluster_id = %fence.cluster_id,
            scope = %fence.scope,
            fencing_token = fence.token,
            transaction_timeout_ms = u64::try_from(self.config.transaction_timeout.as_millis()).unwrap_or(u64::MAX),
            "starting Attribute Store synchronization"
        );
        let projector = AttributeSyncProjector::start(
            &self.config,
            AttributeSyncDependencies {
                writer: self.stores,
                runtime: start.runtime,
                partition_hasher: start.partition_hasher,
                clock: start.clock,
                metrics: start.metrics,
                fence,
                lease_status: start.engine.lease_status(),
                waker: start.waker,
            },
            start.engine.wait_for_stop(),
        );
        AttributeSyncService {
            projector,
            stop_schema_refresh,
            schema_refreshers,
        }
    }
}

impl AttributeSyncService {
    /// Stops the projector, abandoning its in-flight write after `budget`, then the schema refreshers.
    ///
    /// Returns false when the projector had to be aborted.
    pub(crate) async fn shutdown(self, budget: Duration) -> bool {
        let has_stopped = self.projector.shutdown(budget).await;
        self.stop_schema_refresh.cancel();
        for refresher in self.schema_refreshers {
            refresher.abort();
            if let Err(error) = refresher.await
                && !error.is_cancelled()
            {
                tracing::warn!(%error, "an Attribute Store schema refresher failed");
            }
        }
        has_stopped
    }
}

impl AttributeSyncWriter for PostgresAttributeStores {
    fn write_flow_attributes<'a>(
        &'a self,
        store_name: &'a str,
        flow_id: &'a str,
        items: &'a [AttributeSyncItem],
        fence: &'a AttributeSyncFence,
    ) -> BoxFuture<'a, Result<(), AttributeSyncWriteError>> {
        Box::pin(async move {
            let Some(store) = self.by_name.get(store_name) else {
                return Err(AttributeSyncWriteError::Rejected {
                    detail: format!("Attribute Store {store_name:?} is not configured"),
                });
            };
            let items: Vec<dexzd_attribute_store::AttributeSyncItem> = items
                .iter()
                .map(|item| dexzd_attribute_store::AttributeSyncItem {
                    key: item.key.clone(),
                    value: item.value.clone(),
                })
                .collect();
            let fencing_token = FencingToken {
                cluster_id: fence.cluster_id.clone(),
                scope: fence.scope.clone(),
                token: fence.token,
            };
            store
                .write_batch(flow_id, &items, &fencing_token)
                .await
                .map_err(write_error)
        })
    }
}

fn write_error(error: AttributeStoreError) -> AttributeSyncWriteError {
    let detail = error.to_string();
    if matches!(error, AttributeStoreError::Fenced { .. }) {
        AttributeSyncWriteError::Fenced { detail }
    } else if !error.is_retryable() {
        AttributeSyncWriteError::Rejected { detail }
    } else {
        match error.failure_scope() {
            AttributeStoreFailureScope::Store => AttributeSyncWriteError::Retryable { detail },
            AttributeStoreFailureScope::Batch => {
                AttributeSyncWriteError::RetryableForRun { detail }
            }
        }
    }
}
