// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dex_protocol::dex::{AttributeMatch as ProtoAttributeMatch, AttributeMatchOperator, value};

use crate::{SdkError, SdkResult, Value, value_mapper};

/// Describes a scalar Attribute predicate.
///
/// Create a match with an associated factory and pass it to
/// [`crate::Client::wait_for_attribute_match`] or
/// [`crate::Client::wait_for_attribute_map_instance_match`]. String and Boolean
/// Attributes support equality operators. Integer and floating-point Attributes
/// support every operator. A missing Attribute never matches.
///
/// # Examples
///
/// ```
/// use dex_sdk::AttributeMatch;
///
/// let changed = AttributeMatch::greater_than(3_i64);
/// ```
pub struct AttributeMatch<T> {
    operator: AttributeMatchOperator,
    operand: T,
}

impl<T> AttributeMatch<T> {
    /// Creates an equality match.
    pub fn equal_to(operand: T) -> Self {
        Self::new(AttributeMatchOperator::Equal, operand)
    }

    /// Creates a not-equal match. A missing Attribute does not match.
    pub fn not_equal_to(operand: T) -> Self {
        Self::new(AttributeMatchOperator::NotEqual, operand)
    }

    /// Creates a numeric greater-than match.
    pub fn greater_than(operand: T) -> Self {
        Self::new(AttributeMatchOperator::GreaterThan, operand)
    }

    /// Creates a numeric greater-than-or-equal match.
    pub fn greater_than_or_equal(operand: T) -> Self {
        Self::new(AttributeMatchOperator::GreaterThanOrEqual, operand)
    }

    /// Creates a numeric less-than match.
    pub fn less_than(operand: T) -> Self {
        Self::new(AttributeMatchOperator::LessThan, operand)
    }

    /// Creates a numeric less-than-or-equal match.
    pub fn less_than_or_equal(operand: T) -> Self {
        Self::new(AttributeMatchOperator::LessThanOrEqual, operand)
    }

    fn new(operator: AttributeMatchOperator, operand: T) -> Self {
        Self { operator, operand }
    }
}

impl<T: Value> AttributeMatch<T> {
    pub(crate) fn encode(&self) -> SdkResult<ProtoAttributeMatch> {
        let operand = value_mapper::encode(&self.operand)?;
        let is_ordering = self.operator >= AttributeMatchOperator::GreaterThan;
        match operand.kind.as_ref() {
            Some(value::Kind::StringValue(_) | value::Kind::BoolValue(_)) if is_ordering => {
                return Err(invalid(
                    "AttributeMatch ordering requires an integer or floating-point operand",
                ));
            }
            Some(
                value::Kind::StringValue(_)
                | value::Kind::BoolValue(_)
                | value::Kind::IntValue(_)
                | value::Kind::DoubleValue(_),
            ) => {}
            _ => {
                return Err(invalid(
                    "AttributeMatch supports only string, Boolean, integer, or floating-point operands",
                ));
            }
        }
        Ok(ProtoAttributeMatch {
            key: String::new(),
            operator: self.operator as i32,
            operand: Some(operand),
        })
    }
}

fn invalid(message: impl Into<String>) -> SdkError {
    SdkError::InvalidArgument {
        message: message.into(),
    }
}
