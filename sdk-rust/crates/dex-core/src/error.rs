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
