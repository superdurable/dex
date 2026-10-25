// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeSet;

use unicode_properties::GeneralCategory;
use unicode_properties::general_category::UnicodeGeneralCategory;

use crate::settings::ColumnNaming;

const ROW_KEY_WILDCARD: &str = "*";

/// Splits a Flow ID into primary-key column values, such as `release-{project_id}-{release_id}`.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) struct RowKeyTemplate {
    segments: Vec<RowKeySegment>,
}

/// Literal text, or a placeholder; a wildcard placeholder has no column.
#[derive(Clone, Debug, PartialEq, Eq)]
enum RowKeySegment {
    Literal(String),
    Placeholder { column: Option<String> },
}

impl RowKeyTemplate {
    /// Parses a template with Go's `parseRowKeyTemplate` rules and error messages.
    pub(crate) fn parse(template: &str) -> Result<Self, String> {
        let mut segments = Vec::new();
        let mut seen_columns = BTreeSet::new();
        let mut remaining = template;
        while !remaining.is_empty() {
            let Some(opening) = remaining.find(['{', '}']) else {
                segments.push(RowKeySegment::Literal(remaining.to_owned()));
                break;
            };
            if remaining.as_bytes()[opening] == b'}' {
                return Err("rowKey has an unmatched '}'".to_owned());
            }
            if opening > 0 {
                segments.push(RowKeySegment::Literal(remaining[..opening].to_owned()));
            }
            let after_opening = &remaining[opening + 1..];
            let closing = match after_opening.find(['{', '}']) {
                Some(closing) if after_opening.as_bytes()[closing] == b'}' => closing,
                _ => return Err("rowKey has an unterminated placeholder".to_owned()),
            };
            let name = &after_opening[..closing];
            if name.is_empty() {
                return Err("rowKey has an empty placeholder".to_owned());
            }
            if matches!(segments.last(), Some(RowKeySegment::Placeholder { .. })) {
                return Err("rowKey placeholders must be separated by literal text".to_owned());
            }
            let column = if name == ROW_KEY_WILDCARD {
                None
            } else {
                if !seen_columns.insert(name.to_owned()) {
                    return Err(format!("rowKey names column {name:?} more than once"));
                }
                Some(name.to_owned())
            };
            segments.push(RowKeySegment::Placeholder { column });
            remaining = &after_opening[closing + 1..];
        }
        if seen_columns.is_empty() {
            return Err("rowKey must name at least one column".to_owned());
        }
        Ok(Self { segments })
    }

    /// Returns the named placeholders in template order.
    pub(crate) fn columns(&self) -> Vec<String> {
        self.segments
            .iter()
            .filter_map(|segment| match segment {
                RowKeySegment::Placeholder { column } => column.clone(),
                RowKeySegment::Literal(_) => None,
            })
            .collect()
    }

    /// Returns the value of each named placeholder in template order, or None when the Flow ID does not
    /// match. A placeholder captures up to the next literal text, the last one captures the rest, and no
    /// capture is empty.
    pub(crate) fn match_flow_id(&self, flow_id: &str) -> Option<Vec<String>> {
        let mut values = Vec::new();
        let mut position = 0;
        for (index, segment) in self.segments.iter().enumerate() {
            let column = match segment {
                RowKeySegment::Literal(literal) => {
                    if !flow_id[position..].starts_with(literal.as_str()) {
                        return None;
                    }
                    position += literal.len();
                    continue;
                }
                RowKeySegment::Placeholder { column } => column,
            };
            let end = match self.segments.get(index + 1) {
                Some(RowKeySegment::Literal(next)) => {
                    position + flow_id[position..].find(next.as_str())?
                }
                _ => flow_id.len(),
            };
            if end == position {
                return None;
            }
            if column.is_some() {
                values.push(flow_id[position..end].to_owned());
            }
            position = end;
        }
        (position == flow_id.len()).then_some(values)
    }
}

impl ColumnNaming {
    /// Returns the column that stores an Attribute or map value field under this rule.
    pub(crate) fn column_name(self, name: &str) -> String {
        match self {
            Self::AsIs => name.to_owned(),
            Self::SnakeCase => snake_case(name),
        }
    }
}

/// Go's `snakeCase`: `DeploymentURL` becomes `deployment_url`, and `S3Bucket` becomes `s3_bucket`.
pub(crate) fn snake_case(name: &str) -> String {
    let characters = name.chars().collect::<Vec<_>>();
    let mut result = String::with_capacity(name.len() + 4);
    for (index, &character) in characters.iter().enumerate() {
        if index > 0 && is_go_upper(character) {
            let previous = characters[index - 1];
            let ends_acronym = is_go_upper(previous)
                && characters
                    .get(index + 1)
                    .is_some_and(|next| is_go_lower(*next));
            if is_go_lower(previous) || is_go_digit(previous) || ends_acronym {
                result.push('_');
            }
        }
        result.push(go_to_lower(character));
    }
    result
}

/// Go's `unicode.IsUpper`: General_Category Lu, unlike Rust's broader Uppercase property.
fn is_go_upper(character: char) -> bool {
    character.general_category() == GeneralCategory::UppercaseLetter
}

/// Go's `unicode.IsLower`: General_Category Ll, unlike Rust's broader Lowercase property.
fn is_go_lower(character: char) -> bool {
    character.general_category() == GeneralCategory::LowercaseLetter
}

/// Go's `unicode.IsDigit`: ASCII digits in Latin-1, otherwise any decimal digit.
fn is_go_digit(character: char) -> bool {
    character.general_category() == GeneralCategory::DecimalNumber
}

/// Go's `unicode.ToLower` uses the simple mapping, which is the first character of the full mapping.
fn go_to_lower(character: char) -> char {
    character.to_lowercase().next().unwrap_or(character)
}
