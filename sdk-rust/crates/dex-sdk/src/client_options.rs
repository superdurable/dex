// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::Arc;

use crate::Credential;

#[derive(Clone, Debug)]
/// Configures how a [`crate::Client`] reaches Dex.
///
/// [`ClientOptions::new`] connects to `127.0.0.1:8801` over plaintext gRPC and requires no
/// inbound Worker endpoint. All Flows use pull Workers.
pub struct ClientOptions {
    server_address: String,
    credential: Option<Arc<dyn Credential>>,
}

impl ClientOptions {
    /// Creates options with the local Dex server default and pull Worker routing.
    pub fn new() -> Self {
        Self {
            server_address: "127.0.0.1:8801".to_string(),
            credential: None,
        }
    }

    /// Sets the plaintext gRPC `host:port` used for every client request.
    pub fn server_address(mut self, value: impl Into<String>) -> Self {
        self.server_address = value.into();
        self
    }

    /// Sends a bearer token before every FlowService request.
    ///
    /// The default sends no authorization metadata. The credential may be called concurrently;
    /// its error fails the request locally. The token uses the plaintext Dex connection, so
    /// protect the network path. See [`crate::FileCredential`] for file rotation.
    pub fn credential(mut self, value: Arc<dyn Credential>) -> Self {
        self.credential = Some(value);
        self
    }

    pub(crate) fn server_address_value(&self) -> &str {
        &self.server_address
    }

    pub(crate) fn credential_value(&self) -> Option<Arc<dyn Credential>> {
        self.credential.clone()
    }
}

impl Default for ClientOptions {
    fn default() -> Self {
        Self::new()
    }
}
