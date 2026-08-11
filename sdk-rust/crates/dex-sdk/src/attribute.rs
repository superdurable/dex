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

pub struct Attribute<T> {
    name: String,
    index: Option<AttributeIndex>,
    marker: PhantomData<fn() -> T>,
}

impl<T> Attribute<T> {
    pub fn new(name: impl Into<String>) -> Self {
        Self {
            name: name.into(),
            index: None,
            marker: PhantomData,
        }
    }

    pub fn indexed(mut self, index: AttributeIndex) -> Self {
        self.index = Some(index);
        self
    }

    pub fn get(&self, context: &Context) -> HandlerResult<Option<T>>
    where
        T: Value,
    {
        context.get_attribute(self)
    }

    pub fn get_required(&self, context: &Context) -> HandlerResult<T>
    where
        T: Value,
    {
        self.get(context)?
            .ok_or_else(|| HandlerError::new(format!("attribute {} is missing", self.name)))
    }

    pub fn set(&self, context: &mut Context, value: T) -> HandlerResult<()>
    where
        T: Value,
    {
        context.set_attribute(self, value)
    }

    pub fn delete(&self, context: &mut Context) -> HandlerResult<()> {
        context.delete_attribute(self)
    }

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

pub struct AttributeMap<T> {
    name: String,
    index: Option<AttributeIndex>,
    marker: PhantomData<fn() -> T>,
}

impl<T> AttributeMap<T> {
    pub fn new(name: impl Into<String>) -> Self {
        Self {
            name: name.into(),
            index: None,
            marker: PhantomData,
        }
    }

    pub fn indexed(mut self, index: AttributeIndex) -> Self {
        self.index = Some(index);
        self
    }

    pub fn get(&self, context: &Context, instance: &str) -> HandlerResult<Option<T>>
    where
        T: Value,
    {
        context.get_attribute_map(self, instance)
    }

    pub fn get_required(&self, context: &Context, instance: &str) -> HandlerResult<T>
    where
        T: Value,
    {
        self.get(context, instance)?.ok_or_else(|| {
            HandlerError::new(format!("attribute {}[{instance}] is missing", self.name))
        })
    }

    pub fn set(&self, context: &mut Context, instance: &str, value: T) -> HandlerResult<()>
    where
        T: Value,
    {
        context.set_attribute_map(self, instance, value)
    }

    pub fn delete(&self, context: &mut Context, instance: &str) -> HandlerResult<()> {
        context.delete_attribute_map(self, instance)
    }

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
pub struct AttributeIndex {
    kind: AttributeIndexKind,
    key: Option<String>,
}

impl AttributeIndex {
    fn new(kind: AttributeIndexKind) -> Self {
        Self { kind, key: None }
    }

    pub fn keyword() -> Self {
        Self::new(AttributeIndexKind::Keyword)
    }

    pub fn full_text() -> Self {
        Self::new(AttributeIndexKind::FullText)
    }

    pub fn keyword_array() -> Self {
        Self::new(AttributeIndexKind::KeywordArray)
    }

    pub fn int() -> Self {
        Self::new(AttributeIndexKind::Int)
    }

    pub fn double() -> Self {
        Self::new(AttributeIndexKind::Double)
    }

    pub fn bool() -> Self {
        Self::new(AttributeIndexKind::Bool)
    }

    pub fn date_time() -> Self {
        Self::new(AttributeIndexKind::DateTime)
    }

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
