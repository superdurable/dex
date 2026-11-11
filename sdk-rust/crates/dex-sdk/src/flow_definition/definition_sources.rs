// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::{BTreeMap, BTreeSet};
use std::path::{Path, PathBuf};

use serde_json::Value as JsonValue;

use super::FlowDefinitionDocument;
use super::generated_at::{parse_generated_at, truncate_to_millis};
use crate::{SdkError, SdkResult};

const IN_MEMORY_SOURCE: &str = "an in-memory FlowDefinitionDocument";

/// Returns in-memory documents followed by file documents, one per Flow type.
///
/// A directory entry that is not a Flow Definition Graph 2.x is logged and skipped.
///
/// # Errors
///
/// Returns [`SdkError::FlowDefinition`] for a missing or unreadable path, an invalid explicit
/// file, a malformed `generatedAt`, a Flow type configured twice, or an in-memory document that is
/// not a Flow Definition Graph 2.x naming its Flow type.
pub(crate) fn collect_flow_definitions(
    paths: Vec<PathBuf>,
    documents: Vec<FlowDefinitionDocument>,
) -> SdkResult<Vec<FlowDefinitionDocument>> {
    let mut source_by_flow_type: BTreeMap<String, String> = BTreeMap::new();
    let mut collected = Vec::with_capacity(documents.len());
    for document in documents {
        validate_document(&document).map_err(configuration_error)?;
        record_source(
            &mut source_by_flow_type,
            &document.flow_type,
            IN_MEMORY_SOURCE.to_string(),
        )?;
        collected.push(document);
    }
    let mut seen = BTreeSet::new();
    for configured in &paths {
        // Without a working directory, the configured path still identifies the file.
        let path = std::path::absolute(configured).unwrap_or_else(|_| configured.clone());
        let candidates = if path.is_dir() {
            let mut files = Vec::new();
            collect_json_files(&path, &mut files)?;
            files.sort();
            files
                .into_iter()
                .map(|file| (file, FileSelection::DirectoryEntry))
                .collect()
        } else if path.is_file() {
            vec![(path, FileSelection::Explicit)]
        } else {
            return Err(configuration_error(format!(
                "Flow definition path {} does not exist or cannot be read",
                configured.display()
            )));
        };
        for (file, selection) in candidates {
            if !seen.insert(file.clone()) {
                continue;
            }
            if let Some(document) = read_definition_file(&file, selection)? {
                record_source(
                    &mut source_by_flow_type,
                    &document.flow_type,
                    format!("file {}", file.display()),
                )?;
                collected.push(document);
            }
        }
    }
    Ok(collected)
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum FileSelection {
    Explicit,
    DirectoryEntry,
}

fn configuration_error(message: String) -> SdkError {
    SdkError::FlowDefinition { message }
}

/// Returns why an in-memory document is not a Flow Definition Graph 2.x naming its Flow type.
pub(crate) fn validate_document(document: &FlowDefinitionDocument) -> Result<(), String> {
    let (flow_name, _) = inspect_definition(&document.content).map_err(|reason| {
        format!(
            "Flow definition for Flow type {} {reason}",
            document.flow_type
        )
    })?;
    if flow_name != document.flow_type {
        return Err(format!(
            "Flow definition for Flow type {} has flow.name {flow_name}; they must be equal",
            document.flow_type
        ));
    }
    Ok(())
}

fn record_source(
    source_by_flow_type: &mut BTreeMap<String, String>,
    flow_type: &str,
    source: String,
) -> SdkResult<()> {
    if let Some(previous) = source_by_flow_type.get(flow_type) {
        return Err(configuration_error(format!(
            "Flow type {flow_type} has more than one Flow definition: {previous} and {source}"
        )));
    }
    source_by_flow_type.insert(flow_type.to_string(), source);
    Ok(())
}

fn collect_json_files(directory: &Path, files: &mut Vec<PathBuf>) -> SdkResult<()> {
    let unreadable = |error: std::io::Error| {
        configuration_error(format!(
            "Flow definition directory {} cannot be read: {error}",
            directory.display()
        ))
    };
    for entry in std::fs::read_dir(directory).map_err(unreadable)? {
        let entry = entry.map_err(unreadable)?;
        let path = entry.path();
        let is_directory = entry.file_type().map_err(unreadable)?.is_dir();
        if is_directory {
            collect_json_files(&path, files)?;
        } else if path.is_file() && has_json_extension(&path) {
            files.push(path);
        }
    }
    Ok(())
}

fn has_json_extension(path: &Path) -> bool {
    path.extension()
        .is_some_and(|extension| extension.eq_ignore_ascii_case("json"))
}

fn read_definition_file(
    file: &Path,
    selection: FileSelection,
) -> SdkResult<Option<FlowDefinitionDocument>> {
    let content = std::fs::read(file).map_err(|error| {
        configuration_error(format!(
            "Flow definition file {} cannot be read: {error}",
            file.display()
        ))
    })?;
    let (flow_type, generated_at) = match inspect_definition(&content) {
        Ok(fields) => fields,
        Err(reason) if selection == FileSelection::DirectoryEntry => {
            tracing::info!(file = %file.display(), reason, "skipping a file that is not a Flow definition");
            return Ok(None);
        }
        Err(reason) => {
            return Err(configuration_error(format!(
                "Flow definition file {} {reason}",
                file.display()
            )));
        }
    };
    let generated_at = match generated_at {
        None => std::fs::metadata(file)
            .and_then(|metadata| metadata.modified())
            .map(truncate_to_millis)
            .map_err(|error| {
                configuration_error(format!(
                    "cannot read the modification time of Flow definition file {}: {error}",
                    file.display()
                ))
            })?,
        Some(JsonValue::String(value)) => parse_generated_at(&value).map_err(|message| {
            configuration_error(format!(
                "Flow definition file {}: {message}",
                file.display()
            ))
        })?,
        Some(_) => {
            return Err(configuration_error(format!(
                "Flow definition file {} has a generatedAt that is not a string",
                file.display()
            )));
        }
    };
    Ok(Some(FlowDefinitionDocument {
        flow_type,
        content,
        generated_at,
    }))
}

/// Returns the Flow type and raw `generatedAt` of a Flow Definition Graph 2.x, or why it is not one.
fn inspect_definition(content: &[u8]) -> Result<(String, Option<JsonValue>), &'static str> {
    let not_object = "is not a UTF-8 JSON object";
    let text = std::str::from_utf8(content).map_err(|_| not_object)?;
    let JsonValue::Object(mut root) = serde_json::from_str(text).map_err(|_| not_object)? else {
        return Err(not_object);
    };
    let is_version_2 = root
        .get("schemaVersion")
        .and_then(JsonValue::as_str)
        .is_some_and(|version| version.starts_with("2."));
    if !is_version_2 {
        return Err("has no schemaVersion starting with 2.");
    }
    let flow_type = root
        .get("flow")
        .and_then(|flow| flow.get("name"))
        .and_then(JsonValue::as_str)
        .filter(|name| !name.is_empty())
        .ok_or("has no flow.name")?
        .to_string();
    Ok((flow_type, root.remove("generatedAt")))
}
