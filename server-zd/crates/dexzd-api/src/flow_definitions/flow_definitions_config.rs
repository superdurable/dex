// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/// Limits of the Flow definitions that UploadFlowDefinitions stores.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct FlowDefinitionsConfig {
    /// Defaults to 8 MiB, `flowDefinitions.maxDefinitionBytes`; largest content of one definition.
    pub max_definition_bytes: usize,
    /// Defaults to 256, `matching.maxFlowTypeLength`; longest accepted Flow type in bytes.
    pub max_flow_type_length: usize,
    /// Defaults to 32 MiB, `api.grpcMaxMessageBytes`; largest encoded GetFlowDefinitions response,
    /// and the most bytes read from one stored object.
    pub max_response_bytes: usize,
    /// Defaults to 0; bytes that `objectStore.prefix` adds in front of every object key. Flow types
    /// whose full key would exceed 1024 bytes are rejected.
    pub object_key_prefix_bytes: usize,
}

impl Default for FlowDefinitionsConfig {
    fn default() -> Self {
        Self {
            max_definition_bytes: 8 << 20,
            max_flow_type_length: 256,
            max_response_bytes: 32 << 20,
            object_key_prefix_bytes: 0,
        }
    }
}
