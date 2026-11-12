// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Classification of AppendObject responses.

use http_body_util::{BodyExt, Limited};
use hyper::body::Incoming;

use crate::{ObjectStoreError, ObjectStoreErrorKind, ObjectStoreOperation, ObjectStoreResult};

/// Error body bytes kept for the error code and message.
const MAX_ERROR_BODY_BYTES: usize = 64 * 1024;

/// Codes of a 409 that means another object already holds the key, so nothing was written.
const POSITION_TAKEN_CODES: [&str; 2] = ["AppendPositionErr", "ObjectNotAppendable"];

/// What an AppendObject at position 0 did.
#[derive(Debug)]
pub(super) enum AppendResponse {
    /// The object was created; COS may omit its ETag.
    Appended { etag: Option<String> },
    /// The key already holds an object.
    PositionTaken,
}

/// Classifies an AppendObject response like the S3 dialects classify writes.
///
/// Returns [`AppendResponse::PositionTaken`] only for HTTP 409 with a code in
/// [`POSITION_TAKEN_CODES`]. Every HTTP 5xx, including 503 `SlowDown`, is
/// [`ObjectStoreErrorKind::OutcomeUnknown`], because the append may have been applied. Any
/// other 409, HTTP 429, and 4xx throttling codes are [`ObjectStoreErrorKind::Transient`]; other
/// statuses are [`ObjectStoreErrorKind::Permanent`].
pub(super) async fn read_append_response(
    key: &str,
    response: http::Response<Incoming>,
) -> ObjectStoreResult<AppendResponse> {
    let status = response.status();
    if status.is_success() {
        let etag = response
            .headers()
            .get(http::header::ETAG)
            .and_then(|value| value.to_str().ok())
            .map(str::to_owned);
        return Ok(AppendResponse::Appended { etag });
    }
    let (code, message) = read_error_document(key, response).await;
    let status_code = status.as_u16();
    if status_code == 409 && POSITION_TAKEN_CODES.contains(&code.as_str()) {
        return Ok(AppendResponse::PositionTaken);
    }
    let kind = match status_code {
        500..=599 => ObjectStoreErrorKind::OutcomeUnknown,
        409 | 429 => ObjectStoreErrorKind::Transient,
        _ if matches!(
            code.as_str(),
            "SlowDown" | "Throttling" | "ThrottlingException"
        ) =>
        {
            ObjectStoreErrorKind::Transient
        }
        _ => ObjectStoreErrorKind::Permanent,
    };
    Err(ObjectStoreError::new(
        ObjectStoreOperation::Put,
        key,
        kind,
        format!("AppendObject failed: HTTP {status_code} {code}: {message}"),
    ))
}

/// Returns the code and message of an S3 error document; both are empty when it is unreadable.
pub(super) async fn read_error_document(
    key: &str,
    response: http::Response<Incoming>,
) -> (String, String) {
    let body = match Limited::new(response.into_body(), MAX_ERROR_BODY_BYTES)
        .collect()
        .await
    {
        Ok(collected) => collected.to_bytes(),
        Err(error) => {
            // The status was received, so the store answered the request.
            tracing::debug!(key, %error, "reading an error document failed");
            bytes::Bytes::new()
        }
    };
    let text = String::from_utf8_lossy(&body);
    (
        xml_element(&text, "Code").unwrap_or("").to_owned(),
        xml_element(&text, "Message").unwrap_or("").to_owned(),
    )
}

/// Returns the text of the first `<name>` element of an S3 error document.
fn xml_element<'a>(document: &'a str, name: &str) -> Option<&'a str> {
    let open = format!("<{name}>");
    let start = document.find(&open)? + open.len();
    let end = document[start..].find(&format!("</{name}>"))? + start;
    Some(document[start..end].trim())
}
