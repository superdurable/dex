// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::marker::PhantomData;

use crate::{Condition, Context, HandlerResult, Value};

/// Defines one durable FIFO stream of typed messages.
///
/// Add the Channel to [`crate::PersistenceSchema`]. Clients and handlers may publish messages;
/// Steps create [`Condition`] values that wait for queue-size bounds and read the messages consumed
/// by the satisfied condition.
///
/// # Examples
///
/// ```
/// use dex_sdk::{Channel, Wait};
///
/// let approvals = Channel::<String>::new("approvals");
/// let wait = Wait::until(approvals.for_one());
/// ```
pub struct Channel<T> {
    name: String,
    marker: PhantomData<fn() -> T>,
}

impl<T> Channel<T> {
    /// Defines a Channel with stable `name`.
    pub fn new(name: impl Into<String>) -> Self {
        Self {
            name: name.into(),
            marker: PhantomData,
        }
    }

    /// Stages one message from the current Step or RPC invocation.
    ///
    /// # Errors
    ///
    /// Returns a [`crate::HandlerError`] when `value` cannot be encoded.
    pub fn publish(&self, context: &mut Context, value: T) -> HandlerResult<()>
    where
        T: Value,
    {
        context.publish(self, value)
    }

    /// Returns the invocation snapshot's queued-message count.
    pub fn size(&self, context: &Context) -> HandlerResult<usize> {
        context.channel_size(self)
    }

    /// Decodes messages consumed by this Channel's satisfied condition.
    ///
    /// # Errors
    ///
    /// Returns a [`crate::HandlerError`] when a message cannot be decoded.
    pub fn condition_results(&self, context: &Context) -> HandlerResult<Vec<T>>
    where
        T: Value,
    {
        context.channel_results(self)
    }

    /// Creates a condition that consumes exactly one queued message.
    pub fn for_one(&self) -> Condition {
        self.range(Some(1), Some(1))
    }

    /// Creates a condition that consumes exactly `count` queued messages.
    pub fn for_n(&self, count: usize) -> Condition {
        self.range(Some(count), Some(count))
    }

    /// Creates a condition satisfied when at least `count` messages are queued.
    pub fn at_least(&self, count: usize) -> Condition {
        self.range(Some(count), None)
    }

    /// Creates a condition satisfied when no more than `count` messages are queued.
    pub fn at_most(&self, count: usize) -> Condition {
        self.range(None, Some(count))
    }

    /// Creates a condition with optional inclusive lower and upper queue-size bounds.
    pub fn range(&self, at_least: Option<usize>, at_most: Option<usize>) -> Condition {
        Condition::channel(self.name.clone(), None, at_least, at_most)
    }

    /// Creates a guard used by conditional Flow completion to require an empty queue.
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

/// Defines independently queued Channel instances under one name.
///
/// Supply an instance string for every publish, read, condition, and completion guard. Add the map
/// definition once to [`crate::PersistenceSchema`].
pub struct ChannelMap<T> {
    name: String,
    marker: PhantomData<fn() -> T>,
}

impl<T> ChannelMap<T> {
    /// Defines a Channel map with stable `name`.
    pub fn new(name: impl Into<String>) -> Self {
        Self {
            name: name.into(),
            marker: PhantomData,
        }
    }

    /// Stages one message for `instance` from the current invocation.
    ///
    /// # Errors
    ///
    /// Returns a [`crate::HandlerError`] when `value` cannot be encoded.
    pub fn publish(&self, context: &mut Context, instance: &str, value: T) -> HandlerResult<()>
    where
        T: Value,
    {
        context.publish_map(self, instance, value)
    }

    /// Returns the invocation snapshot's message count for `instance`.
    pub fn size(&self, context: &Context, instance: &str) -> HandlerResult<usize> {
        context.channel_map_size(self, instance)
    }

    /// Decodes messages consumed by `instance`'s satisfied condition.
    ///
    /// # Errors
    ///
    /// Returns a [`crate::HandlerError`] when a message cannot be decoded.
    pub fn condition_results(&self, context: &Context, instance: &str) -> HandlerResult<Vec<T>>
    where
        T: Value,
    {
        context.channel_map_results(self, instance)
    }

    /// Creates an `instance` condition that consumes exactly one message.
    pub fn for_one(&self, instance: &str) -> Condition {
        self.range(instance, Some(1), Some(1))
    }

    /// Creates an `instance` condition that consumes exactly `count` messages.
    pub fn for_n(&self, instance: &str, count: usize) -> Condition {
        self.range(instance, Some(count), Some(count))
    }

    /// Creates an `instance` condition with an inclusive lower queue-size bound.
    pub fn at_least(&self, instance: &str, count: usize) -> Condition {
        self.range(instance, Some(count), None)
    }

    /// Creates an `instance` condition with an inclusive upper queue-size bound.
    pub fn at_most(&self, instance: &str, count: usize) -> Condition {
        self.range(instance, None, Some(count))
    }

    /// Creates an `instance` condition with optional inclusive lower and upper bounds.
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

    /// Creates a conditional-completion guard requiring `instance` to be empty.
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
/// Identifies a Channel or Channel-map instance that must be empty before conditional completion.
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
