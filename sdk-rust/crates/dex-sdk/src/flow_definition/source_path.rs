// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::fmt::Write as _;

use serde::de::{MapAccess, Visitor};
use serde::{Deserialize, Deserializer};
use serde_json::value::RawValue;

use super::FlowDefinitionDocument;
use crate::{SdkError, SdkResult};

/// Holds an object's members in order, keeping duplicate keys.
struct ObjectMembers<'content>(Vec<(String, &'content RawValue)>);

impl<'de> Deserialize<'de> for ObjectMembers<'de> {
    fn deserialize<D: Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        deserializer.deserialize_map(ObjectMembersVisitor)
    }
}

struct ObjectMembersVisitor;

impl<'de> Visitor<'de> for ObjectMembersVisitor {
    type Value = ObjectMembers<'de>;

    fn expecting(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter.write_str("a JSON object")
    }

    fn visit_map<A: MapAccess<'de>>(self, mut map: A) -> Result<Self::Value, A::Error> {
        let mut members = Vec::new();
        while let Some(member) = map.next_entry::<String, &'de RawValue>()? {
            members.push(member);
        }
        Ok(ObjectMembers(members))
    }
}

impl<'content> ObjectMembers<'content> {
    /// Returns the value of `key` when it appears exactly once.
    fn only(&self, key: &str) -> Option<&'content RawValue> {
        let mut matches = self.0.iter().filter(|(name, _)| name == key);
        let (_, value) = matches.next()?;
        matches.next().is_none().then_some(*value)
    }
}

/// Returns the documents with an absolute `source.path` made relative to `working_directory`.
///
/// A `None` working directory is unknown, so absolute paths become base names.
///
/// # Errors
///
/// Returns [`SdkError::InvalidArgument`] when a document is not a UTF-8 JSON object.
pub(crate) fn rewrite_source_paths(
    documents: Vec<FlowDefinitionDocument>,
    working_directory: Option<&str>,
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

/// Returns the process working directory, or `None` when the OS cannot report it as UTF-8.
pub(crate) fn current_working_directory() -> Option<String> {
    // An unknown working directory is valid: absolute paths then become base names.
    let directory = std::env::current_dir().ok()?;
    directory.to_str().map(str::to_string)
}

/// Returns new content when the top-level `source.path` string changes, or `None` otherwise.
///
/// Only the bytes of that string change, so key order, spacing, and numbers stay exact.
pub(super) fn rewrite_source_path(
    content: &[u8],
    working_directory: Option<&str>,
) -> Result<Option<Vec<u8>>, String> {
    let text = std::str::from_utf8(content).map_err(|error| format!("is not UTF-8: {error}"))?;
    let definition: ObjectMembers<'_> =
        serde_json::from_str(text).map_err(|error| format!("is not a JSON object: {error}"))?;
    // Only one top-level source object with one string path is rewritten; anything else is sent as is.
    let Some(raw_path) = definition
        .only("source")
        .and_then(|source| serde_json::from_str::<ObjectMembers<'_>>(source.get()).ok())
        .and_then(|source| source.only("path"))
    else {
        return Ok(None);
    };
    let Ok(source_path) = serde_json::from_str::<String>(raw_path.get()) else {
        return Ok(None);
    };
    let Some(relative_path) = relative_source_path(&source_path, working_directory) else {
        return Ok(None);
    };
    if relative_path == source_path {
        return Ok(None);
    }
    let start = raw_path.get().as_ptr() as usize - text.as_ptr() as usize;
    let end = start + raw_path.get().len();
    let mut rewritten = Vec::with_capacity(content.len());
    rewritten.extend_from_slice(&content[..start]);
    rewritten.extend_from_slice(encode_json_string(&relative_path).as_bytes());
    rewritten.extend_from_slice(&content[end..]);
    Ok(Some(rewritten))
}

/// Returns whether a source path is absolute on any operating system.
fn is_absolute_source_path(path: &str) -> bool {
    let bytes = path.as_bytes();
    path.starts_with('/')
        || path.starts_with("\\\\")
        || (bytes.len() >= 3
            && bytes[0].is_ascii_alphabetic()
            && bytes[1] == b':'
            && (bytes[2] == b'\\' || bytes[2] == b'/'))
}

/// Returns the rewritten source path, or `None` when it is relative and stays unchanged.
///
/// Inside the working directory the result is the remaining segments joined with `/`;
/// elsewhere, or when the working directory is unknown, it is the base name.
pub(super) fn relative_source_path(
    source_path: &str,
    working_directory: Option<&str>,
) -> Option<String> {
    if !is_absolute_source_path(source_path) {
        return None;
    }
    let path = lexical_segments(source_path);
    if let Some(directory) =
        working_directory.filter(|directory| is_absolute_source_path(directory))
    {
        let directory = lexical_segments(directory);
        if path.len() > directory.len() && path[..directory.len()] == directory[..] {
            return Some(path[directory.len()..].join("/"));
        }
    }
    Some(if path.len() > 1 {
        path[path.len() - 1].clone()
    } else {
        String::new()
    })
}

/// Returns the root marker followed by the lexically normalized segments of an absolute path.
fn lexical_segments(absolute_path: &str) -> Vec<String> {
    let (root, rest) = if let Some(rest) = absolute_path.strip_prefix("\\\\") {
        ("//".to_string(), rest)
    } else if let Some(rest) = absolute_path.strip_prefix('/') {
        ("/".to_string(), rest)
    } else {
        (
            format!("{}:", absolute_path[..1].to_ascii_uppercase()),
            &absolute_path[3..],
        )
    };
    let mut segments = vec![root];
    for segment in rest.split(['/', '\\']) {
        match segment {
            "" | "." => {}
            ".." => {
                if segments.len() > 1 {
                    segments.pop();
                }
            }
            other => segments.push(other.to_string()),
        }
    }
    segments
}

/// Encodes a JSON string escaping only quote, backslash, and control characters.
pub(super) fn encode_json_string(value: &str) -> String {
    let mut encoded = String::with_capacity(value.len() + 2);
    encoded.push('"');
    for character in value.chars() {
        match character {
            '"' => encoded.push_str("\\\""),
            '\\' => encoded.push_str("\\\\"),
            '\u{8}' => encoded.push_str("\\b"),
            '\u{c}' => encoded.push_str("\\f"),
            '\n' => encoded.push_str("\\n"),
            '\r' => encoded.push_str("\\r"),
            '\t' => encoded.push_str("\\t"),
            control if u32::from(control) < 0x20 => {
                write!(encoded, "\\u{:04x}", u32::from(control))
                    .expect("writing to a String cannot fail");
            }
            other => encoded.push(other),
        }
    }
    encoded.push('"');
    encoded
}
