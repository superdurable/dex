// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Values that a run's local store no longer holds, which time travel computations read.

use dexzd_keyspace::{RunId, ValueId};
use dexzd_protocol::dex::Value;

use crate::ApplyError;

/// Reads values of a run that its local store no longer holds, such as values offloaded to
/// object storage.
///
/// Time travel computations read through it for values their target state holds; apply never
/// does.
pub trait OffloadedValues {
    /// Returns value `value_id` of run `run_id`, or `None` when it no longer exists anywhere.
    ///
    /// Returns an error when the value cannot be read.
    fn offloaded_value(
        &self,
        run_id: RunId,
        value_id: ValueId,
    ) -> Result<Option<Value>, ApplyError>;
}

/// The [`OffloadedValues`] of a store that offloads no value: every lookup finds nothing.
#[derive(Debug, Clone, Copy, Default)]
pub struct NoOffloadedValues;

impl OffloadedValues for NoOffloadedValues {
    fn offloaded_value(
        &self,
        _run_id: RunId,
        _value_id: ValueId,
    ) -> Result<Option<Value>, ApplyError> {
        Ok(None)
    }
}
