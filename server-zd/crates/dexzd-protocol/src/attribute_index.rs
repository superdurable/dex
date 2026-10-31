// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use crate::dex::{IndexType, Value, value};

/// Infers the index type used when an Attribute's IndexConfig type is unspecified.
pub fn inferred_index_type(value: &Value) -> IndexType {
    match value.kind {
        Some(value::Kind::StringValue(_) | value::Kind::ObjValue(_)) => IndexType::Keyword,
        Some(value::Kind::IntValue(_)) => IndexType::Int,
        Some(value::Kind::DoubleValue(_)) => IndexType::Double,
        Some(value::Kind::BoolValue(_)) => IndexType::Bool,
        _ => IndexType::Unspecified,
    }
}
