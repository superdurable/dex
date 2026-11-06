// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::Arc;

use crate::{Credential, FlowServiceTlsOptions};

#[derive(Clone, Debug)]
/// Configures how a [`crate::Client`] reaches Dex.
///
/// [`ClientOptions::new`] connects to `127.0.0.1:8801` over plaintext gRPC and requires no
/// inbound Worker endpoint. All Flows use pull Workers.
pub struct ClientOptions {
    server_address: String,
    credential: Option<Arc<dyn Credential>>,
    flow_service_tls: Option<FlowServiceTlsOptions>,
}

impl ClientOptions {
    /// Creates options with the local Dex server default and pull Worker routing.
    pub fn new() -> Self {
        Self {
            server_address: "127.0.0.1:8801".to_string(),
            credential: None,
            flow_service_tls: None,
        }
    }

    /// Sets the FlowService address used for every Client request.
    ///
    /// With TLS, accepts `host:port`, `dns:///host:port`, or `dns://authority/host:port`.
    pub fn server_address(mut self, value: impl Into<String>) -> Self {
        self.server_address = value.into();
        self
    }

    /// Enables TLS for every outbound Client FlowService connection.
    ///
    /// Omitted by default, preserving plaintext gRPC. Takes ownership of `value` and returns
    /// these options with TLS enabled. [`crate::Client::try_new`] validates the address,
    /// additional roots, and server name, returning [`crate::SdkError::InvalidArgument`] for
    /// invalid input. See [`FlowServiceTlsOptions`] for defaults and a complete example.
    pub fn flow_service_tls(mut self, value: FlowServiceTlsOptions) -> Self {
        self.flow_service_tls = Some(value);
        self
    }

    /// Sends a bearer token before every FlowService request.
    ///
    /// The default sends no authorization metadata. The credential may be called concurrently;
    /// its error fails the request locally. The token uses the configured Dex connection. Enable
    /// TLS with [`Self::flow_service_tls`] to protect it. See [`crate::FileCredential`] for file rotation.
    pub fn credential(mut self, value: Arc<dyn Credential>) -> Self {
        self.credential = Some(value);
        self
    }

    pub(crate) fn server_address_value(&self) -> &str {
        &self.server_address
    }

    pub(crate) fn flow_service_tls_value(&self) -> Option<&FlowServiceTlsOptions> {
        self.flow_service_tls.as_ref()
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
