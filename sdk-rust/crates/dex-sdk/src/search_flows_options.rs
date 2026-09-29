// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/// Controls inclusion of earlier Continue-as-New runs in visibility searches.
#[derive(Clone, Copy, Debug, Default, Eq, PartialEq)]
pub struct SearchFlowsOptions {
    pub(crate) include_continued_as_new: bool,
}

impl SearchFlowsOptions {
    /// Excludes earlier Continue-as-New runs by default.
    pub fn new() -> Self {
        Self::default()
    }

    /// Disables default exclusion when true, retaining all explicit query filters.
    pub fn include_continued_as_new(mut self, value: bool) -> Self {
        self.include_continued_as_new = value;
        self
    }
}
