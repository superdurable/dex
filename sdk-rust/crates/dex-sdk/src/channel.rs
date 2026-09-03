// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::marker::PhantomData;

use crate::{Condition, Context, HandlerResult, Value};

#[derive(Clone, Debug, Eq, PartialEq)]
pub(crate) struct ChannelLoad {
    pub(crate) name: String,
}

/// Selects one ChannelMap instance's pending messages for an RPC invocation.
///
/// Create selections with [`ChannelMap::load_messages`], then attach them with
/// [`Rpc::load_channel_map_instance`](crate::Rpc::load_channel_map_instance).
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ChannelMapLoad {
    pub(crate) name: String,
    pub(crate) instance: Option<String>,
}

/// Identifies one typed value that is still pending in a Channel.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ChannelMessage<T> {
    /// UUIDv7 assigned by Dex when the message was published.
    pub message_id: String,
    /// Decoded Channel value.
    pub value: T,
}

/// Defines one durable FIFO queue of typed messages.
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
    /// Defines a Channel with a stable `name`.
    ///
    /// Slash is prohibited because it is a reserved character.
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

    /// Stages deletion of one pending message from an RPC handler.
    pub fn delete(&self, context: &mut Context, message_id: &str) -> HandlerResult<()> {
        context.delete_channel_message(self, message_id)
    }

    /// Returns the invocation snapshot's queued-message count.
    pub fn size(&self, context: &Context) -> HandlerResult<usize> {
        context.channel_size(self)
    }

    /// Returns pending messages from the RPC invocation snapshot in FIFO order.
    ///
    /// # Errors
    ///
    /// Returns a [`crate::HandlerError`] when called outside an RPC, when this Channel was not
    /// selected with [`Rpc::load_channel`](crate::Rpc::load_channel), or when decoding fails.
    pub fn pending_messages(&self, context: &Context) -> HandlerResult<Vec<ChannelMessage<T>>>
    where
        T: Value,
    {
        context.pending_channel_messages(self)
    }

    /// Finds one pending message by its Dex-assigned ID.
    ///
    /// Returns `Ok(None)` when the loaded snapshot does not contain `message_id`.
    ///
    /// # Errors
    ///
    /// Returns the same errors as [`Self::pending_messages`].
    pub fn find_pending_message(
        &self,
        context: &Context,
        message_id: &str,
    ) -> HandlerResult<Option<ChannelMessage<T>>>
    where
        T: Value,
    {
        Ok(self
            .pending_messages(context)?
            .into_iter()
            .find(|message| message.message_id == message_id))
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

    /// Creates a non-blocking condition consuming up to `count` queued messages.
    ///
    /// When its surrounding Wait completes, it consumes messages queued then. An empty queue yields
    /// no messages.
    pub fn at_most(&self, count: usize) -> Condition {
        self.range(None, Some(count))
    }

    /// Creates a condition that waits for the lower bound, then consumes up to the upper bound.
    ///
    /// Omitting the lower bound makes the condition complete immediately. Consumption uses only
    /// messages queued when the condition completes.
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
/// Slash is prohibited in instance keys because it is a reserved character.
pub struct ChannelMap<T> {
    name: String,
    marker: PhantomData<fn() -> T>,
}

impl<T> ChannelMap<T> {
    /// Defines a Channel map with a stable `name`.
    ///
    /// Slash is prohibited because it is a reserved character.
    pub fn new(name: impl Into<String>) -> Self {
        Self {
            name: name.into(),
            marker: PhantomData,
        }
    }

    /// Selects one slash-free map `instance`'s pending messages for an RPC Context.
    pub fn load_messages(&self, instance: impl Into<String>) -> ChannelMapLoad {
        ChannelMapLoad {
            name: self.name.clone(),
            instance: Some(instance.into()),
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

    /// Stages deletion of one pending message from a Channel-map instance in an RPC.
    pub fn delete(
        &self,
        context: &mut Context,
        instance: &str,
        message_id: &str,
    ) -> HandlerResult<()> {
        context.delete_channel_map_message(self, instance, message_id)
    }

    /// Returns the invocation snapshot's message count for `instance`.
    pub fn size(&self, context: &Context, instance: &str) -> HandlerResult<usize> {
        context.channel_map_size(self, instance)
    }

    /// Returns one instance's pending messages from the RPC snapshot in FIFO order.
    ///
    /// # Errors
    ///
    /// Returns a [`crate::HandlerError`] when called outside an RPC, when the instance was not
    /// selected for loading, or when a Value cannot be decoded.
    pub fn pending_messages(
        &self,
        context: &Context,
        instance: &str,
    ) -> HandlerResult<Vec<ChannelMessage<T>>>
    where
        T: Value,
    {
        context.pending_channel_map_messages(self, instance)
    }

    /// Finds one pending message by ID within one loaded map instance.
    ///
    /// Returns `Ok(None)` when the loaded snapshot does not contain `message_id`.
    ///
    /// # Errors
    ///
    /// Returns the same errors as [`Self::pending_messages`].
    pub fn find_pending_message(
        &self,
        context: &Context,
        instance: &str,
        message_id: &str,
    ) -> HandlerResult<Option<ChannelMessage<T>>>
    where
        T: Value,
    {
        Ok(self
            .pending_messages(context, instance)?
            .into_iter()
            .find(|message| message.message_id == message_id))
    }

    /// Returns the number of non-empty instances visible to the current RPC.
    pub fn map_size(&self, context: &Context) -> HandlerResult<usize> {
        Ok(self.all_instance_keys(context)?.len())
    }

    /// Returns sorted decoded non-empty keys, including buffered publishes.
    pub fn all_instance_keys(&self, context: &Context) -> HandlerResult<Vec<String>> {
        context.channel_map_keys(self)
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

    /// Creates a non-blocking `instance` condition consuming up to `count` queued messages.
    ///
    /// When its surrounding Wait completes, it consumes messages queued then. An empty queue yields
    /// no messages.
    pub fn at_most(&self, instance: &str, count: usize) -> Condition {
        self.range(instance, None, Some(count))
    }

    /// Creates an `instance` condition that waits for the lower bound, then consumes to its upper.
    ///
    /// Omitting the lower bound makes the condition complete immediately. Consumption uses only
    /// messages queued when the condition completes.
    pub fn range(
        &self,
        instance: &str,
        at_least: Option<usize>,
        at_most: Option<usize>,
    ) -> Condition {
        crate::registry::assert_map_instance(instance);
        Condition::channel(
            self.name.clone(),
            Some(instance.to_string()),
            at_least,
            at_most,
        )
    }

    /// Creates a conditional-completion guard requiring `instance` to be empty.
    pub fn when_empty(&self, instance: &str) -> ChannelGuard {
        crate::registry::assert_map_instance(instance);
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
