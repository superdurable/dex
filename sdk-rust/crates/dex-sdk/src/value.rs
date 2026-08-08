// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use serde::Serialize;
use serde::de::DeserializeOwned;

pub trait Value: DeserializeOwned + Send + Serialize + Sync + 'static {}

impl<T> Value for T where T: DeserializeOwned + Send + Serialize + Sync + 'static {}

pub(crate) fn short_type_name<T: ?Sized>() -> &'static str {
    std::any::type_name::<T>()
        .rsplit("::")
        .next()
        .expect("Rust type names are non-empty")
}
