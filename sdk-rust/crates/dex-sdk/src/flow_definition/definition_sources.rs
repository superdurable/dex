// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::{BTreeMap, BTreeSet};
use std::path::{Path, PathBuf};
use std::time::SystemTime;

use serde_json::Value as JsonValue;

use super::FlowDefinitionDocument;
use crate::{SdkError, SdkResult};

const IN_MEMORY_SOURCE: &str = "an in-memory FlowDefinitionDocument";

/// Returns in-memory documents followed by readable file documents, one per Flow type.
///
/// Unreadable or non-definition files are logged and skipped.
///
/// # Errors
///
/// Returns [`SdkError::FlowDefinition`] when two sources share a Flow type, or an in-memory
/// document is not JSON or its `flow.name` differs from its Flow type.
pub(crate) fn collect_flow_definitions(
    paths: Vec<PathBuf>,
    documents: Vec<FlowDefinitionDocument>,
) -> SdkResult<Vec<FlowDefinitionDocument>> {
    let mut source_by_flow_type: BTreeMap<String, String> = BTreeMap::new();
    let mut collected = Vec::with_capacity(documents.len());
    for document in documents {
        require_matching_flow_name(&document)?;
        record_source(
            &mut source_by_flow_type,
            &document.flow_type,
            IN_MEMORY_SOURCE.to_string(),
        )?;
        collected.push(document);
    }
    for file in list_definition_files(&paths) {
        if let Some(document) = read_definition_file(&file) {
            record_source(
                &mut source_by_flow_type,
                &document.flow_type,
                format!("file {}", file.display()),
            )?;
            collected.push(document);
        }
    }
    Ok(collected)
}

fn require_matching_flow_name(document: &FlowDefinitionDocument) -> SdkResult<()> {
    let root: JsonValue =
        serde_json::from_slice(&document.content).map_err(|error| SdkError::FlowDefinition {
            message: format!(
                "Flow definition for Flow type {} is not valid JSON: {error}",
                document.flow_type
            ),
        })?;
    let flow_name = read_flow_name(&root);
    if flow_name != Some(document.flow_type.as_str()) {
        return Err(SdkError::FlowDefinition {
            message: format!(
                "Flow definition for Flow type {} has flow.name {}; they must be equal",
                document.flow_type,
                flow_name.unwrap_or("<missing>")
            ),
        });
    }
    Ok(())
}

fn record_source(
    source_by_flow_type: &mut BTreeMap<String, String>,
    flow_type: &str,
    source: String,
) -> SdkResult<()> {
    if let Some(previous) = source_by_flow_type.get(flow_type) {
        return Err(SdkError::FlowDefinition {
            message: format!(
                "Flow type {flow_type} has more than one Flow definition: {previous} and {source}"
            ),
        });
    }
    source_by_flow_type.insert(flow_type.to_string(), source);
    Ok(())
}

/// Returns configured files and the `*.json` files under configured directories, each once.
fn list_definition_files(paths: &[PathBuf]) -> Vec<PathBuf> {
    let mut seen = BTreeSet::new();
    let mut files = Vec::new();
    for configured in paths {
        // Without a working directory, the configured path still identifies the file.
        let path = std::path::absolute(configured).unwrap_or_else(|_| configured.clone());
        let mut found = Vec::new();
        if path.is_dir() {
            collect_json_files(&path, &mut found);
            found.sort();
        } else if path.is_file() {
            found.push(path);
        } else {
            tracing::warn!(
                path = %configured.display(),
                "Flow definition path is not a readable file or directory; skipping it"
            );
        }
        for file in found {
            if seen.insert(file.clone()) {
                files.push(file);
            }
        }
    }
    files
}

fn collect_json_files(directory: &Path, files: &mut Vec<PathBuf>) {
    let entries = match std::fs::read_dir(directory) {
        Ok(entries) => entries,
        Err(error) => {
            tracing::warn!(
                directory = %directory.display(),
                %error,
                "cannot list Flow definition directory; skipping it"
            );
            return;
        }
    };
    for entry in entries {
        let entry = match entry {
            Ok(entry) => entry,
            Err(error) => {
                tracing::warn!(
                    directory = %directory.display(),
                    %error,
                    "cannot list Flow definition directory entry; skipping it"
                );
                continue;
            }
        };
        let path = entry.path();
        let is_directory = entry.file_type().is_ok_and(|file_type| file_type.is_dir());
        if is_directory {
            collect_json_files(&path, files);
        } else if path.is_file() && has_json_extension(&path) {
            files.push(path);
        }
    }
}

fn has_json_extension(path: &Path) -> bool {
    path.extension()
        .is_some_and(|extension| extension.eq_ignore_ascii_case("json"))
}

fn read_definition_file(file: &Path) -> Option<FlowDefinitionDocument> {
    let content = match std::fs::read(file) {
        Ok(content) => content,
        Err(error) => {
            tracing::warn!(file = %file.display(), %error, "cannot read Flow definition file; skipping it");
            return None;
        }
    };
    let root: JsonValue = match serde_json::from_slice(&content) {
        Ok(root) => root,
        Err(error) => {
            tracing::warn!(file = %file.display(), %error, "Flow definition file is not valid JSON; skipping it");
            return None;
        }
    };
    let Some(flow_type) = read_flow_name(&root)
        .filter(|name| !name.trim().is_empty())
        .map(str::to_string)
    else {
        tracing::info!(file = %file.display(), "file has no flow.name, so it is not a Flow definition; skipping it");
        return None;
    };
    let generated_at = read_generated_at(file, &root)?;
    Some(FlowDefinitionDocument {
        flow_type,
        content,
        generated_at,
    })
}

fn read_flow_name(root: &JsonValue) -> Option<&str> {
    root.get("flow")?.get("name")?.as_str()
}

fn read_generated_at(file: &Path, root: &JsonValue) -> Option<SystemTime> {
    if let Some(generated_at) = root.get("generatedAt").and_then(JsonValue::as_str) {
        match generated_at
            .parse::<prost_types::Timestamp>()
            .map_err(|error| error.to_string())
            .and_then(|timestamp| {
                SystemTime::try_from(timestamp).map_err(|error| error.to_string())
            }) {
            Ok(time) => return Some(time),
            Err(error) => tracing::warn!(
                file = %file.display(),
                generated_at,
                %error,
                "Flow definition file has an invalid generatedAt; using the file modification time"
            ),
        }
    }
    match std::fs::metadata(file).and_then(|metadata| metadata.modified()) {
        Ok(modified) => Some(modified),
        Err(error) => {
            tracing::warn!(
                file = %file.display(),
                %error,
                "cannot read the modification time of a Flow definition file; skipping it"
            );
            None
        }
    }
}
