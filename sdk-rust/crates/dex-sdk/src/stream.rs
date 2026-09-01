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
    stream_capacity_bytes: i64,
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
    /// Panics when `name` is empty or `stream_capacity_bytes` is not positive.
    pub fn new(name: impl Into<String>, stream_capacity_bytes: i64) -> Self {
        let name = name.into();
        assert!(!name.is_empty(), "Stream name must not be empty");
        assert!(
            stream_capacity_bytes > 0,
            "Stream stream_capacity_bytes must be positive"
        );
        Self {
            definition: Arc::new(StreamDefinition {
                name,
                stream_capacity_bytes,
            }),
            marker: PhantomData,
        }
    }

    /// Returns the stable logical Stream name.
    pub fn name(&self) -> &str {
        &self.definition.name
    }

    /// Returns the approximate shared byte budget.
    pub fn stream_capacity_bytes(&self) -> i64 {
        self.definition.stream_capacity_bytes
    }

    /// Appends one message immediately from the current Step execution.
    ///
    /// # Errors
    ///
    /// Returns [`crate::HandlerError`] for RPC or Flow timeout Contexts, unregistered Streams,
    /// encoding failures, cancellation, or a closed Worker output stream. Dex storage failures are
    /// not acknowledged.
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
    /// Client-provided source or Step-generated `#stepExecutionID` source.
    pub source: String,
}
