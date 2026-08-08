// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum StopType {
    Cancel,
    Terminate,
    Fail,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct StopFlowOptions {
    stop_type: StopType,
    reason: Option<String>,
}

impl StopFlowOptions {
    fn new(stop_type: StopType) -> Self {
        Self {
            stop_type,
            reason: None,
        }
    }

    pub fn cancel() -> Self {
        Self::new(StopType::Cancel)
    }

    pub fn terminate() -> Self {
        Self::new(StopType::Terminate)
    }

    pub fn fail() -> Self {
        Self::new(StopType::Fail)
    }

    pub fn reason(mut self, value: impl Into<String>) -> Self {
        self.reason = Some(value.into());
        self
    }
}
