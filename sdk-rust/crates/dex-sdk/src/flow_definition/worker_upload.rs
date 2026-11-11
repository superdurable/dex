// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeSet;
use std::time::Duration;

use dex_protocol::dex::FlowDefinitionUpload;
use dex_protocol::dex::flow_service_client::FlowServiceClient;
use sha2::{Digest, Sha256};
use tonic::Code;

use super::current_working_directory;
use super::{
    FlowDefinitionDocument, FlowDefinitionUploadOutcome, FlowDefinitionUploadResult,
    map_upload_results, rewrite_source_paths, upload_requests,
};
use crate::credential::CredentialChannel;

/// Uploads the registered Flow types' definitions; every failure is logged, never returned.
///
/// Each request gets its own `timeout`.
pub(crate) async fn upload_flow_definitions_at_worker_start(
    service: &FlowServiceClient<CredentialChannel>,
    timeout: Duration,
    documents: &[FlowDefinitionDocument],
    registered_flow_types: &BTreeSet<String>,
) {
    let mut registered = Vec::new();
    for document in documents {
        if registered_flow_types.contains(&document.flow_type) {
            registered.push(document.clone());
        } else {
            tracing::info!(
                flow_type = %document.flow_type,
                "skipping a Flow definition whose Flow type this Worker does not register"
            );
        }
    }
    if registered.is_empty() {
        return;
    }
    let prepared = match current_working_directory()
        .map_err(|error| error.to_string())
        .and_then(|working_directory| {
            rewrite_source_paths(registered, &working_directory).map_err(|error| error.to_string())
        }) {
        Ok(prepared) => prepared,
        Err(error) => {
            tracing::warn!(%error, "cannot prepare Flow definitions for upload; skipping the upload");
            return;
        }
    };
    for request in upload_requests(prepared, false) {
        let uploaded = request
            .definitions
            .iter()
            .map(UploadedDefinition::describe)
            .collect::<Vec<_>>();
        let mut service = service.clone();
        let response = match tokio::time::timeout(timeout, service.upload_flow_definitions(request))
            .await
        {
            Ok(Ok(response)) => response.into_inner(),
            Ok(Err(status)) if status.code() == Code::Unimplemented => {
                tracing::warn!(
                    "the Dex Server does not support Flow definition upload; upgrade the Server to show these Flow definitions in Dex Web"
                );
                return;
            }
            Ok(Err(status)) => {
                tracing::warn!(
                    code = ?status.code(),
                    message = status.message(),
                    "cannot upload Flow definitions; the Worker continues without them"
                );
                return;
            }
            Err(elapsed) => {
                tracing::warn!(
                    error = %elapsed,
                    "Flow definition upload timed out; the Worker continues without them"
                );
                return;
            }
        };
        log_upload_results(&uploaded, map_upload_results(response));
    }
}

/// Identifies one sent document in a KEPT_EXISTING warning.
struct UploadedDefinition {
    digest: String,
    generated_at: String,
}

impl UploadedDefinition {
    fn describe(upload: &FlowDefinitionUpload) -> Self {
        Self {
            digest: hex_digest(&upload.content),
            generated_at: upload
                .generated_at
                .map(|timestamp| timestamp.to_string())
                .unwrap_or_default(),
        }
    }
}

fn log_upload_results(uploaded: &[UploadedDefinition], results: Vec<FlowDefinitionUploadResult>) {
    for (index, result) in results.into_iter().enumerate() {
        match result.outcome {
            FlowDefinitionUploadOutcome::Created | FlowDefinitionUploadOutcome::Replaced => {
                tracing::info!(
                    flow_type = %result.flow_type,
                    outcome = ?result.outcome,
                    digest = %result.stored_digest,
                    "uploaded a Flow definition"
                );
            }
            FlowDefinitionUploadOutcome::Unchanged => {
                tracing::debug!(
                    flow_type = %result.flow_type,
                    digest = %result.stored_digest,
                    "the stored Flow definition is unchanged"
                );
            }
            FlowDefinitionUploadOutcome::KeptExisting | FlowDefinitionUploadOutcome::Unknown => {
                log_kept_existing(&result, uploaded.get(index));
            }
        }
    }
}

fn log_kept_existing(result: &FlowDefinitionUploadResult, uploaded: Option<&UploadedDefinition>) {
    let uploaded_digest = uploaded
        .map(|upload| upload.digest.as_str())
        .unwrap_or_default();
    let uploaded_generated_at = uploaded
        .map(|upload| upload.generated_at.as_str())
        .unwrap_or_default();
    let stored_generated_at = result
        .stored_generated_at
        .map(|time| prost_types::Timestamp::from(time).to_string())
        .unwrap_or_default();
    tracing::warn!(
        flow_type = %result.flow_type,
        outcome = ?result.outcome,
        stored_digest = %result.stored_digest,
        stored_generated_at,
        uploaded_digest,
        uploaded_generated_at,
        "the Dex Server kept its stored Flow definition; regenerate the document or upload it with force to replace it"
    );
}

fn hex_digest(content: &[u8]) -> String {
    Sha256::digest(content)
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect()
}
