// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::sync::Arc;

use crate::{BlobCache, Registry, WorkerOptions};

pub struct Worker {
    _private: (),
}

impl Worker {
    pub fn new(_registry: Registry, _blob_cache: Arc<BlobCache>, _options: WorkerOptions) -> Self {
        Self { _private: () }
    }
}
