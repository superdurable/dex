// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Store format tests: recorded stores read back, and `Store::open` stamps and enforces the version.

mod contents;
mod format_version;
mod generator;
mod recorded_store;
mod store_files;
mod workload;
