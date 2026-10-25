// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! CopyObject: copies one stored version to a new key, keeping or replacing its metadata.

use std::sync::Arc;

use http::{HeaderMap, StatusCode};
use s3mock::{CopyObjectRequest, MetadataDirective, Store, StoreError};

use crate::request_headers::{
    ObjectWrite, content_headers, copy_source_conditions, header_text,
    refuse_unsupported_write_headers, requested_checksum_algorithm, user_metadata, write_condition,
};
use crate::routing::CopySource;
use crate::s3_error::S3Error;
use crate::server::{S3Response, header_value, run_store_operation, xml_response};
use crate::xml::{XmlWriter, format_iso8601};

/// `should_hide_missing_source` answers 403 for a missing source, as GetObject does without
/// list permission.
pub(crate) async fn copy_object(
    store: Arc<Store>,
    bucket: String,
    key: String,
    source: CopySource,
    headers: &HeaderMap,
    should_hide_missing_source: bool,
) -> Result<S3Response, S3Error> {
    refuse_unsupported_write_headers(headers, ObjectWrite::CopyObject)?;
    let request = CopyObjectRequest {
        source_bucket: source.bucket,
        source_key: source.key,
        source_version_id: source.version_id,
        source_conditions: copy_source_conditions(headers),
        metadata_directive: metadata_directive(headers)?,
        checksum_algorithm: requested_checksum_algorithm(headers)?,
        write_condition: write_condition(headers, "CopyObject")?,
    };
    let (source_bucket, source_key) = (request.source_bucket.clone(), request.source_key.clone());
    let copied = run_store_operation(&store, move |store| {
        Ok(store.copy_object(&bucket, &key, request))
    })
    .await?;
    let outcome = match copied {
        Ok(outcome) => outcome,
        Err(StoreError::NoSuchKey {
            bucket: missing_bucket,
            key: missing_key,
        }) if should_hide_missing_source
            && missing_bucket == source_bucket
            && missing_key == source_key =>
        {
            return Err(S3Error::new(
                StatusCode::FORBIDDEN,
                "AccessDenied",
                "the credential cannot discover whether the copy source exists",
            ));
        }
        Err(error) => return Err(S3Error::from(error)),
    };
    let destination = &outcome.destination;
    let checksum = &destination.checksum;
    let mut document = XmlWriter::new("CopyObjectResult");
    document
        .text_element("ETag", &format!("\"{}\"", destination.etag))
        .text_element(
            "LastModified",
            &format_iso8601(destination.last_modified_unix_ms),
        )
        .text_element("ChecksumType", checksum.checksum_type.s3_name())
        .text_element(
            checksum.algorithm.xml_element_name(),
            &checksum.base64_value,
        );
    let mut response = xml_response(document.finish("CopyObjectResult"))?;
    let response_headers = response.headers_mut();
    if let Some(version_id) = &destination.version_id {
        response_headers.insert("x-amz-version-id", header_value(version_id));
    }
    if let Some(version_id) = &outcome.source_version_id {
        response_headers.insert("x-amz-copy-source-version-id", header_value(version_id));
    }
    Ok(response)
}

/// Parses `x-amz-metadata-directive`; `COPY`, the default, ignores the request's own metadata.
fn metadata_directive(headers: &HeaderMap) -> Result<MetadataDirective, S3Error> {
    match header_text(headers, "x-amz-metadata-directive").map(str::trim) {
        None | Some("COPY") => Ok(MetadataDirective::Copy),
        Some("REPLACE") => Ok(MetadataDirective::Replace {
            content_headers: content_headers(headers),
            user_metadata: user_metadata(headers),
        }),
        Some(other) => Err(S3Error::invalid_argument(format!(
            "unknown metadata directive {other:?}"
        ))),
    }
}
