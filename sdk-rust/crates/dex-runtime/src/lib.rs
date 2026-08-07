// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

mod worker_service;

pub use dex_protocol::dex::worker_service_server::WorkerServiceServer;
pub use worker_service::{WorkerService, WorkerServiceError};
