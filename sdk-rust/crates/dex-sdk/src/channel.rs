// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::marker::PhantomData;

use crate::{Condition, Context, HandlerResult, Value};

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

    pub(crate) fn name(&self) -> &str {
        &self.name
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

    pub(crate) fn name(&self) -> &str {
        &self.name
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
pub struct ChannelGuard {
    name: String,
    instance: Option<String>,
}

impl ChannelGuard {
    pub(crate) fn physical_name(&self) -> String {
        match self.instance.as_deref() {
            Some(instance) => crate::registry::physical_name(&self.name, instance),
            None => self.name.clone(),
        }
    }
}
