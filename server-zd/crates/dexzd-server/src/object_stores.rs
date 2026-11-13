// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::Arc;

use dexzd_metrics::NodeMetrics;
use dexzd_object_store::{
    AliyunOssObjectStore, AzureBlobCredentials, AzureGcsObjectStore, AzureGcsObjectStoreConfig,
    AzureGcsProvider, Db9ObjectStore, Db9ObjectStoreConfig, GoogleCloudStorageCredentials,
    InstrumentedObjectStore, MINIMUM_SUPERSEDED_AGE_BEFORE_RECLAMATION, ObjectStore, S3ApiDialect,
    S3Credentials, S3ObjectStore, S3ObjectStoreConfig, check_aliyun_oss_create_only_writes,
    check_no_data_of_another_provider, startup_self_check,
};

use crate::ServerError;
use crate::config::{
    CredentialSource, ObjectStoreConfig, ObjectStoreCredentials, ObjectStoreLocationConfig,
    ObjectStoreProvider,
};

/// The durable stores: logs, metadata, and snapshots, plus the hard-delete value destination.
pub(crate) struct ObjectStores {
    pub(crate) primary: Arc<dyn ObjectStore>,
    pub(crate) hard_delete: Arc<dyn ObjectStore>,
}

/// Whether a store holds the cluster metadata, which only the primary store does.
#[derive(Clone, Copy, PartialEq, Eq)]
enum StoreRole {
    Primary,
    HardDelete,
}

impl ObjectStores {
    /// Connects to the configured stores and verifies that each supports conditional writes.
    pub(crate) async fn connect(
        config: &ObjectStoreConfig,
        multipart_threshold_bytes: u64,
        metrics: &NodeMetrics,
    ) -> Result<Self, ServerError> {
        let primary = match config.provider {
            ObjectStoreProvider::Db9 => connect_db9(config, metrics).await?,
            ObjectStoreProvider::S3 => {
                connect_store(primary_config(config)?, multipart_threshold_bytes, metrics).await?
            }
            ObjectStoreProvider::AliyunOss => {
                connect_aliyun_oss_store(
                    primary_config(config)?,
                    StoreRole::Primary,
                    multipart_threshold_bytes,
                    metrics,
                )
                .await?
            }
            ObjectStoreProvider::AzureBlob | ObjectStoreProvider::GoogleCloudStorage => {
                let destination = AzureGcsDestination {
                    provider: config.provider,
                    azure_account: &config.azure_account,
                    bucket: &config.bucket,
                    prefix: &config.prefix,
                    azure_blob_credentials: config.azure_blob_credentials.as_ref(),
                    google_cloud_storage_credentials: config
                        .google_cloud_storage_credentials
                        .as_ref(),
                };
                connect_azure_gcs_store(&destination, metrics).await?
            }
        };
        let hard_delete = match &config.hard_delete_store {
            Some(location) => match location.effective_provider(config.provider) {
                ObjectStoreProvider::AliyunOss => {
                    connect_aliyun_oss_store(
                        location_config(location)?,
                        StoreRole::HardDelete,
                        multipart_threshold_bytes,
                        metrics,
                    )
                    .await?
                }
                provider @ (ObjectStoreProvider::AzureBlob
                | ObjectStoreProvider::GoogleCloudStorage) => {
                    let destination = AzureGcsDestination {
                        provider,
                        azure_account: &location.azure_account,
                        bucket: &location.bucket,
                        prefix: &location.prefix,
                        azure_blob_credentials: location.azure_blob_credentials.as_ref(),
                        google_cloud_storage_credentials: location
                            .google_cloud_storage_credentials
                            .as_ref(),
                    };
                    connect_azure_gcs_store(&destination, metrics).await?
                }
                // Validation rejects db9 here, so only s3 remains.
                ObjectStoreProvider::S3 | ObjectStoreProvider::Db9 => {
                    connect_store(
                        location_config(location)?,
                        multipart_threshold_bytes,
                        metrics,
                    )
                    .await?
                }
            },
            None => primary.clone(),
        };
        Ok(Self {
            primary,
            hard_delete,
        })
    }
}

fn primary_config(config: &ObjectStoreConfig) -> Result<S3ObjectStoreConfig, ServerError> {
    Ok(S3ObjectStoreConfig {
        endpoint: non_empty(&config.endpoint),
        key_prefix: config.prefix.clone(),
        force_path_style: config.force_path_style,
        credentials: s3_credentials(
            "objectStore.credentialsSource",
            config.credentials_source,
            config.credentials.as_ref(),
        )?,
        ..S3ObjectStoreConfig::new(config.region.clone(), config.bucket.clone())
    })
}

fn location_config(
    location: &ObjectStoreLocationConfig,
) -> Result<S3ObjectStoreConfig, ServerError> {
    Ok(S3ObjectStoreConfig {
        endpoint: non_empty(&location.endpoint),
        key_prefix: location.prefix.clone(),
        force_path_style: location.force_path_style,
        credentials: s3_credentials(
            "objectStore.hardDeleteStore.credentialsSource",
            location.credentials_source,
            location.credentials.as_ref(),
        )?,
        ..S3ObjectStoreConfig::new(location.region.clone(), location.bucket.clone())
    })
}

async fn connect_store(
    config: S3ObjectStoreConfig,
    multipart_threshold_bytes: u64,
    metrics: &NodeMetrics,
) -> Result<Arc<dyn ObjectStore>, ServerError> {
    let bucket = config.bucket.clone();
    let store = new_s3_store(config, multipart_threshold_bytes).await?;
    let instrumented = InstrumentedObjectStore::new(Arc::new(store), metrics.clone());
    startup_self_check(&instrumented)
        .await
        .map_err(|source| ServerError::ObjectStoreSelfCheck { bucket, source })?;
    Ok(Arc::new(instrumented))
}

/// Connects an Aliyun OSS bucket and checks it before the server uses it.
///
/// Metrics see logical keys, because instrumentation wraps the versioning adapter.
async fn connect_aliyun_oss_store(
    mut config: S3ObjectStoreConfig,
    role: StoreRole,
    multipart_threshold_bytes: u64,
    metrics: &NodeMetrics,
) -> Result<Arc<dyn ObjectStore>, ServerError> {
    config.api_dialect = S3ApiDialect::AliyunOss;
    let bucket = config.bucket.clone();
    let inner: Arc<dyn ObjectStore> =
        Arc::new(new_s3_store(config, multipart_threshold_bytes).await?);
    let bucket_check_error = |source| ServerError::AliyunOssBucketCheck {
        bucket: bucket.clone(),
        source,
    };
    if role == StoreRole::Primary {
        check_no_data_of_another_provider(inner.as_ref())
            .await
            .map_err(bucket_check_error)?;
    }
    check_aliyun_oss_create_only_writes(inner.as_ref())
        .await
        .map_err(bucket_check_error)?;
    let adapter = Arc::new(AliyunOssObjectStore::new(
        inner,
        MINIMUM_SUPERSEDED_AGE_BEFORE_RECLAMATION,
        metrics.clone(),
    ));
    match adapter.remove_stale_startup_probes().await {
        Ok(0) => {}
        Ok(removed_probe_keys) => {
            tracing::info!(
                bucket,
                removed_probe_keys,
                "removed stale startup probe objects"
            );
        }
        // Leftover probes only use storage, so a failed cleanup does not stop startup.
        Err(error) => {
            tracing::warn!(bucket, %error, "removing stale startup probe objects failed");
        }
    }
    let instrumented = InstrumentedObjectStore::new(adapter, metrics.clone());
    startup_self_check(&instrumented)
        .await
        .map_err(|source| ServerError::ObjectStoreSelfCheck { bucket, source })?;
    Ok(Arc::new(instrumented))
}

async fn new_s3_store(
    mut config: S3ObjectStoreConfig,
    multipart_threshold_bytes: u64,
) -> Result<S3ObjectStore, ServerError> {
    config.multipart_threshold = usize::try_from(multipart_threshold_bytes).map_err(|_| {
        ServerError::ConfigValueOutOfRange {
            field: "commit.multipartThresholdBytes",
        }
    })?;
    let bucket = config.bucket.clone();
    S3ObjectStore::new(config)
        .await
        .map_err(|source| ServerError::ObjectStore { bucket, source })
}

fn s3_credentials(
    field: &'static str,
    source: CredentialSource,
    credentials: Option<&ObjectStoreCredentials>,
) -> Result<S3Credentials, ServerError> {
    match (source, credentials) {
        (CredentialSource::DefaultChain, _) => Ok(S3Credentials::DefaultChain),
        (CredentialSource::Environment, Some(credentials)) => Ok(S3Credentials::Static {
            access_key_id: credentials.access_key_id.clone(),
            secret_access_key: credentials.secret_access_key.clone(),
            session_token: credentials.session_token.clone(),
        }),
        (CredentialSource::Environment, None) => Err(ServerError::UnsupportedCredentials {
            field,
            detail: "environment credentials were not resolved".to_owned(),
        }),
        (CredentialSource::Anonymous, _) => Ok(S3Credentials::Anonymous),
    }
}

fn non_empty(value: &str) -> Option<String> {
    (!value.is_empty()).then(|| value.to_owned())
}

async fn connect_db9(
    config: &ObjectStoreConfig,
    metrics: &NodeMetrics,
) -> Result<Arc<dyn ObjectStore>, ServerError> {
    let store = Db9ObjectStore::new(Db9ObjectStoreConfig {
        connection_string: config.db9_connection_string.0.clone(),
        bucket: config.bucket.clone(),
        key_prefix: config.prefix.clone(),
        pool_size: config.db9_pool_size,
        request_timeout: config.db9_request_timeout,
    })
    .await
    .map_err(|source| ServerError::ObjectStore {
        bucket: config.bucket.clone(),
        source,
    })?;
    let instrumented = InstrumentedObjectStore::new(Arc::new(store), metrics.clone());
    startup_self_check(&instrumented).await.map_err(|source| {
        ServerError::ObjectStoreSelfCheck {
            bucket: config.bucket.clone(),
            source,
        }
    })?;
    Ok(Arc::new(instrumented))
}

/// An azureBlob or googleCloudStorage destination from the primary or hard-delete configuration.
struct AzureGcsDestination<'a> {
    provider: ObjectStoreProvider,
    azure_account: &'a str,
    bucket: &'a str,
    prefix: &'a str,
    azure_blob_credentials: Option<&'a AzureBlobCredentials>,
    google_cloud_storage_credentials: Option<&'a GoogleCloudStorageCredentials>,
}

/// Connects, deletes abandoned staging uploads, and verifies conditional writes.
async fn connect_azure_gcs_store(
    destination: &AzureGcsDestination<'_>,
    metrics: &NodeMetrics,
) -> Result<Arc<dyn ObjectStore>, ServerError> {
    let unresolved = || ServerError::UnsupportedCredentials {
        field: "objectStore.credentialsSource",
        detail: "credentials were not resolved".to_owned(),
    };
    let provider = match destination.provider {
        ObjectStoreProvider::AzureBlob => AzureGcsProvider::AzureBlob {
            account: destination.azure_account.to_owned(),
            container: destination.bucket.to_owned(),
            credentials: destination
                .azure_blob_credentials
                .ok_or_else(unresolved)?
                .clone(),
        },
        _ => AzureGcsProvider::GoogleCloudStorage {
            bucket: destination.bucket.to_owned(),
            credentials: destination
                .google_cloud_storage_credentials
                .ok_or_else(unresolved)?
                .clone(),
        },
    };
    let bucket = destination.bucket.to_owned();
    let store = AzureGcsObjectStore::new(AzureGcsObjectStoreConfig {
        key_prefix: destination.prefix.to_owned(),
        ..AzureGcsObjectStoreConfig::new(provider)
    })
    .map_err(|source| ServerError::ObjectStore {
        bucket: bucket.clone(),
        source,
    })?;
    match store.delete_abandoned_staging_objects().await {
        Ok(0) => {}
        Ok(deleted) => tracing::info!(%bucket, deleted, "deleted abandoned staging uploads"),
        Err(error) => tracing::warn!(%bucket, %error, "failed to delete abandoned staging uploads"),
    }
    let instrumented = InstrumentedObjectStore::new(Arc::new(store), metrics.clone());
    startup_self_check(&instrumented)
        .await
        .map_err(|source| ServerError::ObjectStoreSelfCheck { bucket, source })?;
    Ok(Arc::new(instrumented))
}
