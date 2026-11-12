// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The Tencent COS dialect: create-only writes through AppendObject at position 0.

mod append_body;
mod append_response;
mod bucket_checks;

use std::path::Path;
use std::time::{Duration, SystemTime};

use aws_config::SdkConfig;
use aws_sdk_s3::Client;
use aws_sdk_s3::config::{Credentials, ProvideCredentials, SharedCredentialsProvider};
use aws_sigv4::http_request::{
    PayloadChecksumKind, PercentEncodingMode, SignableBody, SignableRequest, SigningSettings,
    UriPathNormalizationMode, sign,
};
use aws_sigv4::sign::v4;
use bytes::Bytes;
use url::Url;

use super::{S3ApiDialect, S3Credentials, S3ObjectStoreConfig};
use crate::{
    ObjectStoreError, ObjectStoreErrorKind, ObjectStoreOperation, ObjectStoreResult, WriteOutcome,
};
use append_body::{AppendBody, AppendBodyDigests};
use append_response::{AppendResponse, map_send_error, read_append_response};
pub use bucket_checks::{
    TencentCosBucketCheckError, check_tencent_cos_bucket_versioning,
    check_tencent_cos_create_only_writes, check_tencent_cos_holds_no_data_of_another_provider,
};

/// Largest appendable object Tencent COS stores: 5 GiB.
pub const MAX_TENCENT_COS_APPENDABLE_OBJECT_BYTES: u64 = 5 * 1024 * 1024 * 1024;

/// Slowest upload rate an append may take before it times out, beyond the request timeout.
const MIN_APPEND_UPLOAD_BYTES_PER_SECOND: u64 = 1024 * 1024;

/// Signs and sends AppendObject requests, which the AWS SDK does not model.
pub(super) struct TencentCosAppendClient {
    http_client: reqwest::Client,
    /// `None` sends unsigned requests.
    credentials: Option<SharedCredentialsProvider>,
    region: String,
    /// URL of the bucket root, ending in `/`, in virtual-hosted or path style.
    bucket_url: Url,
    /// Reads the ETag of an append, whose response carries only the next position.
    read_client: Client,
    bucket: String,
    request_timeout: Duration,
}

impl TencentCosAppendClient {
    /// Builds the client of the Tencent COS dialect, or returns `None` for another dialect.
    pub(super) fn for_dialect(
        config: &S3ObjectStoreConfig,
        shared: &SdkConfig,
        read_client: &Client,
    ) -> ObjectStoreResult<Option<Self>> {
        match config.api_dialect {
            S3ApiDialect::TencentCos => Self::new(config, shared, read_client.clone()).map(Some),
            S3ApiDialect::AmazonS3 | S3ApiDialect::AliyunOss => Ok(None),
        }
    }

    fn new(
        config: &S3ObjectStoreConfig,
        shared: &SdkConfig,
        read_client: Client,
    ) -> ObjectStoreResult<Self> {
        let configuration_error = |detail: String| {
            ObjectStoreError::new(
                ObjectStoreOperation::Put,
                "",
                ObjectStoreErrorKind::Permanent,
                detail,
            )
        };
        let endpoint = config.endpoint.as_deref().ok_or_else(|| {
            configuration_error(
                "the Tencent COS dialect requires an endpoint, such as \
                 https://cos.ap-guangzhou.myqcloud.com"
                    .to_owned(),
            )
        })?;
        let bucket_url = bucket_root_url(endpoint, &config.bucket, config.force_path_style)
            .map_err(configuration_error)?;
        let http_client = reqwest::Client::builder()
            .redirect(reqwest::redirect::Policy::none())
            // A read timeout would also fire during a long upload; the append deadline bounds it.
            .connect_timeout(config.request_timeout)
            .build()
            .map_err(|error| configuration_error(format!("building the HTTP client: {error}")))?;
        Ok(Self {
            http_client,
            credentials: append_credentials(&config.credentials, shared),
            region: config.region.clone(),
            bucket_url,
            read_client,
            bucket: config.bucket.clone(),
            request_timeout: config.request_timeout,
        })
    }

    /// Creates `full_key` from `body` with one AppendObject at position 0, so a concurrent or
    /// earlier object at the key makes it fail.
    ///
    /// Returns [`WriteOutcome::PreconditionFailed`] for HTTP 409 `AppendPositionErr` or
    /// `ObjectNotAppendable`, meaning nothing was written. An empty body or one over
    /// [`MAX_TENCENT_COS_APPENDABLE_OBJECT_BYTES`] fails with
    /// [`ObjectStoreErrorKind::Permanent`] before any request. A request that may have reached
    /// the store fails with [`ObjectStoreErrorKind::OutcomeUnknown`].
    pub(super) async fn create_by_append(
        &self,
        key: &str,
        full_key: &str,
        body: AppendSource<'_>,
    ) -> ObjectStoreResult<WriteOutcome> {
        let body = match body {
            AppendSource::Bytes(bytes) => AppendBody::from_bytes(key, bytes)?,
            AppendSource::File(path) => AppendBody::from_file(key, path).await?,
        };
        let digests = body.digests(key).await?;
        let url = self.append_url(full_key);
        let headers = self.signed_headers(key, &url, &digests).await?;
        let mut request = self
            .http_client
            .post(url)
            .header(http::header::CONTENT_LENGTH, digests.size)
            .header("content-md5", &digests.content_md5);
        for (name, value) in &headers {
            request = request.header(name.as_str(), value.as_str());
        }
        let deadline = self.request_timeout
            + Duration::from_secs(digests.size / MIN_APPEND_UPLOAD_BYTES_PER_SECOND);
        let sent = tokio::time::timeout(deadline, async {
            let response = request
                .body(body.into_request_body(key).await?)
                .send()
                .await
                .map_err(|error| map_send_error(key, &error))?;
            read_append_response(key, response).await
        })
        .await
        .map_err(|_| {
            ObjectStoreError::new(
                ObjectStoreOperation::Put,
                key,
                ObjectStoreErrorKind::OutcomeUnknown,
                format!("AppendObject did not finish within {deadline:?}"),
            )
        })??;
        match sent {
            AppendResponse::PositionTaken => Ok(WriteOutcome::PreconditionFailed),
            AppendResponse::Appended { etag: Some(etag) } => Ok(WriteOutcome::Written { etag }),
            AppendResponse::Appended { etag: None } => self.read_appended_etag(key, full_key).await,
        }
    }

    fn append_url(&self, full_key: &str) -> Url {
        let mut url = self.bucket_url.clone();
        let path = format!("{}{}", url.path(), encode_object_key(full_key));
        url.set_path(&path);
        url.set_query(Some("append&position=0"));
        url
    }

    /// Returns the SigV4 headers of the request, or none for anonymous credentials.
    async fn signed_headers(
        &self,
        key: &str,
        url: &Url,
        digests: &AppendBodyDigests,
    ) -> ObjectStoreResult<Vec<(String, String)>> {
        let Some(provider) = &self.credentials else {
            return Ok(Vec::new());
        };
        let signing_error = |kind, detail: String| {
            ObjectStoreError::new(ObjectStoreOperation::Put, key, kind, detail)
        };
        // Nothing was sent, so a credential failure may be retried.
        let credentials = provider.provide_credentials().await.map_err(|error| {
            signing_error(
                ObjectStoreErrorKind::Transient,
                format!("resolving credentials for AppendObject: {error}"),
            )
        })?;
        let identity = credentials.into();
        let mut settings = SigningSettings::default();
        settings.percent_encoding_mode = PercentEncodingMode::Single;
        settings.uri_path_normalization_mode = UriPathNormalizationMode::Disabled;
        settings.payload_checksum_kind = PayloadChecksumKind::XAmzSha256;
        let params = v4::SigningParams::builder()
            .identity(&identity)
            .region(&self.region)
            .name("s3")
            .time(SystemTime::now())
            .settings(settings)
            .build()
            .map_err(|error| signing_error(ObjectStoreErrorKind::Permanent, error.to_string()))?
            .into();
        let content_length = digests.size.to_string();
        let unsigned_headers = [
            ("content-length", content_length.as_str()),
            ("content-md5", digests.content_md5.as_str()),
        ];
        let signable = SignableRequest::new(
            "POST",
            url.as_str(),
            unsigned_headers.into_iter(),
            SignableBody::Precomputed(digests.sha256_hex.clone()),
        )
        .map_err(|error| signing_error(ObjectStoreErrorKind::Permanent, error.to_string()))?;
        let (instructions, _signature) = sign(signable, &params)
            .map_err(|error| signing_error(ObjectStoreErrorKind::Permanent, error.to_string()))?
            .into_parts();
        let (headers, _query) = instructions.into_parts();
        Ok(headers
            .into_iter()
            .map(|header| (header.name().to_owned(), header.value().to_owned()))
            .collect())
    }

    /// Reads the ETag of the object this client just appended.
    ///
    /// The append succeeded, so a failed read reports [`ObjectStoreErrorKind::OutcomeUnknown`]
    /// and the caller reads the key to confirm the write.
    async fn read_appended_etag(
        &self,
        key: &str,
        full_key: &str,
    ) -> ObjectStoreResult<WriteOutcome> {
        let unknown = |detail: String| {
            ObjectStoreError::new(
                ObjectStoreOperation::Put,
                key,
                ObjectStoreErrorKind::OutcomeUnknown,
                format!("AppendObject succeeded, but reading its ETag failed: {detail}"),
            )
        };
        let head = self
            .read_client
            .head_object()
            .bucket(&self.bucket)
            .key(full_key)
            .send()
            .await
            .map_err(|error| unknown(error.to_string()))?;
        let etag = head
            .e_tag()
            .ok_or_else(|| unknown("the HEAD response omitted the ETag".to_owned()))?;
        Ok(WriteOutcome::Written {
            etag: etag.to_owned(),
        })
    }
}

/// Returns the provider that signs AppendObject requests, or `None` for anonymous requests.
fn append_credentials(
    credentials: &S3Credentials,
    shared: &SdkConfig,
) -> Option<SharedCredentialsProvider> {
    match credentials {
        S3Credentials::DefaultChain => shared.credentials_provider(),
        S3Credentials::Static {
            access_key_id,
            secret_access_key,
            session_token,
        } => Some(SharedCredentialsProvider::new(Credentials::new(
            access_key_id.clone(),
            secret_access_key.clone(),
            session_token.clone(),
            None,
            "dexzd-object-store",
        ))),
        S3Credentials::Anonymous => None,
    }
}

/// The body of a create-only write that the Tencent COS dialect sends with AppendObject.
pub(super) enum AppendSource<'a> {
    Bytes(Bytes),
    File(&'a Path),
}

/// Returns the bucket root URL: path style for an IP endpoint or `force_path_style`, as the AWS
/// SDK addresses them, and virtual-hosted style otherwise.
fn bucket_root_url(endpoint: &str, bucket: &str, is_force_path_style: bool) -> Result<Url, String> {
    let mut url =
        Url::parse(endpoint).map_err(|error| format!("invalid endpoint {endpoint:?}: {error}"))?;
    let host = url
        .host()
        .ok_or_else(|| format!("endpoint {endpoint:?} has no host"))?;
    let is_ip_host = matches!(host, url::Host::Ipv4(_) | url::Host::Ipv6(_));
    let base_path = url.path().trim_end_matches('/').to_owned();
    if is_force_path_style || is_ip_host {
        url.set_path(&format!("{base_path}/{}/", encode_object_key(bucket)));
    } else {
        let virtual_host = format!("{bucket}.{host}");
        url.set_host(Some(&virtual_host))
            .map_err(|error| format!("invalid bucket host {virtual_host:?}: {error}"))?;
        url.set_path(&format!("{base_path}/"));
    }
    url.set_query(None);
    Ok(url)
}

/// Percent-encodes every byte of `key` except unreserved characters and `/`, as S3 paths need.
fn encode_object_key(key: &str) -> String {
    let mut encoded = String::with_capacity(key.len());
    for byte in key.bytes() {
        if byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_' | b'.' | b'~' | b'/') {
            encoded.push(char::from(byte));
        } else {
            encoded.push_str(&format!("%{byte:02X}"));
        }
    }
    encoded
}

/// Returns the permanent error of a replacement, which Tencent COS cannot do atomically.
pub(super) fn atomic_replacement_unsupported(key: &str) -> ObjectStoreError {
    ObjectStoreError::new(
        ObjectStoreOperation::Put,
        key,
        ObjectStoreErrorKind::Permanent,
        "the Tencent COS dialect has no atomic replacement, so ReplaceIfEtagMatches is unsupported",
    )
}

/// Returns the permanent error of a conditional PutObject, which Tencent COS ignores.
pub(super) fn conditional_put_object_unsupported(key: &str) -> ObjectStoreError {
    ObjectStoreError::new(
        ObjectStoreOperation::Put,
        key,
        ObjectStoreErrorKind::Permanent,
        "Tencent COS ignores conditional PutObject headers; create-only writes use AppendObject",
    )
}

#[cfg(test)]
mod tests {
    use super::{bucket_root_url, encode_object_key};

    #[test]
    fn bucket_urls_follow_the_sdk_addressing_style() {
        let virtual_hosted = bucket_root_url("https://cos.ap-guangzhou.myqcloud.com", "b-1", false)
            .expect("a virtual-hosted URL");
        assert_eq!(
            virtual_hosted.as_str(),
            "https://b-1.cos.ap-guangzhou.myqcloud.com/"
        );
        let ip = bucket_root_url("http://127.0.0.1:9000", "b-1", false).expect("an IP URL");
        assert_eq!(ip.as_str(), "http://127.0.0.1:9000/b-1/");
        let forced = bucket_root_url("https://cos.example.com/base/", "b-1", true)
            .expect("a path-style URL");
        assert_eq!(forced.as_str(), "https://cos.example.com/base/b-1/");
    }

    #[test]
    fn object_keys_keep_unreserved_characters_and_slashes() {
        assert_eq!(
            encode_object_key("dexzd/snapshot/a b+c%~.log"),
            "dexzd/snapshot/a%20b%2Bc%25~.log"
        );
    }
}
