// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::marker::PhantomData;

use dex_protocol::dex::IndexConfig;

use crate::{Context, HandlerError, HandlerResult, Value};

/// Defines one durable, typed value stored with a Flow.
///
/// Declare Attributes on the Flow, add them to [`crate::PersistenceSchema`], and reuse the same
/// definition from Step and RPC code. Values use Serde serialization. `get` distinguishes a missing
/// value with `None`; `get_required` converts absence into [`HandlerError`].
///
/// # Examples
///
/// ```
/// use dex_sdk::{Attribute, AttributeIndex};
///
/// let status = Attribute::<String>::new("status")
///     .indexed(AttributeIndex::keyword());
/// ```
pub struct Attribute<T> {
    name: String,
    index: Option<AttributeIndex>,
    marker: PhantomData<fn() -> T>,
}

impl<T> Attribute<T> {
    /// Defines an unindexed Attribute with stable `name`.
    pub fn new(name: impl Into<String>) -> Self {
        Self {
            name: name.into(),
            index: None,
            marker: PhantomData,
        }
    }

    /// Enables Flow-search indexing with the supplied representation.
    pub fn indexed(mut self, index: AttributeIndex) -> Self {
        self.index = Some(index);
        self
    }

    /// Reads the current value from a Step or RPC invocation.
    ///
    /// Returns `Ok(None)` when no value is stored.
    ///
    /// # Errors
    ///
    /// Returns [`HandlerError`] when the payload cannot be decoded.
    pub fn get(&self, context: &Context) -> HandlerResult<Option<T>>
    where
        T: Value,
    {
        context.get_attribute(self)
    }

    /// Reads the current value and treats absence as an error.
    ///
    /// # Errors
    ///
    /// Returns [`HandlerError`] when the Attribute is missing or cannot be decoded.
    pub fn get_required(&self, context: &Context) -> HandlerResult<T>
    where
        T: Value,
    {
        self.get(context)?
            .ok_or_else(|| HandlerError::new(format!("attribute {} is missing", self.name)))
    }

    /// Stages `value` for durable persistence in the current invocation.
    ///
    /// # Errors
    ///
    /// Returns [`HandlerError`] when the value cannot be encoded or indexed.
    pub fn set(&self, context: &mut Context, value: T) -> HandlerResult<()>
    where
        T: Value,
    {
        context.set_attribute(self, value)
    }

    /// Stages deletion of the current value.
    ///
    /// Deleting a missing Attribute is valid.
    pub fn delete(&self, context: &mut Context) -> HandlerResult<()> {
        context.delete_attribute(self)
    }

    /// Creates a lock request for Step options or an RPC definition.
    pub fn lock(&self) -> AttributeLock {
        AttributeLock {
            attribute: self.name.clone(),
            instance: None,
        }
    }

    pub(crate) fn name(&self) -> &str {
        &self.name
    }

    pub(crate) fn index(&self) -> Option<&AttributeIndex> {
        self.index.as_ref()
    }
}

impl<T> Clone for Attribute<T> {
    fn clone(&self) -> Self {
        Self {
            name: self.name.clone(),
            index: self.index.clone(),
            marker: PhantomData,
        }
    }
}

/// Defines keyed durable values sharing one Attribute name.
///
/// Each `instance` identifies an independent stored value and lock. Add the map definition once to
/// [`crate::PersistenceSchema`]; instance names are supplied at access time.
pub struct AttributeMap<T> {
    name: String,
    index: Option<AttributeIndex>,
    marker: PhantomData<fn() -> T>,
}

impl<T> AttributeMap<T> {
    /// Defines an unindexed Attribute map with stable `name`.
    pub fn new(name: impl Into<String>) -> Self {
        Self {
            name: name.into(),
            index: None,
            marker: PhantomData,
        }
    }

    /// Enables search indexing for every instance using the supplied representation.
    pub fn indexed(mut self, index: AttributeIndex) -> Self {
        self.index = Some(index);
        self
    }

    /// Reads one instance, returning `Ok(None)` when it is absent.
    ///
    /// # Errors
    ///
    /// Returns [`HandlerError`] when the payload cannot be decoded.
    pub fn get(&self, context: &Context, instance: &str) -> HandlerResult<Option<T>>
    where
        T: Value,
    {
        context.get_attribute_map(self, instance)
    }

    /// Reads one instance and treats absence as an error.
    ///
    /// # Errors
    ///
    /// Returns [`HandlerError`] when the instance is missing or cannot be decoded.
    pub fn get_required(&self, context: &Context, instance: &str) -> HandlerResult<T>
    where
        T: Value,
    {
        self.get(context, instance)?.ok_or_else(|| {
            HandlerError::new(format!("attribute {}[{instance}] is missing", self.name))
        })
    }

    /// Stages one instance value for durable persistence.
    ///
    /// # Errors
    ///
    /// Returns [`HandlerError`] when the value cannot be encoded or indexed.
    pub fn set(&self, context: &mut Context, instance: &str, value: T) -> HandlerResult<()>
    where
        T: Value,
    {
        context.set_attribute_map(self, instance, value)
    }

    /// Stages deletion of one instance; missing instances are valid.
    pub fn delete(&self, context: &mut Context, instance: &str) -> HandlerResult<()> {
        context.delete_attribute_map(self, instance)
    }

    /// Creates a lock request scoped to one map instance.
    pub fn lock(&self, instance: impl Into<String>) -> AttributeLock {
        AttributeLock {
            attribute: self.name.clone(),
            instance: Some(instance.into()),
        }
    }

    pub(crate) fn name(&self) -> &str {
        &self.name
    }

    pub(crate) fn index(&self) -> Option<&AttributeIndex> {
        self.index.as_ref()
    }
}

impl<T> Clone for AttributeMap<T> {
    fn clone(&self) -> Self {
        Self {
            name: self.name.clone(),
            index: self.index.clone(),
            marker: PhantomData,
        }
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
/// Describes how Dex projects an Attribute value into its Flow-search index.
///
/// Without [`Self::with_key`], a single Attribute uses its name as the physical key. Attribute maps
/// derive per-instance physical keys unless an explicit key is supplied.
pub struct AttributeIndex {
    kind: AttributeIndexKind,
    key: Option<String>,
}

impl AttributeIndex {
    fn new(kind: AttributeIndexKind) -> Self {
        Self { kind, key: None }
    }

    /// Indexes one exact UTF-8 string for equality and aggregation queries.
    pub fn keyword() -> Self {
        Self::new(AttributeIndexKind::Keyword)
    }

    /// Indexes analyzed UTF-8 text for full-text queries.
    pub fn full_text() -> Self {
        Self::new(AttributeIndexKind::FullText)
    }

    /// Indexes an array of exact UTF-8 strings.
    pub fn keyword_array() -> Self {
        Self::new(AttributeIndexKind::KeywordArray)
    }

    /// Indexes a signed 64-bit integer.
    pub fn int() -> Self {
        Self::new(AttributeIndexKind::Int)
    }

    /// Indexes a finite 64-bit floating-point number.
    pub fn double() -> Self {
        Self::new(AttributeIndexKind::Double)
    }

    /// Indexes a Boolean value.
    pub fn bool() -> Self {
        Self::new(AttributeIndexKind::Bool)
    }

    /// Indexes an RFC 3339 date-time value.
    pub fn date_time() -> Self {
        Self::new(AttributeIndexKind::DateTime)
    }

    /// Uses an explicit physical search-index key instead of the default Attribute-derived key.
    pub fn with_key(mut self, key: impl Into<String>) -> Self {
        self.key = Some(key.into());
        self
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum AttributeIndexKind {
    Keyword,
    FullText,
    KeywordArray,
    Int,
    Double,
    Bool,
    DateTime,
}

#[derive(Clone, Debug, Eq, PartialEq)]
/// Identifies an Attribute or Attribute-map instance lock.
///
/// Create locks with [`Attribute::lock`] or [`AttributeMap::lock`], then attach them to
/// [`crate::StepOptions`] or [`crate::RpcDefinition`](crate::Rpc).
pub struct AttributeLock {
    attribute: String,
    instance: Option<String>,
}

impl AttributeIndex {
    pub(crate) fn proto_config(&self, dynamic: bool) -> IndexConfig {
        IndexConfig {
            enable: true,
            r#type: self.kind.proto_value(),
            index_key: self
                .key
                .clone()
                .or_else(|| dynamic.then(String::new))
                .unwrap_or_default(),
        }
    }

    pub(crate) fn key(&self) -> Option<&str> {
        self.key.as_deref()
    }

    pub(crate) fn proto_value(&self) -> i32 {
        self.kind.proto_value()
    }
}

impl AttributeIndexKind {
    pub(crate) fn proto_value(self) -> i32 {
        use dex_protocol::dex::IndexType;

        match self {
            Self::Keyword => IndexType::Keyword.into(),
            Self::FullText => IndexType::Text.into(),
            Self::KeywordArray => IndexType::KeywordArray.into(),
            Self::Int => IndexType::Int.into(),
            Self::Double => IndexType::Double.into(),
            Self::Bool => IndexType::Bool.into(),
            Self::DateTime => IndexType::Datetime.into(),
        }
    }
}

impl AttributeLock {
    pub(crate) fn physical_name(&self) -> String {
        match self.instance.as_deref() {
            Some(instance) => crate::registry::physical_name(&self.attribute, instance),
            None => self.attribute.clone(),
        }
    }
}
