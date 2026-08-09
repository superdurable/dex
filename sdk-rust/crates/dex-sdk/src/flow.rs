// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use crate::{PersistenceSchema, RpcList, StepList, Value, short_type_name};

pub trait Flow: Send + Sync + 'static {
    type StartInput: Value;

    fn flow_type(&self) -> &'static str {
        short_type_name::<Self>()
    }

    fn steps(&self) -> StepList<Self::StartInput> {
        StepList::empty()
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
    }

    fn rpcs(&self) -> RpcList<Self>
    where
        Self: Sized,
    {
        RpcList::new()
    }
}
