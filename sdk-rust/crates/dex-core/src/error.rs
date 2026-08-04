// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::error::Error;
use std::fmt::{Display, Formatter};

use crate::InvocationId;

/// Core lifecycle and routing failures.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum CoreError {
    InvalidQueueCapacity,
    InvocationIdExhausted,
    UnsupportedProtocolVersion { expected: u32, actual: u32 },
    WorkerShutdown,
    UnknownInvocation(InvocationId),
    CompletionReceiverDropped(InvocationId),
}

impl Display for CoreError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::InvalidQueueCapacity => formatter.write_str("queue capacity must be positive"),
            Self::InvocationIdExhausted => formatter.write_str("invocation IDs exhausted"),
            Self::UnsupportedProtocolVersion { expected, actual } => write!(
                formatter,
                "unsupported Core protocol version {actual}; expected {expected}"
            ),
            Self::WorkerShutdown => formatter.write_str("worker is shut down"),
            Self::UnknownInvocation(invocation_id) => {
                write!(formatter, "unknown invocation {}", invocation_id.get())
            }
            Self::CompletionReceiverDropped(invocation_id) => write!(
                formatter,
                "completion receiver dropped for invocation {}",
                invocation_id.get()
            ),
        }
    }
}

impl Error for CoreError {}
