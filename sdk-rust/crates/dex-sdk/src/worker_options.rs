// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

#[derive(Clone, Debug)]
pub struct WorkerTarget {
    address: String,
    headless: bool,
}

impl WorkerTarget {
    pub fn new(address: impl Into<String>) -> Self {
        Self {
            address: address.into(),
            headless: false,
        }
    }

    pub fn headless(mut self, value: bool) -> Self {
        self.headless = value;
        self
    }

    pub fn address(&self) -> &str {
        &self.address
    }

    pub fn is_headless(&self) -> bool {
        self.headless
    }
}

#[derive(Clone, Debug)]
pub struct WorkerOptions {
    bind_address: String,
    server_address: String,
    worker_target: Option<WorkerTarget>,
}

impl WorkerOptions {
    pub fn new() -> Self {
        Self {
            bind_address: "0.0.0.0:8803".to_string(),
            server_address: "127.0.0.1:8801".to_string(),
            worker_target: None,
        }
    }

    pub fn bind_address(mut self, value: impl Into<String>) -> Self {
        self.bind_address = value.into();
        self
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

impl Default for WorkerOptions {
    fn default() -> Self {
        Self::new()
    }
}
