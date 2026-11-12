// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Classification of AppendObject responses and transport failures.

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
/// [`POSITION_TAKEN_CODES`]. Any other 409, HTTP 429, and throttling codes are
/// [`ObjectStoreErrorKind::Transient`]; HTTP 5xx and an unreadable success are
/// [`ObjectStoreErrorKind::OutcomeUnknown`]; other statuses are
/// [`ObjectStoreErrorKind::Permanent`].
pub(super) async fn read_append_response(
    key: &str,
    response: reqwest::Response,
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
    let body = match response.bytes().await {
        Ok(body) => body,
        Err(error) => {
            // A non-success status was received, so the store refused the request.
            tracing::debug!(key, %error, "reading an AppendObject error body failed");
            bytes::Bytes::new()
        }
    };
    let text = String::from_utf8_lossy(&body[..body.len().min(MAX_ERROR_BODY_BYTES)]);
    let code = xml_element(&text, "Code").unwrap_or("");
    let message = xml_element(&text, "Message").unwrap_or("");
    let status_code = status.as_u16();
    if status_code == 409 && POSITION_TAKEN_CODES.contains(&code) {
        return Ok(AppendResponse::PositionTaken);
    }
    let kind = match status_code {
        409 | 429 => ObjectStoreErrorKind::Transient,
        _ if matches!(code, "SlowDown" | "Throttling" | "ThrottlingException") => {
            ObjectStoreErrorKind::Transient
        }
        500..=599 => ObjectStoreErrorKind::OutcomeUnknown,
        _ => ObjectStoreErrorKind::Permanent,
    };
    Err(ObjectStoreError::new(
        ObjectStoreOperation::Put,
        key,
        kind,
        format!("AppendObject failed: HTTP {status_code} {code}: {message}"),
    ))
}

/// Maps a request that got no response; only a failed connection proves nothing was sent.
pub(super) fn map_send_error(key: &str, error: &reqwest::Error) -> ObjectStoreError {
    let kind = if error.is_builder() {
        ObjectStoreErrorKind::Permanent
    } else if error.is_connect() {
        ObjectStoreErrorKind::Transient
    } else {
        ObjectStoreErrorKind::OutcomeUnknown
    };
    ObjectStoreError::new(
        ObjectStoreOperation::Put,
        key,
        kind,
        format!("sending AppendObject: {error}"),
    )
}

/// Returns the text of the first `<name>` element of an S3 error document.
fn xml_element<'a>(document: &'a str, name: &str) -> Option<&'a str> {
    let open = format!("<{name}>");
    let start = document.find(&open)? + open.len();
    let end = document[start..].find(&format!("</{name}>"))? + start;
    Some(document[start..end].trim())
}
