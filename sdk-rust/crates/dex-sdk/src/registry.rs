// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use crate::Flow;

#[derive(Clone)]
pub struct Registry {
    _private: (),
}

impl Registry {
    pub fn new() -> Self {
        Self { _private: () }
    }

    pub fn register<SomeFlow: Flow>(self, _flow: SomeFlow) -> Self {
        self
    }
}

impl Default for Registry {
    fn default() -> Self {
        Self::new()
    }
}
