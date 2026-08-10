// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

#![allow(dead_code)]

#[path = "iwfcompat/support.rs"]
mod support;

#[path = "iwfcompat/basic.rs"]
mod basic;
#[path = "iwfcompat/channels.rs"]
mod channels;
#[path = "iwfcompat/operations.rs"]
mod operations;
#[path = "iwfcompat/persistence.rs"]
mod persistence;
#[path = "iwfcompat/rpc.rs"]
mod rpc;
#[path = "iwfcompat/state.rs"]
mod state;
