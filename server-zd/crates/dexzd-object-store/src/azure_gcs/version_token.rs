// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use object_store::UpdateVersion;

use super::AzureGcsCloud;
use crate::{ObjectStoreError, ObjectStoreErrorKind, ObjectStoreOperation, ObjectStoreResult};

/// Returns the ETag reported to callers for one object version.
///
/// Azure reports its ETag, always quoted. GCS reports the quoted generation, because its ETag is
/// a content hash that identical rewrites keep. The result is the same for writes, reads, and
/// listings of one version.
pub(crate) fn version_token(
    cloud: AzureGcsCloud,
    e_tag: Option<&str>,
    generation: Option<&str>,
    operation: ObjectStoreOperation,
    key: &str,
) -> ObjectStoreResult<String> {
    let (value, field) = match cloud {
        AzureGcsCloud::AzureBlob => (e_tag.map(unquote), "ETag"),
        AzureGcsCloud::GoogleCloudStorage => (generation, "generation"),
    };
    match value {
        Some(value) if is_token_body(cloud, value) => Ok(format!("\"{value}\"")),
        _ => Err(ObjectStoreError::new(
            operation,
            key,
            ObjectStoreErrorKind::Permanent,
            format!("response omitted a valid {field}: {value:?}"),
        )),
    }
}

/// Returns the precondition of a replacement, or `None` when `token` came from no version.
///
/// A token that no read or write returned cannot match, so the replacement fails its
/// precondition without a request.
pub(crate) fn update_version(cloud: AzureGcsCloud, token: &str) -> Option<UpdateVersion> {
    let body = token.strip_prefix('"')?.strip_suffix('"')?;
    if !is_token_body(cloud, body) {
        return None;
    }
    Some(match cloud {
        AzureGcsCloud::AzureBlob => UpdateVersion {
            e_tag: Some(token.to_owned()),
            version: None,
        },
        AzureGcsCloud::GoogleCloudStorage => UpdateVersion {
            e_tag: None,
            version: Some(body.to_owned()),
        },
    })
}

fn unquote(e_tag: &str) -> &str {
    e_tag
        .strip_prefix('"')
        .and_then(|inner| inner.strip_suffix('"'))
        .unwrap_or(e_tag)
}

/// Azure ETags must be header-safe; GCS generations are decimal integers.
fn is_token_body(cloud: AzureGcsCloud, body: &str) -> bool {
    match cloud {
        AzureGcsCloud::AzureBlob => {
            !body.is_empty()
                && body
                    .bytes()
                    .all(|byte| byte.is_ascii_graphic() && byte != b'"')
        }
        AzureGcsCloud::GoogleCloudStorage => {
            !body.is_empty() && body.len() <= 20 && body.bytes().all(|byte| byte.is_ascii_digit())
        }
    }
}
