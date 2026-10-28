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

use std::time::Duration;

use dex_sdk::PullWorkerOptions;

/// Upper bound for a pull Worker to register every queue with Dex.
pub const PULL_REGISTRATION_TIMEOUT: Duration = Duration::from_secs(30);

/// Pull options sized for every sample Flow sharing one Worker process.
pub fn example_pull_worker_options() -> PullWorkerOptions {
    PullWorkerOptions::new().max_concurrent_tasks(64)
}
