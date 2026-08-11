// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use serde::Serialize;
use serde::de::DeserializeOwned;

/// Marks application values that Dex can serialize as JSON and move across handler boundaries.
///
/// The blanket implementation accepts owned, thread-safe Serde values. Keep serialized shapes
/// backward compatible for running Flows, because persisted Attributes, Channel messages, Step
/// inputs, and RPC payloads may outlive a deployment.
pub trait Value: DeserializeOwned + Send + Serialize + Sync + 'static {}

impl<T> Value for T where T: DeserializeOwned + Send + Serialize + Sync + 'static {}

pub(crate) fn short_type_name<T: ?Sized>() -> &'static str {
    std::any::type_name::<T>()
        .rsplit("::")
        .next()
        .expect("Rust type names are non-empty")
}
