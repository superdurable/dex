// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use crate::flow_definition::{
    map_upload_results, rewrite_source_paths, upload_requests, validate_document,
};
use crate::sdk_error::FlowTargetRequirement;
use crate::{Client, FlowDefinitionDocument, FlowDefinitionUploadResult, SdkError, SdkResult};

const UPLOAD_FLOW_DEFINITIONS: &str = "upload_flow_definitions";

impl Client {
    /// Uploads Flow Definition Graph documents so Dex Web can draw their Flow types.
    ///
    /// Use this from a deploy or CI script. Workers upload their configured documents at start;
    /// see [`crate::WorkerOptions::flow_definition_paths`]. The Dex Server decides each Flow type
    /// separately. It stores a document for a new Flow type, keeps an identical one, and replaces
    /// a different one only when the uploaded generation time is strictly later. With `force`, a
    /// different document always replaces the stored one; use it to roll a definition back on
    /// purpose. Before sending, an absolute `source.path` in a document is replaced by a path
    /// relative to the working directory, or by the file name when the file is elsewhere, exactly
    /// as a Worker does. Documents are sent in requests of at most 256 documents and 16 MiB of
    /// content; a larger document goes alone. A failed request returns an error; documents in earlier requests stay stored, and repeating the call is safe. An empty
    /// list sends nothing.
    ///
    /// Returns one result for each document, in order.
    ///
    /// Since 1.45.0.
    ///
    /// ```no_run
    /// use std::time::{Duration, SystemTime};
    /// use dex_sdk::{Client, FlowDefinitionDocument, FlowDefinitionUploadOutcome, SdkResult};
    ///
    /// fn upload(client: &Client, content: Vec<u8>) -> SdkResult<()> {
    ///     let generated_at = SystemTime::UNIX_EPOCH + Duration::from_secs(1_793_491_200);
    ///     let document = FlowDefinitionDocument::new("OrderFlow", content, generated_at);
    ///     for result in client.upload_flow_definitions([document], false)? {
    ///         if result.outcome() == FlowDefinitionUploadOutcome::KeptExisting {
    ///             eprintln!("{} kept {}", result.flow_type(), result.stored_digest());
    ///         }
    ///     }
    ///     Ok(())
    /// }
    /// ```
    ///
    /// # Errors
    ///
    /// Returns [`SdkError::InvalidArgument`] before any request when a document is not a UTF-8 JSON
    /// object whose `schemaVersion` starts with `2.` and whose `flow.name` equals its Flow type.
    /// Returns a service-backed
    /// [`SdkError`] when Dex rejects a request, for example with `INVALID_ARGUMENT` for an invalid
    /// document, or with `UNIMPLEMENTED` on a Server without Flow definition storage.
    pub fn upload_flow_definitions(
        &self,
        documents: impl IntoIterator<Item = FlowDefinitionDocument>,
        force: bool,
    ) -> SdkResult<Vec<FlowDefinitionUploadResult>> {
        let documents = documents.into_iter().collect::<Vec<_>>();
        if documents.is_empty() {
            return Ok(Vec::new());
        }
        for document in &documents {
            validate_document(document).map_err(|message| SdkError::InvalidArgument { message })?;
        }
        let working_directory = crate::flow_definition::current_working_directory();
        let prepared = rewrite_source_paths(documents, working_directory.as_deref())?;
        self.require_server_protocol(UPLOAD_FLOW_DEFINITIONS)?;
        let mut results = Vec::new();
        for request in upload_requests(prepared, force) {
            let mut service = self.service.clone();
            let response = self
                .runtime
                .block_on(service.upload_flow_definitions(request))
                .map_err(|status| {
                    SdkError::from_status(
                        status,
                        UPLOAD_FLOW_DEFINITIONS,
                        None,
                        FlowTargetRequirement::None,
                    )
                })?;
            results.extend(map_upload_results(response.into_inner()));
        }
        Ok(results)
    }
}
