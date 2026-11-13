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
mod signed_request;

use std::path::Path;
use std::time::Duration;

use aws_config::SdkConfig;
use aws_sdk_s3::config::{Credentials, SharedCredentialsProvider};
use bytes::Bytes;
use http_body_util::{BodyExt, Empty};
use url::Url;

use super::{S3ApiDialect, S3Credentials, S3ObjectStore, S3ObjectStoreConfig};
use crate::{
    ObjectListing, ObjectStoreError, ObjectStoreErrorKind, ObjectStoreOperation, ObjectStoreResult,
    WriteOutcome,
};
use append_body::AppendBody;
use append_response::{AppendResponse, read_append_response, read_error_document};
pub use bucket_checks::{
    TencentCosBucketCheckError, check_tencent_cos_bucket_versioning,
    check_tencent_cos_create_only_writes, check_tencent_cos_holds_no_data_of_another_provider,
};
use signed_request::{EMPTY_PAYLOAD_SHA256_HEX, SignedRequest, SignedRequestSender};

/// Largest appendable object Tencent COS stores: 5 GiB.
pub const MAX_TENCENT_COS_APPENDABLE_OBJECT_BYTES: u64 = 5 * 1024 * 1024 * 1024;

/// Slowest upload rate an append may take before it times out, beyond the request timeout.
const MIN_APPEND_UPLOAD_BYTES_PER_SECOND: u64 = 1024 * 1024;

/// User metadata that names one append, so a later read can tell it from another writer's.
const APPEND_ID_METADATA_HEADER: &str = "x-cos-meta-dex-append-id";

/// The same metadata as an S3-compatible endpoint may return it.
const APPEND_ID_S3_METADATA_HEADER: &str = "x-amz-meta-dex-append-id";

/// Signs and sends AppendObject requests, which the AWS SDK does not model.
pub(super) struct TencentCosAppendClient {
    sender: SignedRequestSender,
    /// URL of the bucket root, ending in `/`, in virtual-hosted or path style.
    bucket_url: String,
    request_timeout: Duration,
}

impl TencentCosAppendClient {
    /// Builds the client of the Tencent COS dialect, or returns `None` for another dialect.
    pub(super) fn for_dialect(
        config: &S3ObjectStoreConfig,
        shared: &SdkConfig,
    ) -> ObjectStoreResult<Option<Self>> {
        match config.api_dialect {
            S3ApiDialect::TencentCos => Self::new(config, shared).map(Some),
            S3ApiDialect::AmazonS3 | S3ApiDialect::AliyunOss => Ok(None),
        }
    }

    fn new(config: &S3ObjectStoreConfig, shared: &SdkConfig) -> ObjectStoreResult<Self> {
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
        // A read timeout would also fire during a long upload; the append deadline bounds it.
        let sender = SignedRequestSender::new(
            append_credentials(&config.credentials, shared),
            config.region.clone(),
            config.request_timeout,
        )
        .map_err(configuration_error)?;
        Ok(Self {
            sender,
            bucket_url: bucket_url.to_string(),
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
        // An empty object reads as absent, so a create must never send one.
        assert_ne!(digests.size, 0, "AppendBody accepts only non-empty bodies");
        let append_id = format!("{:032x}", rand::random::<u128>());
        let uri = format!("{}?append&position=0", self.object_uri(full_key));
        let content_length = digests.size.to_string();
        let headers = [
            ("content-length", content_length.as_str()),
            ("content-md5", digests.content_md5.as_str()),
            (APPEND_ID_METADATA_HEADER, append_id.as_str()),
        ];
        let deadline = self.request_timeout
            + Duration::from_secs(digests.size / MIN_APPEND_UPLOAD_BYTES_PER_SECOND);
        let sent = tokio::time::timeout(deadline, async {
            let request = SignedRequest {
                method: http::Method::POST,
                uri: &uri,
                headers: &headers,
                payload_sha256_hex: &digests.sha256_hex,
                body: body.into_request_body(key).await?,
            };
            let response = self.sender.send(key, request).await?;
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
            AppendResponse::Appended { etag: None } => {
                self.read_appended_etag(key, full_key, &append_id).await
            }
        }
    }

    /// Returns the URI of `full_key`, whose path keeps `.` and `..` segments as the SDK does.
    fn object_uri(&self, full_key: &str) -> String {
        format!("{}{}", self.bucket_url, encode_object_key(full_key))
    }

    /// Reads the ETag of the object that the append `append_id` just created.
    ///
    /// The append succeeded, so a failed read, or an object that another append wrote after a
    /// delete, reports [`ObjectStoreErrorKind::OutcomeUnknown`]; the caller then reads the key
    /// to confirm the write.
    async fn read_appended_etag(
        &self,
        key: &str,
        full_key: &str,
        append_id: &str,
    ) -> ObjectStoreResult<WriteOutcome> {
        let unknown = |detail: String| {
            ObjectStoreError::new(
                ObjectStoreOperation::Put,
                key,
                ObjectStoreErrorKind::OutcomeUnknown,
                format!("AppendObject succeeded, but reading its ETag failed: {detail}"),
            )
        };
        let uri = self.object_uri(full_key);
        let request = SignedRequest {
            method: http::Method::HEAD,
            uri: &uri,
            headers: &[],
            payload_sha256_hex: EMPTY_PAYLOAD_SHA256_HEX,
            body: Empty::new().map_err(|never| match never {}).boxed_unsync(),
        };
        let response = tokio::time::timeout(self.request_timeout, self.sender.send(key, request))
            .await
            .map_err(|_| {
                unknown(format!(
                    "HEAD did not finish within {:?}",
                    self.request_timeout
                ))
            })?
            .map_err(|error| unknown(error.to_string()))?;
        let status = response.status();
        if !status.is_success() {
            let (code, message) = read_error_document(key, response).await;
            return Err(unknown(format!(
                "HEAD returned HTTP {status} {code}: {message}"
            )));
        }
        let header = |name: &str| {
            response
                .headers()
                .get(name)
                .and_then(|value| value.to_str().ok())
                .map(str::to_owned)
        };
        let read_append_id =
            header(APPEND_ID_METADATA_HEADER).or_else(|| header(APPEND_ID_S3_METADATA_HEADER));
        if read_append_id.as_deref() != Some(append_id) {
            return Err(unknown(format!(
                "the key now holds the object of another append ({read_append_id:?})"
            )));
        }
        let etag = header(http::header::ETAG.as_str())
            .ok_or_else(|| unknown("the HEAD response omitted the ETag".to_owned()))?;
        Ok(WriteOutcome::Written { etag })
    }
}

/// Lists up to `max_keys` non-empty objects; a repeated stale page ends the call early.
pub(super) async fn list_non_empty_objects(
    store: &S3ObjectStore,
    prefix: &str,
    start_after: Option<&str>,
    max_keys: u32,
) -> ObjectStoreResult<ObjectListing> {
    let mut objects = Vec::new();
    let mut page_start_after = start_after.map(str::to_owned);
    let mut is_continuation = false;
    loop {
        let wanted = max_keys - u32::try_from(objects.len()).expect("at most 1000 objects");
        let page = store
            .list_page(prefix, page_start_after.as_deref(), wanted)
            .await?;
        let is_after_start = |key: &str| {
            !is_continuation || page_start_after.as_deref().is_none_or(|after| key > after)
        };
        let has_progressed = page
            .objects
            .iter()
            .any(|object| is_after_start(&object.key));
        if is_continuation && !has_progressed {
            return Ok(ObjectListing {
                objects,
                next_start_after: page_start_after,
            });
        }
        objects.extend(
            page.objects
                .into_iter()
                .filter(|object| object.size > 0 && is_after_start(&object.key)),
        );
        match page.next_start_after {
            Some(next) if objects.len() < max_keys as usize => {
                page_start_after = Some(next);
                is_continuation = true;
            }
            next_start_after => {
                return Ok(ObjectListing {
                    objects,
                    next_start_after,
                });
            }
        }
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
