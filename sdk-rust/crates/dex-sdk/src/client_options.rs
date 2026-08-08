// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use crate::WorkerTarget;

#[derive(Clone, Debug)]
pub struct ClientOptions {
    server_address: String,
    worker_target: Option<WorkerTarget>,
}

impl ClientOptions {
    pub fn new() -> Self {
        Self {
            server_address: "127.0.0.1:8801".to_string(),
            worker_target: None,
        }
    }

    pub fn server_address(mut self, value: impl Into<String>) -> Self {
        self.server_address = value.into();
        self
    }

    pub fn worker_target(mut self, value: WorkerTarget) -> Self {
        self.worker_target = Some(value);
        self
    }
}

impl Default for ClientOptions {
    fn default() -> Self {
        Self::new()
    }
}
