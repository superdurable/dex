// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::path::PathBuf;
use std::time::Duration;

const DEFAULT_SINGLE_PUT_MAX_BYTES: usize = 16 * 1024 * 1024;
const DEFAULT_PART_SIZE: usize = 8 * 1024 * 1024;

/// Credentials of an Azure Blob Storage account.
#[derive(Clone, PartialEq, Eq)]
#[non_exhaustive]
pub enum AzureBlobCredentials {
    /// Signs requests with a storage account access key.
    AccountKey {
        /// Base64 access key of the storage account.
        account_key: String,
    },
    /// Uses a Microsoft Entra ID service principal and its client secret.
    ClientSecret {
        /// Directory (tenant) ID of the service principal.
        tenant_id: String,
        /// Application (client) ID of the service principal.
        client_id: String,
        /// Client secret of the service principal.
        client_secret: String,
    },
    /// Uses Microsoft Entra workload identity federation, as on AKS.
    WorkloadIdentity {
        /// Directory (tenant) ID of the federated identity.
        tenant_id: String,
        /// Application (client) ID of the federated identity.
        client_id: String,
        /// File holding the projected service account token.
        federated_token_file: PathBuf,
    },
    /// Uses a managed identity from the instance metadata service.
    ManagedIdentity {
        /// Client ID of a user-assigned identity, or `None` for the system-assigned identity.
        client_id: Option<String>,
        /// Token endpoint replacing the instance metadata service, such as `IDENTITY_ENDPOINT`.
        endpoint: Option<String>,
    },
    /// Sends no credentials and never contacts a token endpoint, for a local emulator or a fake server.
    Anonymous,
}

impl std::fmt::Debug for AzureBlobCredentials {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::AccountKey { .. } => formatter.write_str("AccountKey([REDACTED])"),
            Self::ClientSecret {
                tenant_id,
                client_id,
                ..
            } => formatter
                .debug_struct("ClientSecret")
                .field("tenant_id", tenant_id)
                .field("client_id", client_id)
                .field("client_secret", &"[REDACTED]")
                .finish(),
            Self::WorkloadIdentity {
                tenant_id,
                client_id,
                federated_token_file,
            } => formatter
                .debug_struct("WorkloadIdentity")
                .field("tenant_id", tenant_id)
                .field("client_id", client_id)
                .field("federated_token_file", federated_token_file)
                .finish(),
            Self::ManagedIdentity {
                client_id,
                endpoint,
            } => formatter
                .debug_struct("ManagedIdentity")
                .field("client_id", client_id)
                .field("endpoint", endpoint)
                .finish(),
            Self::Anonymous => formatter.write_str("Anonymous"),
        }
    }
}

/// Credentials of a Google Cloud Storage bucket.
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub enum GoogleCloudStorageCredentials {
    /// Signs token requests with a service account JSON key file.
    ServiceAccountKeyFile {
        /// Path of the service account JSON key file.
        path: PathBuf,
    },
    /// Uses application default credentials.
    ///
    /// Reads `credentials_file` when set, then the gcloud well-known file, then the metadata server.
    ApplicationDefault {
        /// Credentials file named by `GOOGLE_APPLICATION_CREDENTIALS`, if any.
        credentials_file: Option<PathBuf>,
    },
    /// Sends no credentials and never contacts a token endpoint, for a local emulator or a fake server.
    Anonymous,
}

/// The cloud service, the bucket or container, and its credentials.
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub enum AzureGcsProvider {
    /// Azure Blob Storage.
    AzureBlob {
        /// Storage account name.
        account: String,
        /// Existing container holding all objects.
        container: String,
        /// Account credentials.
        credentials: AzureBlobCredentials,
    },
    /// Google Cloud Storage.
    GoogleCloudStorage {
        /// Existing bucket holding all objects.
        bucket: String,
        /// Bucket credentials.
        credentials: GoogleCloudStorageCredentials,
    },
}

/// Configuration of an [`super::AzureGcsObjectStore`]. Keys passed to the store are relative to `key_prefix`.
#[derive(Debug, Clone)]
pub struct AzureGcsObjectStoreConfig {
    /// Cloud service, bucket or container, and credentials. No default.
    pub provider: AzureGcsProvider,
    /// Service URL override, such as `http://127.0.0.1:10000` for a fake server.
    /// Defaults to `None`, the cloud's public endpoint; the server connects to it by HTTP(S).
    pub endpoint: Option<String>,
    /// Prefix prepended to every relative key. An absent trailing slash is added.
    /// Defaults to an empty prefix.
    pub key_prefix: String,
    /// Connect timeout, idle timeout of reads, and total timeout of each write request. A multipart
    /// upload applies it to each part, so a large file needs no faster link than one part does.
    /// Defaults to 30 seconds.
    pub request_timeout: Duration,
    /// Largest body sent as one request. Larger create-only writes upload to a staging key and copy.
    /// Defaults to 16 MiB.
    pub single_put_max_bytes: usize,
    /// Part size of multipart uploads; defaults to 8 MiB and must be at least 5 MiB.
    pub multipart_part_size: usize,
    /// First wait before retrying a throttled write; each retry doubles it. Defaults to 1 second.
    pub throttled_write_initial_backoff: Duration,
    /// Longest wait for an Azure copy that the service left pending. After it, the large create
    /// reports `OutcomeUnknown` and keeps its staging object. Defaults to 60 seconds.
    pub pending_copy_timeout: Duration,
}

impl AzureGcsObjectStoreConfig {
    /// Creates configuration with the default timeout, sizes, and an empty key prefix.
    pub fn new(provider: AzureGcsProvider) -> Self {
        Self {
            provider,
            endpoint: None,
            key_prefix: String::new(),
            request_timeout: Duration::from_secs(30),
            single_put_max_bytes: DEFAULT_SINGLE_PUT_MAX_BYTES,
            multipart_part_size: DEFAULT_PART_SIZE,
            throttled_write_initial_backoff: Duration::from_secs(1),
            pending_copy_timeout: Duration::from_secs(60),
        }
    }
}
