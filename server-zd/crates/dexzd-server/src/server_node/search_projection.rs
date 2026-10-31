// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

mod committed_search_frames;
mod projection_repairs;
mod projection_worker;

use std::future::Future;
use std::sync::Arc;
use std::time::Duration;

use dexzd_engine::{CommandCommitter, NodeStopReason};
use dexzd_metrics::NodeMetrics;
use dexzd_postgres_connection::ConnectionSettings;
use dexzd_search::SearchBackend;
use dexzd_search::sql_visibility::{
    AttributeIndexRegistryReader, ProjectionVisibilityWait, SqlVisibilityProjector,
    SqlVisibilitySearchBackend, VisibilityDialect,
};
#[cfg(test)]
use tokio::sync::mpsc;
use tokio::task::JoinHandle;
use tokio_util::sync::CancellationToken;

use crate::ServerError;
use crate::config::{ConfigError, SearchBackend as SearchBackendKind, SearchConfig};

pub(crate) use committed_search_frames::CommittedSearchFrames;

use projection_worker::SearchProjectionWorker;

/// Startup waits for another node's schema migration, which holds its lock for at most 5 s.
const CONNECT_ATTEMPTS: u32 = 3;
const CONNECT_RETRY_DELAY: Duration = Duration::from_secs(1);
const DEFAULT_SCHEMA: &str = "public";

/// The SQL visibility SearchFlows backend and the committed frames that its projection consumes.
pub(crate) struct SqlVisibilitySearch {
    backend: Arc<SqlVisibilitySearchBackend>,
    frames: Arc<CommittedSearchFrames>,
}

impl SqlVisibilitySearch {
    /// Connects the pool and ensures the schema; call it before the engine replays the log.
    pub(crate) async fn connect(
        search: &SearchConfig,
        registry_reader: Arc<dyn AttributeIndexRegistryReader>,
        metrics: Arc<NodeMetrics>,
    ) -> Result<Self, ServerError> {
        let configuration = postgres_configuration(search)?;
        let is_postgres = search.backend == SearchBackendKind::Postgres;
        let dialect = if is_postgres {
            VisibilityDialect::Postgres
        } else {
            VisibilityDialect::ParadeDb
        };
        if is_postgres {
            let registry = registry_reader
                .read_attribute_index_registry()
                .await
                .map_err(|source| ServerError::SearchBackend { source })?;
            dexzd_search::sql_visibility::assign_attribute_slots(&registry)
                .map_err(|source| ServerError::SearchBackend { source })?;
            dexzd_cluster_metadata::validate_attribute_index_capacity(
                &registry,
                &dexzd_cluster_metadata::AttributeIndexCapacity::postgres(),
            )
            .map_err(|error| ServerError::SearchBackend {
                source: dexzd_search::SearchError::Internal(error.to_string()),
            })?;
        }
        let pool_size = usize::try_from(if is_postgres {
            search.postgres_connection_pool_size
        } else {
            search.parade_db_connection_pool_size
        })
        .map_err(|_| ServerError::ConfigValueOutOfRange {
            field: if is_postgres {
                "search.postgresConnectionPoolSize"
            } else {
                "search.paradeDbConnectionPoolSize"
            },
        })?;
        let frames = CommittedSearchFrames::new(Arc::new(search.projection_queue.clone()), metrics);
        let frames = Arc::new(frames);
        let visibility_wait: Arc<dyn ProjectionVisibilityWait> = frames.clone();
        let mut attempt = 1;
        let backend = loop {
            match SqlVisibilitySearchBackend::connect_dialect_with_settings(
                &configuration,
                pool_size,
                Some(visibility_wait.clone()),
                search.read_your_writes_wait,
                dialect,
                Some(registry_reader.clone()),
            )
            .await
            {
                Ok(backend) => break backend,
                Err(error) if attempt < CONNECT_ATTEMPTS => {
                    tracing::warn!(%error, attempt, "connecting to SQL visibility database failed; retrying");
                    attempt += 1;
                    tokio::time::sleep(CONNECT_RETRY_DELAY).await;
                }
                Err(source) => return Err(ServerError::SearchBackend { source }),
            }
        };
        tracing::info!(
            pool_size,
            "connected to SQL visibility database and ensured its search schema"
        );
        Ok(Self {
            backend: Arc::new(backend),
            frames,
        })
    }

    /// Returns the frames that the engine's committed-command sink feeds.
    pub(crate) fn committed_frames(&self) -> Arc<CommittedSearchFrames> {
        self.frames.clone()
    }

    pub(crate) fn search_backend(&self) -> Arc<dyn SearchBackend> {
        self.backend.clone()
    }

    /// Starts projecting in the background; it stops at `node_stopped` or [`SearchProjection::shutdown`].
    pub(crate) fn start_projection(
        self,
        committer: CommandCommitter,
        partition_count: u32,
        metrics: Arc<NodeMetrics>,
        node_stopped: impl Future<Output = NodeStopReason> + Send + 'static,
    ) -> SearchProjection {
        let projector = SqlVisibilityProjector::new_with_metric_recorder(
            self.backend,
            Arc::new(SearchProjectionMetrics(metrics)),
        );
        let backfill = refresh_attribute_slots(projector.clone());
        let worker =
            SearchProjectionWorker::new(projector, committer, self.frames, partition_count);
        let stop = CancellationToken::new();
        let stopped = stop.clone();
        let task = tokio::spawn(async move {
            tokio::select! {
                () = stopped.cancelled() => {}
                reason = node_stopped => {
                    tracing::info!(%reason, "the search projection stopped with the node");
                }
                () = worker.run() => {}
                () = backfill => {}
            }
        });
        SearchProjection { stop, task }
    }
}

async fn refresh_attribute_slots(projector: SqlVisibilityProjector) {
    let mut refresh = tokio::time::interval(Duration::from_secs(1));
    refresh.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
    loop {
        refresh.tick().await;
        if let Err(error) = projector.synchronize_attribute_slots().await {
            tracing::warn!(%error, "refreshing Attribute search slots failed");
        }
    }
}

struct SearchProjectionMetrics(Arc<NodeMetrics>);

impl dexzd_search::sql_visibility::ProjectionMetricRecorder for SearchProjectionMetrics {
    fn record_skipped_search_document(
        &self,
        _partition_id: dexzd_keyspace::PartitionId,
        _tenant_id: dexzd_keyspace::TenantId,
        _run_id: dexzd_keyspace::RunId,
    ) {
        self.0.search_skipped_documents.inc();
    }
}

/// The running SQL visibility projection; only the partition owner may project, so stop it before the lease is released.
pub(crate) struct SearchProjection {
    stop: CancellationToken,
    task: JoinHandle<()>,
}

impl SearchProjection {
    /// Stops the projection, aborting it when it does not stop within `budget`.
    ///
    /// Returns false when the projection had to be aborted.
    pub(crate) async fn shutdown(mut self, budget: Duration) -> bool {
        self.stop.cancel();
        match tokio::time::timeout(budget, &mut self.task).await {
            Ok(Ok(())) => true,
            Ok(Err(error)) => {
                tracing::warn!(%error, "the search projection task failed");
                true
            }
            Err(_) => {
                self.task.abort();
                false
            }
        }
    }
}

/// Applies the configured SQL schema through the search path, as transaction poolers keep no session state.
fn postgres_configuration(search: &SearchConfig) -> Result<ConnectionSettings, ServerError> {
    let is_postgres = search.backend == SearchBackendKind::Postgres;
    let connection_string = if is_postgres {
        &search.postgres_connection_string
    } else {
        &search.parade_db_connection_string
    };
    let schema = if is_postgres {
        &search.postgres_schema
    } else {
        &search.parade_db_schema
    };
    let mut configuration =
        ConnectionSettings::parse_for_search(connection_string).map_err(|reason| {
            ServerError::Config(ConfigError::Invalid {
                field: if is_postgres {
                    "search.postgresConnectionString"
                } else {
                    "search.paradeDbConnectionString"
                },
                message: reason.to_string(),
            })
        })?;
    if schema != DEFAULT_SCHEMA {
        let search_path = if is_postgres {
            format!("-c search_path={schema},public")
        } else {
            format!("-c search_path={schema},public,pdb")
        };
        let options = match configuration.configuration().get_options() {
            Some(options) => format!("{options} {search_path}"),
            None => search_path,
        };
        configuration.configuration_mut().options(options);
    }
    Ok(configuration)
}

#[cfg(test)]
mod tests {
    use super::*;

    struct ExcessiveAttributeRegistry;

    #[async_trait::async_trait]
    impl AttributeIndexRegistryReader for ExcessiveAttributeRegistry {
        async fn read_attribute_index_registry(
            &self,
        ) -> Result<dexzd_protocol::dexzd::v1::AttributeIndexRegistry, dexzd_search::SearchError>
        {
            Ok(dexzd_protocol::dexzd::v1::AttributeIndexRegistry {
                version: 1,
                registrations: (0..11)
                    .map(
                        |position| dexzd_protocol::dexzd::v1::AttributeIndexRegistration {
                            tenant_id: 1,
                            index_key: format!("keyword_{position}"),
                            index_type: dexzd_protocol::dex::IndexType::Keyword as i32,
                            ..Default::default()
                        },
                    )
                    .collect(),
            })
        }
    }

    #[tokio::test]
    async fn postgres_startup_rejects_existing_registry_before_opening_database_connections() {
        let config = SearchConfig {
            backend: SearchBackendKind::Postgres,
            postgres_connection_string: "postgres://localhost:1/unreachable".into(),
            ..Default::default()
        };
        let error = match SqlVisibilitySearch::connect(
            &config,
            Arc::new(ExcessiveAttributeRegistry),
            Arc::new(NodeMetrics::unregistered()),
        )
        .await
        {
            Ok(_) => panic!("over-capacity registry must fail startup"),
            Err(error) => error,
        };
        let message = error.to_string();
        assert!(message.contains("capacity exceeded"), "{message}");
        for position in 0..11 {
            assert!(
                message.contains(&format!("keyword_{position}")),
                "{message}"
            );
        }
    }

    /// Only the lease owner may project, so a projection that ignores cancellation must end before release.
    #[tokio::test(start_paused = true)]
    async fn a_projection_that_ignores_cancellation_is_aborted() {
        let (liveness, mut is_alive) = mpsc::channel::<()>(1);
        let task = tokio::spawn(async move {
            let _liveness = liveness;
            std::future::pending::<()>().await;
        });
        let projection = SearchProjection {
            stop: CancellationToken::new(),
            task,
        };
        assert!(!projection.shutdown(Duration::from_secs(1)).await);
        assert!(
            is_alive.recv().await.is_none(),
            "the aborted task released its state"
        );
    }
}
