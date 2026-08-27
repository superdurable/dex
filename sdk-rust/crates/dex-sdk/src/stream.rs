// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::marker::PhantomData;
use std::sync::Arc;
use std::time::SystemTime;

use crate::{Context, HandlerResult, Value};

#[derive(Debug)]
struct StreamDefinition {
    name: String,
    max_estimated_bytes: i64,
}

/// Defines a typed best-effort resumable message Stream owned by one Flow type.
///
/// Register the same definition in exactly one [`crate::PersistenceSchema`]. Clones share
/// identity and may be used at Client and Step call sites.
#[derive(Clone, Debug)]
pub struct Stream<T> {
    definition: Arc<StreamDefinition>,
    marker: PhantomData<fn() -> T>,
}

impl<T> Stream<T> {
    /// Creates a Stream with a positive approximate budget shared by all instances of its Flow.
    ///
    /// # Panics
    ///
    /// Panics when `name` is empty or `max_estimated_bytes` is not positive.
    pub fn new(name: impl Into<String>, max_estimated_bytes: i64) -> Self {
        let name = name.into();
        assert!(!name.is_empty(), "Stream name must not be empty");
        assert!(
            max_estimated_bytes > 0,
            "Stream max_estimated_bytes must be positive"
        );
        Self {
            definition: Arc::new(StreamDefinition {
                name,
                max_estimated_bytes,
            }),
            marker: PhantomData,
        }
    }

    /// Returns the stable logical Stream name.
    pub fn name(&self) -> &str {
        &self.definition.name
    }

    /// Returns the approximate shared byte budget.
    pub fn max_estimated_bytes(&self) -> i64 {
        self.definition.max_estimated_bytes
    }

    /// Appends one message immediately from the current Step execution.
    ///
    /// # Errors
    ///
    /// Returns [`crate::HandlerError`] for RPC Contexts, duplicate writes, unregistered Streams,
    /// encoding failures, or a failed FlowService write.
    pub fn write(&self, context: &mut Context, value: T) -> HandlerResult<()>
    where
        T: Value,
    {
        context.write_stream(self, value)
    }

    pub(crate) fn identity(&self) -> usize {
        Arc::as_ptr(&self.definition) as usize
    }
}

/// Describes one retained Stream message returned by [`crate::Client::read_stream`].
#[derive(Clone, Debug)]
pub struct StreamMessage<T> {
    /// Decoded application message.
    pub value: T,
    /// Opaque token to pass unchanged to the next read.
    pub resume_token: String,
    /// Server-assigned creation time.
    pub created_time: SystemTime,
    /// Client key or Step-generated runID#stepExecutionID key.
    pub idempotency_key: String,
}
