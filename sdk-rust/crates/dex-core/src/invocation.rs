// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

/// Initial bridge protocol version.
pub const CORE_PROTOCOL_VERSION: u32 = 1;

/// Opaque invocation identifier.
#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq)]
pub struct InvocationId(u64);

impl InvocationId {
    pub(crate) fn new(value: u64) -> Self {
        debug_assert_ne!(value, 0);
        Self(value)
    }

    /// Returns the wire representation.
    pub fn get(self) -> u64 {
        self.0
    }
}

/// User method selected by a server request.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum InvocationKind {
    WaitFor,
    Execute,
    WorkerRpc,
}

/// Work delivered to a language SDK.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Invocation {
    protocol_version: u32,
    id: InvocationId,
    kind: InvocationKind,
    request: Vec<u8>,
}

impl Invocation {
    pub(crate) fn new(id: InvocationId, kind: InvocationKind, request: Vec<u8>) -> Self {
        Self {
            protocol_version: CORE_PROTOCOL_VERSION,
            id,
            kind,
            request,
        }
    }

    /// Returns the bridge protocol version.
    pub fn protocol_version(&self) -> u32 {
        self.protocol_version
    }

    /// Returns the opaque invocation identifier.
    pub fn id(&self) -> InvocationId {
        self.id
    }

    /// Returns the user method kind.
    pub fn kind(&self) -> InvocationKind {
        self.kind
    }

    /// Returns the serialized canonical request.
    pub fn request(&self) -> &[u8] {
        &self.request
    }

    /// Moves out the serialized canonical request.
    pub fn into_request(self) -> Vec<u8> {
        self.request
    }
}

/// Structured user-code failure.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct InvocationFailure {
    error_type: String,
    message: String,
    details: Vec<u8>,
}

impl InvocationFailure {
    /// Creates a language failure.
    pub fn new(
        error_type: impl Into<String>,
        message: impl Into<String>,
        details: Vec<u8>,
    ) -> Self {
        Self {
            error_type: error_type.into(),
            message: message.into(),
            details,
        }
    }

    /// Returns the language error type.
    pub fn error_type(&self) -> &str {
        &self.error_type
    }

    /// Returns the error message.
    pub fn message(&self) -> &str {
        &self.message
    }

    /// Returns serialized language details.
    pub fn details(&self) -> &[u8] {
        &self.details
    }
}

/// Language completion returned to transport.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum InvocationResult {
    Success(Vec<u8>),
    Failure(InvocationFailure),
}
