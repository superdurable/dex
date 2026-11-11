// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::time::SystemTime;

use dex_protocol::dex::{
    FlowDefinitionUpload, FlowDefinitionUploadOutcome as ProtoFlowDefinitionUploadOutcome,
    FlowDefinitionUploadResult as ProtoFlowDefinitionUploadResult, UploadFlowDefinitionsRequest,
    UploadFlowDefinitionsResponse,
};

#[cfg(test)]
mod conformance_tests;
mod definition_sources;
mod generated_at;
mod source_path;
mod worker_upload;
#[cfg(test)]
mod worker_upload_tests;

pub(crate) use definition_sources::{collect_flow_definitions, validate_document};
pub(crate) use source_path::{current_working_directory, rewrite_source_paths};
pub(crate) use worker_upload::upload_flow_definitions_at_worker_start;

const MAX_DEFINITIONS_PER_REQUEST: usize = 256;
// Half the Server's default 32 MiB gRPC message limit; a larger document goes alone.
const MAX_CONTENT_BYTES_PER_REQUEST: usize = 16 * 1024 * 1024;

/// Holds one Flow Definition Graph JSON document in memory for upload to the Dex Server.
///
/// Dex Web draws a Flow type's graph from the document stored for it. Pass documents to
/// [`crate::WorkerOptions::flow_definitions`] to upload them when a Worker starts, or to
/// [`crate::Client::upload_flow_definitions`] from a deploy script. Use
/// [`crate::WorkerOptions::flow_definition_paths`] instead when the documents are files, such as
/// the output of `dexcli visualize`.
///
/// The Flow type must equal `flow.name` in the content. The content must be a UTF-8 JSON object
/// whose `schemaVersion` starts with `2.`. The Dex Server compares `generated_at` with the stored
/// document: a different document replaces the stored one only when its generation time is
/// strictly later. Pass a fixed time for a document generated from a template, such as the
/// template's release time, so every Worker sends the same time.
///
/// `Debug` prints the content length, not the content.
///
/// Since 1.45.0.
///
/// # Examples
///
/// ```no_run
/// use std::time::{Duration, SystemTime};
/// use dex_sdk::{FlowDefinitionDocument, WorkerOptions};
///
/// let content = std::fs::read("flow-definitions/OrderFlow.json")?;
/// let generated_at = SystemTime::UNIX_EPOCH + Duration::from_secs(1_793_491_200);
/// let options = WorkerOptions::new().flow_definitions([FlowDefinitionDocument::new(
///     "OrderFlow",
///     content,
///     generated_at,
/// )]);
/// # Ok::<(), Box<dyn std::error::Error>>(())
/// ```
#[derive(Clone, PartialEq, Eq)]
pub struct FlowDefinitionDocument {
    flow_type: String,
    content: Vec<u8>,
    generated_at: SystemTime,
}

impl FlowDefinitionDocument {
    /// Creates a document from its Flow type, JSON content, and generation time.
    ///
    /// `flow_type` must equal `flow.name` in `content`, and `content` must hold the UTF-8 JSON
    /// bytes of the Flow Definition Graph. The constructor does not validate them.
    /// [`crate::Worker::try_new`] returns [`crate::SdkError::FlowDefinition`], and
    /// [`crate::Client::upload_flow_definitions`] returns [`crate::SdkError::InvalidArgument`],
    /// when a document is not a UTF-8 JSON object whose `schemaVersion` starts with `2.` and whose
    /// `flow.name` equals the Flow type. The Dex Server rejects other invalid documents.
    ///
    /// Since 1.45.0.
    pub fn new(
        flow_type: impl Into<String>,
        content: impl Into<Vec<u8>>,
        generated_at: SystemTime,
    ) -> Self {
        Self {
            flow_type: flow_type.into(),
            content: content.into(),
            generated_at,
        }
    }

    /// Returns the Flow type that the document describes.
    ///
    /// Since 1.45.0.
    pub fn flow_type(&self) -> &str {
        &self.flow_type
    }

    /// Returns the UTF-8 JSON bytes of the document.
    ///
    /// Since 1.45.0.
    pub fn content(&self) -> &[u8] {
        &self.content
    }

    /// Returns the time when the document was generated.
    ///
    /// Since 1.45.0.
    pub fn generated_at(&self) -> SystemTime {
        self.generated_at
    }
}

impl std::fmt::Debug for FlowDefinitionDocument {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter
            .debug_struct("FlowDefinitionDocument")
            .field("flow_type", &self.flow_type)
            .field("content_bytes", &self.content.len())
            .field("generated_at", &self.generated_at)
            .finish()
    }
}

/// Reports what the Dex Server did with one uploaded Flow definition.
///
/// The Server decides each Flow type separately. A stored document is replaced only by a
/// different document with a strictly later generation time, unless the upload uses `force`.
/// See [`crate::Client::upload_flow_definitions`] for an example.
///
/// Since 1.45.0.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
#[non_exhaustive]
pub enum FlowDefinitionUploadOutcome {
    /// No document was stored for the Flow type; the uploaded one is now stored.
    Created,
    /// The uploaded document replaced a different, older one.
    Replaced,
    /// The stored document is identical; nothing was written.
    Unchanged,
    /// The stored document differs and is at least as new, so it stays.
    ///
    /// Upload with `force` to replace it anyway.
    KeptExisting,
    /// The Server reported an outcome that this SDK version does not know.
    Unknown,
}

/// Describes the outcome of uploading one Flow definition and the document stored afterwards.
///
/// [`crate::Client::upload_flow_definitions`] returns one result for each uploaded document, in
/// request order. The stored digest and generation time describe the document that the Server
/// holds after the call. They differ from the uploaded document only for
/// [`FlowDefinitionUploadOutcome::KeptExisting`].
///
/// Since 1.45.0.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct FlowDefinitionUploadResult {
    flow_type: String,
    outcome: FlowDefinitionUploadOutcome,
    stored_digest: String,
    stored_generated_at: Option<SystemTime>,
}

impl FlowDefinitionUploadResult {
    /// Returns the Flow type of the uploaded document.
    ///
    /// Since 1.45.0.
    pub fn flow_type(&self) -> &str {
        &self.flow_type
    }

    /// Returns what the Server did with the uploaded document.
    ///
    /// Since 1.45.0.
    pub fn outcome(&self) -> FlowDefinitionUploadOutcome {
        self.outcome
    }

    /// Returns the lowercase hexadecimal SHA-256 digest of the document stored after the call.
    ///
    /// Since 1.45.0.
    pub fn stored_digest(&self) -> &str {
        &self.stored_digest
    }

    /// Returns the generation time of the document stored after the call.
    ///
    /// Returns the time at microsecond precision, or `None` when the Server did not report one
    /// or it is outside the platform's `SystemTime` range.
    ///
    /// Since 1.45.0.
    pub fn stored_generated_at(&self) -> Option<SystemTime> {
        self.stored_generated_at
    }
}

fn uploader() -> String {
    format!("sdk-rust/{}", crate::server_protocol::SDK_VERSION)
}

/// Builds upload requests of at most 256 documents each, in document order.
pub(crate) fn upload_requests(
    documents: Vec<FlowDefinitionDocument>,
    force: bool,
) -> Vec<UploadFlowDefinitionsRequest> {
    let mut requests: Vec<UploadFlowDefinitionsRequest> = Vec::new();
    for document in documents {
        let needs_new_request = requests.last().is_none_or(|request| {
            request.definitions.len() == MAX_DEFINITIONS_PER_REQUEST
                || request_content_bytes(request) + document.content.len()
                    > MAX_CONTENT_BYTES_PER_REQUEST
        });
        if needs_new_request {
            requests.push(UploadFlowDefinitionsRequest {
                definitions: Vec::new(),
                uploader: uploader(),
                force,
            });
        }
        let request = requests
            .last_mut()
            .expect("a request was pushed for this document");
        request.definitions.push(FlowDefinitionUpload {
            flow_type: document.flow_type,
            content: document.content,
            generated_at: Some(generated_at::truncate_timestamp_to_millis(
                prost_types::Timestamp::from(document.generated_at),
            )),
        });
    }
    requests
}

fn request_content_bytes(request: &UploadFlowDefinitionsRequest) -> usize {
    request
        .definitions
        .iter()
        .map(|definition| definition.content.len())
        .sum()
}

pub(crate) fn map_upload_results(
    response: UploadFlowDefinitionsResponse,
) -> Vec<FlowDefinitionUploadResult> {
    response
        .results
        .into_iter()
        .map(map_upload_result)
        .collect()
}

pub(crate) fn map_upload_result(
    result: ProtoFlowDefinitionUploadResult,
) -> FlowDefinitionUploadResult {
    let outcome = match ProtoFlowDefinitionUploadOutcome::try_from(result.outcome) {
        Ok(ProtoFlowDefinitionUploadOutcome::Created) => FlowDefinitionUploadOutcome::Created,
        Ok(ProtoFlowDefinitionUploadOutcome::Replaced) => FlowDefinitionUploadOutcome::Replaced,
        Ok(ProtoFlowDefinitionUploadOutcome::Unchanged) => FlowDefinitionUploadOutcome::Unchanged,
        Ok(ProtoFlowDefinitionUploadOutcome::KeptExisting) => {
            FlowDefinitionUploadOutcome::KeptExisting
        }
        Ok(ProtoFlowDefinitionUploadOutcome::Unspecified) | Err(_) => {
            FlowDefinitionUploadOutcome::Unknown
        }
    };
    FlowDefinitionUploadResult {
        flow_type: result.flow_type,
        outcome,
        stored_digest: result.stored_digest,
        // A time outside the SystemTime range is reported as None, as documented.
        stored_generated_at: result
            .stored_generated_at
            .and_then(|timestamp| SystemTime::try_from(timestamp).ok()),
    }
}
