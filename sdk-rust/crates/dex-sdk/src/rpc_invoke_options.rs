// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use crate::{AttributeLock, AttributeMapLoad, ChannelMapLoad};

/// Adds runtime-selected map instances to one RPC invocation.
///
/// Dex unions these selections with the RPC definition's fixed locks and loads, then sorts and
/// deduplicates their physical names. Locks and loads are independent. Read-modify-write handlers
/// must add the same AttributeMap instance through both builder methods.
///
/// # Examples
///
/// ```
/// use dex_sdk::{AttributeMap, RpcInvokeOptions};
///
/// let profiles = AttributeMap::<String>::new("profiles");
/// let options = RpcInvokeOptions::new()
///     .lock_attribute_map_instance(profiles.lock("partition-007"))
///     .load_attribute_map_instance(profiles.load("partition-007"));
/// ```
#[derive(Clone, Debug, Default)]
pub struct RpcInvokeOptions {
    pub(crate) lock_attribute_map_instances: Vec<AttributeLock>,
    pub(crate) load_attribute_map_instances: Vec<AttributeMapLoad>,
    pub(crate) load_channel_map_instances: Vec<ChannelMapLoad>,
}

impl RpcInvokeOptions {
    /// Creates empty invocation options that preserve the RPC definition's behavior.
    pub fn new() -> Self {
        Self::default()
    }

    /// Adds one AttributeMap instance lock for this invocation.
    ///
    /// This does not load the instance into the handler snapshot.
    #[must_use]
    pub fn lock_attribute_map_instance(mut self, lock: AttributeLock) -> Self {
        self.lock_attribute_map_instances.push(lock);
        self
    }

    /// Adds one exact AttributeMap instance to the handler snapshot.
    #[must_use]
    pub fn load_attribute_map_instance(mut self, load: AttributeMapLoad) -> Self {
        self.load_attribute_map_instances.push(load);
        self
    }

    /// Adds one exact ChannelMap instance's pending messages to the handler snapshot.
    #[must_use]
    pub fn load_channel_map_instance(mut self, load: ChannelMapLoad) -> Self {
        self.load_channel_map_instances.push(load);
        self
    }
}
