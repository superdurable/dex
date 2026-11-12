// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::time::Duration;

use object_store::client::{HttpClient, HttpError, HttpRequestBody};
use object_store::gcp::GcpCredentialProvider;
use serde::Deserialize;
use time::OffsetDateTime;
use time::format_description::well_known::Rfc3339;

use super::error_mapping::{RequestEffect, map_object_store_error};
use super::response_status_classifier::{HttpFailureCause, http_failure_cause};
use crate::{ObjectStoreError, ObjectStoreErrorKind, ObjectStoreOperation, ObjectStoreResult};

const LIST_ATTEMPTS: u32 = 3;
const LISTED_FIELDS: &str = "items(name,size,generation,updated),nextPageToken";

/// Lists GCS objects through the JSON API, which reports the generations that the XML listing drops.
#[derive(Debug)]
pub(crate) struct GoogleCloudStorageListing {
    http_client: HttpClient,
    credentials: Option<GcpCredentialProvider>,
    base_url: String,
    bucket: String,
}

/// One object of a JSON API listing page.
#[derive(Debug, Deserialize)]
pub(crate) struct ListedGoogleCloudStorageObject {
    pub(crate) name: String,
    #[serde(deserialize_with = "deserialize_decimal_string")]
    pub(crate) size: u64,
    pub(crate) generation: String,
    pub(crate) updated: Option<String>,
}

/// One JSON API listing page.
#[derive(Debug, Deserialize)]
pub(crate) struct GoogleCloudStorageListingPage {
    #[serde(default)]
    pub(crate) items: Vec<ListedGoogleCloudStorageObject>,
    #[serde(rename = "nextPageToken")]
    pub(crate) next_page_token: Option<String>,
}

impl GoogleCloudStorageListing {
    pub(crate) fn new(
        http_client: HttpClient,
        credentials: Option<GcpCredentialProvider>,
        base_url: String,
        bucket: String,
    ) -> Self {
        Self {
            http_client,
            credentials,
            base_url: base_url.trim_end_matches('/').to_owned(),
            bucket,
        }
    }

    /// Returns one page of objects whose names start with `prefix` and sort at or after
    /// `start_offset`, or after the position that `page_token` names.
    pub(crate) async fn list_page(
        &self,
        prefix: &str,
        start_offset: Option<&str>,
        page_token: Option<&str>,
        max_results: u32,
    ) -> ObjectStoreResult<GoogleCloudStorageListingPage> {
        let mut attempt = 1;
        loop {
            match self
                .send_list_request(prefix, start_offset, page_token, max_results)
                .await
            {
                Err(error)
                    if error.kind == ObjectStoreErrorKind::Transient && attempt < LIST_ATTEMPTS =>
                {
                    tokio::time::sleep(Duration::from_millis(200 << attempt)).await;
                    attempt += 1;
                }
                result => return result,
            }
        }
    }

    async fn send_list_request(
        &self,
        prefix: &str,
        start_offset: Option<&str>,
        page_token: Option<&str>,
        max_results: u32,
    ) -> ObjectStoreResult<GoogleCloudStorageListingPage> {
        let mut url = url::Url::parse(&format!("{}/storage/v1/b", self.base_url))
            .map_err(|error| list_error(prefix, ObjectStoreErrorKind::Permanent, error))?;
        url.path_segments_mut()
            .map_err(|()| {
                list_error(
                    prefix,
                    ObjectStoreErrorKind::Permanent,
                    "GCS base URL cannot hold a path",
                )
            })?
            .push(&self.bucket)
            .push("o");
        {
            let mut query = url.query_pairs_mut();
            query
                .append_pair("prefix", prefix)
                .append_pair("maxResults", &max_results.to_string())
                .append_pair("fields", LISTED_FIELDS);
            if let Some(start_offset) = start_offset {
                query.append_pair("startOffset", start_offset);
            }
            if let Some(page_token) = page_token {
                query.append_pair("pageToken", page_token);
            }
        }
        let mut request = http::Request::builder()
            .method(http::Method::GET)
            .uri(url.as_str());
        if let Some(credentials) = &self.credentials {
            let credential = credentials.get_credential().await.map_err(|error| {
                map_object_store_error(
                    ObjectStoreOperation::List,
                    prefix,
                    RequestEffect::Read,
                    &error,
                )
            })?;
            request = request.header(
                http::header::AUTHORIZATION,
                format!("Bearer {}", credential.bearer),
            );
        }
        let request = request
            .body(HttpRequestBody::empty())
            .map_err(|error| list_error(prefix, ObjectStoreErrorKind::Permanent, error))?;
        let response = self
            .http_client
            .execute(request)
            .await
            .map_err(|error| map_http_error(prefix, &error))?;
        let status = response.status();
        let body = response
            .into_body()
            .bytes()
            .await
            .map_err(|error| map_http_error(prefix, &error))?;
        if !status.is_success() {
            let kind = match status.as_u16() {
                401 | 403 | 404 => ObjectStoreErrorKind::Permanent,
                _ => ObjectStoreErrorKind::Transient,
            };
            return Err(list_error(
                prefix,
                kind,
                format!("HTTP {status}: {}", String::from_utf8_lossy(&body)),
            ));
        }
        serde_json::from_slice(&body)
            .map_err(|error| list_error(prefix, ObjectStoreErrorKind::Permanent, error))
    }
}

impl ListedGoogleCloudStorageObject {
    /// Returns the `updated` time in microseconds since the Unix epoch, or `None` when absent.
    pub(crate) fn updated_unix_micros(&self, prefix: &str) -> ObjectStoreResult<Option<i64>> {
        let Some(updated) = &self.updated else {
            return Ok(None);
        };
        let parsed = OffsetDateTime::parse(updated, &Rfc3339)
            .map_err(|error| list_error(prefix, ObjectStoreErrorKind::Permanent, error))?;
        i64::try_from(parsed.unix_timestamp_nanos() / 1_000)
            .map(Some)
            .map_err(|error| list_error(prefix, ObjectStoreErrorKind::Permanent, error))
    }
}

fn map_http_error(prefix: &str, error: &HttpError) -> ObjectStoreError {
    let kind = match http_failure_cause(error) {
        HttpFailureCause::Status(status) if matches!(status.status, 408 | 429 | 500..=599) => {
            ObjectStoreErrorKind::Transient
        }
        HttpFailureCause::Status(_) | HttpFailureCause::NotHttp => ObjectStoreErrorKind::Permanent,
        HttpFailureCause::Transport(_) => ObjectStoreErrorKind::Transient,
    };
    list_error(prefix, kind, error)
}

fn list_error(prefix: &str, kind: ObjectStoreErrorKind, detail: impl ToString) -> ObjectStoreError {
    ObjectStoreError::new(ObjectStoreOperation::List, prefix, kind, detail.to_string())
}

fn deserialize_decimal_string<'de, D>(deserializer: D) -> Result<u64, D::Error>
where
    D: serde::Deserializer<'de>,
{
    let text = String::deserialize(deserializer)?;
    text.parse().map_err(serde::de::Error::custom)
}
