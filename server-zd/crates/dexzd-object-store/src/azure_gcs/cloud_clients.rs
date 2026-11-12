// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::Arc;
use std::time::Duration;

use object_store::azure::AzureCredential;
use object_store::azure::{MicrosoftAzure, MicrosoftAzureBuilder};
use object_store::client::HttpConnector;
use object_store::gcp::GcpCredential;
use object_store::gcp::{GoogleCloudStorage, GoogleCloudStorageBuilder};
use object_store::{BackoffConfig, ClientOptions, RetryConfig, StaticCredentialProvider};

use super::google_cloud_storage_listing::GoogleCloudStorageListing;
use super::response_status_classifier::{
    RequestedOrigins, ResponseStatusClassifyingConnector, TransportTimeouts,
};
use super::{
    AzureBlobCredentials, AzureGcsCloud, AzureGcsObjectStoreConfig, AzureGcsProvider,
    GoogleCloudStorageCredentials,
};
use crate::{ObjectStoreError, ObjectStoreErrorKind, ObjectStoreOperation, ObjectStoreResult};

const DEFAULT_GCS_BASE_URL: &str = "https://storage.googleapis.com";
const READ_RETRIES: usize = 2;

/// The `object_store` clients of one cloud: retrying reads and single-attempt writes.
#[derive(Debug)]
pub(super) enum CloudClients {
    AzureBlob {
        reads: MicrosoftAzure,
        writes: MicrosoftAzure,
    },
    GoogleCloudStorage {
        reads: GoogleCloudStorage,
        writes: GoogleCloudStorage,
        listing: GoogleCloudStorageListing,
    },
}

impl CloudClients {
    /// Builds retrying read clients and single-attempt write clients that share credentials.
    pub(super) fn build(
        config: &AzureGcsObjectStoreConfig,
        requested_origins: &RequestedOrigins,
    ) -> ObjectStoreResult<(AzureGcsCloud, Self)> {
        let allow_http = config
            .endpoint
            .as_deref()
            .is_some_and(|endpoint| endpoint.starts_with("http://"));
        let read_options = ClientOptions::new()
            .with_allow_http(allow_http)
            .with_connect_timeout(config.request_timeout)
            .with_read_timeout(config.request_timeout)
            .with_timeout_disabled();
        let write_options = ClientOptions::new()
            .with_allow_http(allow_http)
            .with_connect_timeout(config.request_timeout)
            .with_timeout(config.request_timeout);
        let read_retry = RetryConfig {
            backoff: BackoffConfig::default(),
            max_retries: READ_RETRIES,
            retry_timeout: config.request_timeout.saturating_mul(3),
        };
        let write_retry = RetryConfig {
            backoff: BackoffConfig::default(),
            max_retries: 0,
            retry_timeout: Duration::ZERO,
        };
        let read_timeouts = TransportTimeouts {
            connect: config.request_timeout,
            read_idle: Some(config.request_timeout),
            total: None,
        };
        let write_timeouts = TransportTimeouts {
            connect: config.request_timeout,
            read_idle: None,
            total: Some(config.request_timeout),
        };
        let (cloud, clients) = match &config.provider {
            AzureGcsProvider::AzureBlob {
                account,
                container,
                credentials,
            } => {
                let cloud = AzureGcsCloud::AzureBlob;
                let base = |timeouts| {
                    let builder = MicrosoftAzureBuilder::new()
                        .with_account(account.as_str())
                        .with_container_name(container.as_str())
                        .with_skip_signature(*credentials == AzureBlobCredentials::Anonymous)
                        .with_http_connector(ResponseStatusClassifyingConnector::new(
                            cloud,
                            timeouts,
                            requested_origins.clone(),
                        ));
                    match &config.endpoint {
                        Some(endpoint) => builder.with_endpoint(endpoint.clone()),
                        None => builder,
                    }
                };
                let reads = with_azure_credentials(base(read_timeouts), credentials)
                    .with_retry(read_retry)
                    .with_client_options(read_options)
                    .build()
                    .map_err(|error| config_error(error.to_string()))?;
                let writes = base(write_timeouts)
                    .with_credentials(reads.credentials().clone())
                    .with_retry(write_retry)
                    .with_client_options(write_options)
                    .build()
                    .map_err(|error| config_error(error.to_string()))?;
                (cloud, CloudClients::AzureBlob { reads, writes })
            }
            AzureGcsProvider::GoogleCloudStorage {
                bucket,
                credentials,
            } => {
                let cloud = AzureGcsCloud::GoogleCloudStorage;
                let base_url = config
                    .endpoint
                    .clone()
                    .unwrap_or_else(|| DEFAULT_GCS_BASE_URL.to_owned());
                let is_anonymous = *credentials == GoogleCloudStorageCredentials::Anonymous;
                let base = |timeouts| {
                    GoogleCloudStorageBuilder::new()
                        .with_bucket_name(bucket.as_str())
                        .with_base_url(&base_url)
                        .with_skip_signature(is_anonymous)
                        .with_http_connector(ResponseStatusClassifyingConnector::new(
                            cloud,
                            timeouts,
                            requested_origins.clone(),
                        ))
                };
                let reads = with_gcs_credentials(base(read_timeouts), credentials)
                    .with_retry(read_retry)
                    .with_client_options(read_options.clone())
                    .build()
                    .map_err(|error| config_error(error.to_string()))?;
                let writes = base(write_timeouts)
                    .with_credentials(reads.credentials().clone())
                    .with_retry(write_retry)
                    .with_client_options(write_options)
                    .build()
                    .map_err(|error| config_error(error.to_string()))?;
                let listing = GoogleCloudStorageListing::new(
                    ResponseStatusClassifyingConnector::new(
                        cloud,
                        read_timeouts,
                        requested_origins.clone(),
                    )
                    .connect(&read_options)
                    .map_err(|error| config_error(error.to_string()))?,
                    (!is_anonymous).then(|| reads.credentials().clone()),
                    base_url,
                    bucket.clone(),
                );
                (
                    cloud,
                    CloudClients::GoogleCloudStorage {
                        reads,
                        writes,
                        listing,
                    },
                )
            }
        };
        Ok((cloud, clients))
    }

    pub(super) fn reads(&self) -> &dyn object_store::ObjectStore {
        match self {
            CloudClients::AzureBlob { reads, .. } => reads,
            CloudClients::GoogleCloudStorage { reads, .. } => reads,
        }
    }

    pub(super) fn writes(&self) -> &dyn object_store::ObjectStore {
        match self {
            CloudClients::AzureBlob { writes, .. } => writes,
            CloudClients::GoogleCloudStorage { writes, .. } => writes,
        }
    }
}

fn with_azure_credentials(
    builder: MicrosoftAzureBuilder,
    credentials: &AzureBlobCredentials,
) -> MicrosoftAzureBuilder {
    match credentials {
        AzureBlobCredentials::AccountKey { account_key } => builder.with_access_key(account_key),
        AzureBlobCredentials::ClientSecret {
            tenant_id,
            client_id,
            client_secret,
        } => builder.with_client_secret_authorization(client_id, client_secret, tenant_id),
        AzureBlobCredentials::WorkloadIdentity {
            tenant_id,
            client_id,
            federated_token_file,
        } => builder
            .with_tenant_id(tenant_id)
            .with_client_id(client_id)
            .with_federated_token_file(federated_token_file.to_string_lossy()),
        AzureBlobCredentials::ManagedIdentity {
            client_id,
            endpoint,
        } => {
            let builder = match client_id {
                Some(client_id) => builder.with_client_id(client_id),
                None => builder,
            };
            match endpoint {
                Some(endpoint) => builder.with_msi_endpoint(endpoint),
                None => builder,
            }
        }
        // A static credential keeps the client from resolving a managed identity it never uses.
        AzureBlobCredentials::Anonymous => builder.with_credentials(Arc::new(
            StaticCredentialProvider::new(AzureCredential::BearerToken(String::new())),
        )),
    }
}

fn with_gcs_credentials(
    builder: GoogleCloudStorageBuilder,
    credentials: &GoogleCloudStorageCredentials,
) -> GoogleCloudStorageBuilder {
    match credentials {
        GoogleCloudStorageCredentials::ServiceAccountKeyFile { path } => {
            builder.with_service_account_path(path.to_string_lossy())
        }
        GoogleCloudStorageCredentials::ApplicationDefault {
            credentials_file: Some(credentials_file),
        } => builder.with_application_credentials(credentials_file.to_string_lossy()),
        GoogleCloudStorageCredentials::ApplicationDefault {
            credentials_file: None,
        } => builder,
        // object_store asks the credential provider even with skip_signature, so it must be static.
        GoogleCloudStorageCredentials::Anonymous => {
            builder.with_credentials(Arc::new(StaticCredentialProvider::new(GcpCredential {
                bearer: String::new(),
            })))
        }
    }
}

pub(super) fn config_error(detail: String) -> ObjectStoreError {
    ObjectStoreError::new(
        ObjectStoreOperation::Put,
        "",
        ObjectStoreErrorKind::Permanent,
        detail,
    )
}
