// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Offloading run history to object storage, gated by a committed partition mode.

pub(crate) mod offload_mode;
pub(crate) mod segment_catalog;
pub(crate) mod segment_migration;
pub(crate) mod segment_seal;
pub(crate) mod segment_upload;
pub(crate) mod segmented_history;
