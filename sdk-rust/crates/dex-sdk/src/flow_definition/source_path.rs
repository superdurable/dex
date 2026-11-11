// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::path::{Component, Path, PathBuf};

use serde::Deserialize;
use serde_json::value::RawValue;

use super::FlowDefinitionDocument;
use crate::{SdkError, SdkResult};

#[derive(Deserialize)]
struct DefinitionSourceField<'content> {
    #[serde(borrow, default)]
    source: Option<&'content RawValue>,
}

#[derive(Deserialize)]
struct SourcePathField<'content> {
    #[serde(borrow, default)]
    path: Option<&'content RawValue>,
}

/// Returns the documents with an absolute `source.path` made relative to `working_directory`.
///
/// # Errors
///
/// Returns [`SdkError::InvalidArgument`] when a document is not a UTF-8 JSON object.
pub(crate) fn rewrite_source_paths(
    documents: Vec<FlowDefinitionDocument>,
    working_directory: &Path,
) -> SdkResult<Vec<FlowDefinitionDocument>> {
    documents
        .into_iter()
        .map(|mut document| {
            if let Some(content) = rewrite_source_path(&document.content, working_directory)
                .map_err(|message| SdkError::InvalidArgument {
                    message: format!(
                        "Flow definition for Flow type {} {message}",
                        document.flow_type
                    ),
                })?
            {
                document.content = content;
            }
            Ok(document)
        })
        .collect()
}

/// Returns the working directory used to make `source.path` relative.
pub(crate) fn current_working_directory() -> Result<PathBuf, std::io::Error> {
    std::env::current_dir().map(|directory| lexically_normalize(&directory))
}

/// Returns new content when the top-level `source.path` string changes, or `None` otherwise.
///
/// Only the bytes of that string change, so key order, spacing, and numbers stay exact.
fn rewrite_source_path(
    content: &[u8],
    working_directory: &Path,
) -> Result<Option<Vec<u8>>, String> {
    let text = std::str::from_utf8(content).map_err(|error| format!("is not UTF-8: {error}"))?;
    let definition: DefinitionSourceField<'_> =
        serde_json::from_str(text).map_err(|error| format!("is not a JSON object: {error}"))?;
    let Some(source) = definition.source else {
        return Ok(None);
    };
    let Ok(SourcePathField {
        path: Some(raw_path),
    }) = serde_json::from_str::<SourcePathField<'_>>(source.get())
    else {
        // A source that is not an object, or has no path, carries no local path.
        return Ok(None);
    };
    let Ok(source_path) = serde_json::from_str::<String>(raw_path.get()) else {
        // A non-string path carries no local path.
        return Ok(None);
    };
    let relative_path = relative_source_path(&source_path, working_directory);
    if relative_path == source_path {
        return Ok(None);
    }
    let start = raw_path.get().as_ptr() as usize - text.as_ptr() as usize;
    let end = start + raw_path.get().len();
    let replacement = serde_json::to_string(&relative_path)
        .map_err(|error| format!("cannot encode source.path: {error}"))?;
    let mut rewritten = Vec::with_capacity(content.len());
    rewritten.extend_from_slice(&content[..start]);
    rewritten.extend_from_slice(replacement.as_bytes());
    rewritten.extend_from_slice(&content[end..]);
    Ok(Some(rewritten))
}

/// Returns an absolute path relative to the working directory when under it, else its file name.
///
/// A relative path is returned unchanged. Separators in the result are forward slashes.
fn relative_source_path(source_path: &str, working_directory: &Path) -> String {
    let path = Path::new(source_path);
    if !path.is_absolute() {
        return source_path.to_string();
    }
    let normalized = lexically_normalize(path);
    if let Ok(relative) = normalized.strip_prefix(working_directory)
        && !relative.as_os_str().is_empty()
    {
        return relative
            .components()
            .map(|component| component.as_os_str().to_string_lossy())
            .collect::<Vec<_>>()
            .join("/");
    }
    match normalized.file_name() {
        Some(file_name) => file_name.to_string_lossy().into_owned(),
        None => source_path
            .rsplit(['/', '\\'])
            .next()
            .unwrap_or_default()
            .to_string(),
    }
}

fn lexically_normalize(path: &Path) -> PathBuf {
    let mut normalized = PathBuf::new();
    for component in path.components() {
        match component {
            Component::CurDir => {}
            Component::ParentDir => {
                normalized.pop();
            }
            other => normalized.push(other.as_os_str()),
        }
    }
    normalized
}
