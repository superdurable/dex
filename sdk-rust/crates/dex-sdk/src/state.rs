// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::marker::PhantomData;
use std::time::SystemTime;

use crate::{Condition, HandlerResult, Value};

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
            .ok_or_else(|| crate::HandlerError::new(format!("attribute {} is missing", self.name)))
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
            crate::HandlerError::new(format!("attribute {}[{instance}] is missing", self.name))
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

pub struct Channel<T> {
    name: String,
    marker: PhantomData<fn() -> T>,
}

impl<T> Channel<T> {
    pub fn new(name: impl Into<String>) -> Self {
        Self {
            name: name.into(),
            marker: PhantomData,
        }
    }

    pub fn publish(&self, context: &mut Context, value: T) -> HandlerResult<()>
    where
        T: Value,
    {
        context.publish(self, value)
    }

    pub fn size(&self, context: &Context) -> HandlerResult<usize> {
        context.channel_size(self)
    }

    pub fn condition_results(&self, context: &Context) -> HandlerResult<Vec<T>>
    where
        T: Value,
    {
        context.channel_results(self)
    }

    pub fn for_one(&self) -> Condition {
        self.range(Some(1), Some(1))
    }

    pub fn for_n(&self, count: usize) -> Condition {
        self.range(Some(count), Some(count))
    }

    pub fn at_least(&self, count: usize) -> Condition {
        self.range(Some(count), None)
    }

    pub fn at_most(&self, count: usize) -> Condition {
        self.range(None, Some(count))
    }

    pub fn range(&self, at_least: Option<usize>, at_most: Option<usize>) -> Condition {
        Condition::channel(self.name.clone(), None, at_least, at_most)
    }

    pub fn when_empty(&self) -> ChannelGuard {
        ChannelGuard {
            name: self.name.clone(),
            instance: None,
        }
    }
}

impl<T> Clone for Channel<T> {
    fn clone(&self) -> Self {
        Self {
            name: self.name.clone(),
            marker: PhantomData,
        }
    }
}

pub struct ChannelMap<T> {
    name: String,
    marker: PhantomData<fn() -> T>,
}

impl<T> ChannelMap<T> {
    pub fn new(name: impl Into<String>) -> Self {
        Self {
            name: name.into(),
            marker: PhantomData,
        }
    }

    pub fn publish(&self, context: &mut Context, instance: &str, value: T) -> HandlerResult<()>
    where
        T: Value,
    {
        context.publish_map(self, instance, value)
    }

    pub fn size(&self, context: &Context, instance: &str) -> HandlerResult<usize> {
        context.channel_map_size(self, instance)
    }

    pub fn condition_results(&self, context: &Context, instance: &str) -> HandlerResult<Vec<T>>
    where
        T: Value,
    {
        context.channel_map_results(self, instance)
    }

    pub fn for_one(&self, instance: &str) -> Condition {
        self.range(instance, Some(1), Some(1))
    }

    pub fn for_n(&self, instance: &str, count: usize) -> Condition {
        self.range(instance, Some(count), Some(count))
    }

    pub fn at_least(&self, instance: &str, count: usize) -> Condition {
        self.range(instance, Some(count), None)
    }

    pub fn at_most(&self, instance: &str, count: usize) -> Condition {
        self.range(instance, None, Some(count))
    }

    pub fn range(
        &self,
        instance: &str,
        at_least: Option<usize>,
        at_most: Option<usize>,
    ) -> Condition {
        Condition::channel(
            self.name.clone(),
            Some(instance.to_string()),
            at_least,
            at_most,
        )
    }

    pub fn when_empty(&self, instance: &str) -> ChannelGuard {
        ChannelGuard {
            name: self.name.clone(),
            instance: Some(instance.to_string()),
        }
    }
}

impl<T> Clone for ChannelMap<T> {
    fn clone(&self) -> Self {
        Self {
            name: self.name.clone(),
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
enum AttributeIndexKind {
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

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ChannelGuard {
    name: String,
    instance: Option<String>,
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct PersistenceSchema {
    definitions: Vec<PersistenceDefinition>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
enum PersistenceDefinition {
    Attribute,
    AttributeMap,
    Channel,
    ChannelMap,
}

impl PersistenceSchema {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn attribute<T>(mut self, _attribute: &Attribute<T>) -> Self {
        self.definitions.push(PersistenceDefinition::Attribute);
        self
    }

    pub fn attribute_map<T>(mut self, _attribute: &AttributeMap<T>) -> Self {
        self.definitions.push(PersistenceDefinition::AttributeMap);
        self
    }

    pub fn channel<T>(mut self, _channel: &Channel<T>) -> Self {
        self.definitions.push(PersistenceDefinition::Channel);
        self
    }

    pub fn channel_map<T>(mut self, _channel: &ChannelMap<T>) -> Self {
        self.definitions.push(PersistenceDefinition::ChannelMap);
        self
    }
}

pub struct Context {
    _private: (),
}

impl Context {
    pub fn flow_id(&self) -> &str {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn run_id(&self) -> &str {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn flow_started_at(&self) -> SystemTime {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn attempt(&self) -> u32 {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn has_timer_fired(&self, _index: usize) -> bool {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn wait_for_method_failed(&self) -> bool {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn set_step_execution_local<T: Value>(
        &mut self,
        _key: &str,
        _value: T,
    ) -> HandlerResult<()> {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn step_execution_local<T: Value>(&self, _key: &str) -> HandlerResult<T> {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn record_event<T: Value>(&mut self, _name: &str, _value: T) -> HandlerResult<()> {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn get_attribute<T: Value>(&self, _attribute: &Attribute<T>) -> HandlerResult<Option<T>> {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn get_attribute_map<T: Value>(
        &self,
        _attribute: &AttributeMap<T>,
        _instance: &str,
    ) -> HandlerResult<Option<T>> {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn set_attribute<T: Value>(
        &mut self,
        _attribute: &Attribute<T>,
        _value: T,
    ) -> HandlerResult<()> {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn set_attribute_map<T: Value>(
        &mut self,
        _attribute: &AttributeMap<T>,
        _instance: &str,
        _value: T,
    ) -> HandlerResult<()> {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn delete_attribute<T>(&mut self, _attribute: &Attribute<T>) -> HandlerResult<()> {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn delete_attribute_map<T>(
        &mut self,
        _attribute: &AttributeMap<T>,
        _instance: &str,
    ) -> HandlerResult<()> {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn publish<T: Value>(&mut self, _channel: &Channel<T>, _value: T) -> HandlerResult<()> {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn publish_map<T: Value>(
        &mut self,
        _channel: &ChannelMap<T>,
        _instance: &str,
        _value: T,
    ) -> HandlerResult<()> {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn channel_size<T>(&self, _channel: &Channel<T>) -> HandlerResult<usize> {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn channel_map_size<T>(
        &self,
        _channel: &ChannelMap<T>,
        _instance: &str,
    ) -> HandlerResult<usize> {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn channel_results<T: Value>(&self, _channel: &Channel<T>) -> HandlerResult<Vec<T>> {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn channel_map_results<T: Value>(
        &self,
        _channel: &ChannelMap<T>,
        _instance: &str,
    ) -> HandlerResult<Vec<T>> {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }
}
