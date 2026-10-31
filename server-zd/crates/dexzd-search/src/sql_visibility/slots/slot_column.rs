// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::fmt;

use crate::SearchError;
use dexzd_protocol::dex::IndexType;

/// A validated PostgreSQL column reserved for one typed Attribute slot.
#[derive(Debug, Clone, PartialEq, Eq, PartialOrd, Ord)]
pub struct AttributeSlotColumn(String);

impl AttributeSlotColumn {
    pub(crate) fn for_registration(
        index_type: IndexType,
        position: usize,
    ) -> Result<Self, SearchError> {
        let (prefix, capacity, _) = slot_type(index_type)?;
        if !(1..=capacity).contains(&position) {
            return Err(SearchError::Internal(format!(
                "invalid {index_type:?} Attribute slot position {position}"
            )));
        }
        Ok(Self(format!("{prefix}_{position:02}")))
    }

    pub(crate) fn from_database(column: &str) -> Result<Self, SearchError> {
        for index_type in INDEX_TYPES {
            let (_, capacity, _) = slot_type(index_type)?;
            for position in 1..=capacity {
                let candidate = Self::for_registration(index_type, position)?;
                if candidate.as_str() == column {
                    return Ok(candidate);
                }
            }
        }
        Err(SearchError::Internal(format!(
            "unknown PostgreSQL Attribute slot column {column:?}; use a new database or schema"
        )))
    }

    /// Borrows the validated SQL column identifier.
    pub fn as_str(&self) -> &str {
        &self.0
    }

    pub(crate) fn index_type(&self) -> IndexType {
        let (prefix, _) = self
            .0
            .rsplit_once('_')
            .expect("validated slot columns include a position");
        INDEX_TYPES
            .into_iter()
            .find(|index_type| {
                slot_type(*index_type).expect("slot types are specified").0 == prefix
            })
            .expect("validated columns have a supported type")
    }
}

impl fmt::Display for AttributeSlotColumn {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(&self.0)
    }
}

pub(crate) const INDEX_TYPES: [IndexType; 7] = [
    IndexType::Keyword,
    IndexType::Int,
    IndexType::Datetime,
    IndexType::Double,
    IndexType::Bool,
    IndexType::KeywordArray,
    IndexType::Text,
];

pub(crate) fn slot_type(
    index_type: IndexType,
) -> Result<(&'static str, usize, &'static str), SearchError> {
    match index_type {
        IndexType::Keyword => Ok(("keyword", 10, "text")),
        IndexType::Int => Ok(("int", 3, "bigint")),
        IndexType::Datetime => Ok(("datetime", 3, "bigint")),
        IndexType::Double => Ok(("double", 3, "double precision")),
        IndexType::Bool => Ok(("bool", 3, "boolean")),
        IndexType::KeywordArray => Ok(("keyword_array", 3, "text[]")),
        IndexType::Text => Ok(("text_terms", 3, "text[]")),
        IndexType::Unspecified => Err(SearchError::Internal(
            "unspecified Attribute index type in registry".into(),
        )),
    }
}
