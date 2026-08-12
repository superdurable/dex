// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

use std::error::Error;
use std::sync::Arc;

use dex_examples_rust::create_example_registry;
use dex_sdk::{BlobCache, BlobCacheConfig, Worker, WorkerOptions};

fn main() -> Result<(), Box<dyn Error>> {
    let server_address =
        std::env::var("DEX_SERVER_ADDRESS").unwrap_or_else(|_| "127.0.0.1:8801".to_string());
    let bind_address =
        std::env::var("DEX_WORKER_ADDRESS").unwrap_or_else(|_| "0.0.0.0:8803".to_string());
    let cache_directory = std::env::var("DEX_BLOB_CACHE_DIR")
        .unwrap_or_else(|_| "/tmp/dex-rust-examples-blobs".to_string());
    let cache = Arc::new(BlobCache::open(BlobCacheConfig::new(
        cache_directory,
        64 * 1024 * 1024,
        10_000,
    )?)?);
    let worker = Worker::try_new(
        create_example_registry()?,
        Arc::clone(&cache),
        WorkerOptions::new()
            .server_address(server_address)
            .bind_address(bind_address),
    )?;
    let result = worker.start();
    cache.close()?;
    result?;
    Ok(())
}
