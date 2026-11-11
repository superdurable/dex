// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

mod flow_definition_digest;
mod flow_definition_store;
mod flow_definitions_config;
mod upload_validation;

use std::collections::BTreeSet;
use std::sync::Arc;

use bytes::Bytes;
use dexzd_clock::WallClock;
use dexzd_keyspace::TenantId;
use dexzd_object_store::{ObjectStore, ObjectStoreErrorKind, WriteCondition, WriteOutcome};
use dexzd_protocol::dex::{
    ErrorSubStatus, FlowDefinition, FlowDefinitionInfo, FlowDefinitionUploadOutcome,
    FlowDefinitionUploadResult, GetFlowDefinitionsRequest, GetFlowDefinitionsResponse,
    ListFlowDefinitionsRequest, ListFlowDefinitionsResponse, UploadFlowDefinitionsRequest,
    UploadFlowDefinitionsResponse,
};
use dexzd_protocol::dexzd::v1::FlowDefinitionRecord;
use prost::Message;
use tonic::Code;

pub use flow_definitions_config::FlowDefinitionsConfig;

use self::flow_definition_digest::FlowDefinitionDigest;
use self::flow_definition_store::{
    FlowDefinitionRead, StoredFlowDefinition, decode_canonical_key_segment, decode_page_token,
    encode_page_token, flow_definition_key, flow_definition_types_prefix, object_store_unavailable,
    read_flow_definition,
};
use self::upload_validation::{
    ValidatedUpload, unix_micros_timestamp, validate_requested_flow_types, validate_upload_request,
};
use crate::FlowOperationError;

/// Most conditional writes for one uploaded Flow type before the call gives up.
const MAX_WRITE_ATTEMPTS: usize = 16;
/// ListFlowDefinitions page size when the request sets 0, and the largest page.
const MAX_LIST_PAGE_SIZE: u32 = 1000;

/// Stores and reads the Flow Definition Graph documents that Workers upload.
///
/// Each Flow type has one object under `registry/flow-definitions/<tenant_id>/types/`. Objects
/// are created with If-None-Match and replaced with If-Match, so concurrent uploads never lose
/// a newer document. They are not part of the apply log.
pub struct FlowDefinitions {
    object_store: Arc<dyn ObjectStore>,
    clock: Arc<dyn WallClock>,
    config: FlowDefinitionsConfig,
}

impl FlowDefinitions {
    /// Creates the store over the server's primary `object_store`.
    pub fn new(
        object_store: Arc<dyn ObjectStore>,
        clock: Arc<dyn WallClock>,
        config: &FlowDefinitionsConfig,
    ) -> Self {
        Self {
            object_store,
            clock,
            config: config.clone(),
        }
    }

    /// Validates every item, then stores each one by the replace rule of UploadFlowDefinitions.
    ///
    /// Returns one result per item in request order. Returns `InvalidArgument` with
    /// `FLOW_DEFINITION_INVALID` before any write when an item is invalid, `Unavailable` when object
    /// storage fails or concurrent writers keep conflicting, and `Internal` for a corrupt object.
    pub async fn upload_flow_definitions(
        &self,
        tenant_id: TenantId,
        request: UploadFlowDefinitionsRequest,
    ) -> Result<UploadFlowDefinitionsResponse, FlowOperationError> {
        let now_unix_micros = self.clock.now_unix_micros();
        let validated = validate_upload_request(&self.config, tenant_id, request, now_unix_micros)?;
        let mut results = Vec::with_capacity(validated.uploads.len());
        for upload in validated.uploads {
            let record = FlowDefinitionRecord {
                flow_type: upload.flow_type.clone(),
                digest: upload.digest.as_bytes().to_vec(),
                generated_at_unix_micros: upload.generated_at_unix_micros,
                uploaded_at_unix_micros: now_unix_micros,
                uploader: validated.uploader.clone(),
                content: Vec::new(),
            };
            results.push(
                self.upload_flow_definition(tenant_id, upload, record, validated.is_forced)
                    .await?,
            );
        }
        Ok(UploadFlowDefinitionsResponse { results })
    }

    /// Returns one page of the Flow types that have a stored definition, from a key listing.
    ///
    /// Skips keys under the prefix that this server did not write. Returns `InvalidArgument` for
    /// a negative page size or a malformed page token, and `Unavailable` when object storage fails.
    pub async fn list_flow_definitions(
        &self,
        tenant_id: TenantId,
        request: ListFlowDefinitionsRequest,
    ) -> Result<ListFlowDefinitionsResponse, FlowOperationError> {
        let page_size = match u32::try_from(request.page_size) {
            Ok(0) => MAX_LIST_PAGE_SIZE,
            Ok(page_size) => page_size.min(MAX_LIST_PAGE_SIZE),
            Err(_) => {
                return Err(FlowOperationError::invalid_argument(
                    "page size must be non-negative",
                ));
            }
        };
        let prefix = flow_definition_types_prefix(tenant_id);
        let start_after = if request.next_page_token.is_empty() {
            None
        } else {
            let segment = decode_page_token(&request.next_page_token).ok_or_else(|| {
                FlowOperationError::invalid_argument(
                    "next_page_token is not a token that ListFlowDefinitions returned",
                )
            })?;
            Some(format!("{prefix}{segment}"))
        };
        let listing = self
            .object_store
            .list(&prefix, start_after.as_deref(), page_size)
            .await
            .map_err(|error| object_store_unavailable("list", &prefix, &error))?;
        let mut flow_types = Vec::with_capacity(listing.objects.len());
        for object in &listing.objects {
            match decode_canonical_key_segment(stored_key_segment(&prefix, &object.key)?) {
                Some(flow_type) => flow_types.push(flow_type),
                None => tracing::debug!(
                    key = %object.key,
                    "skipping an object key that is not a Flow definition record"
                ),
            }
        }
        let next_page_token = match listing.next_start_after {
            Some(key) => encode_page_token(stored_key_segment(&prefix, &key)?),
            None => String::new(),
        };
        Ok(ListFlowDefinitionsResponse {
            flow_types,
            next_page_token,
        })
    }

    /// Returns the stored definitions of the requested Flow types, in request order.
    ///
    /// Leaves out Flow types without a definition and repeats of a Flow type. Returns
    /// `InvalidArgument` for an invalid request, `ResourceExhausted` with
    /// `FLOW_DEFINITION_RESPONSE_TOO_LARGE` when the response would exceed the gRPC message
    /// limit, `Unavailable` when object storage fails, and `Internal` for a corrupt object.
    pub async fn get_flow_definitions(
        &self,
        tenant_id: TenantId,
        request: GetFlowDefinitionsRequest,
    ) -> Result<GetFlowDefinitionsResponse, FlowOperationError> {
        validate_requested_flow_types(&self.config, tenant_id, &request.flow_types)?;
        let mut requested = BTreeSet::new();
        let mut response = GetFlowDefinitionsResponse::default();
        // Sequential reads capped at the remaining budget keep memory within the response limit.
        for flow_type in &request.flow_types {
            if !requested.insert(flow_type.as_str()) {
                continue;
            }
            let remaining_bytes = self
                .config
                .max_response_bytes
                .saturating_sub(response.encoded_len());
            let key = flow_definition_key(tenant_id, flow_type);
            let read = read_flow_definition(
                self.object_store.as_ref(),
                &key,
                flow_type,
                u64::try_from(remaining_bytes).unwrap_or(u64::MAX),
            )
            .await?;
            let stored = match read {
                FlowDefinitionRead::Missing => continue,
                FlowDefinitionRead::Stored(stored) => stored,
                FlowDefinitionRead::TooLarge { .. } => return Err(self.response_too_large()),
            };
            let definition = stored_flow_definition(stored);
            let definition_bytes = prost::encoding::message::encoded_len(1, &definition);
            if definition_bytes > remaining_bytes {
                return Err(self.response_too_large());
            }
            response.definitions.push(definition);
        }
        Ok(response)
    }

    fn response_too_large(&self) -> FlowOperationError {
        FlowOperationError::rejected(
            Code::ResourceExhausted,
            ErrorSubStatus::FlowDefinitionResponseTooLarge,
            format!(
                "the requested Flow definitions exceed the {}-byte response limit; request fewer Flow types",
                self.config.max_response_bytes
            ),
        )
    }

    /// Reads the record that an upload compares with, at most one gRPC message in size.
    async fn read_stored_for_upload(
        &self,
        key: &str,
        flow_type: &str,
    ) -> Result<Option<StoredFlowDefinition>, FlowOperationError> {
        let max_object_bytes = u64::try_from(self.config.max_response_bytes).unwrap_or(u64::MAX);
        match read_flow_definition(self.object_store.as_ref(), key, flow_type, max_object_bytes)
            .await?
        {
            FlowDefinitionRead::Missing => Ok(None),
            FlowDefinitionRead::Stored(stored) => Ok(Some(stored)),
            FlowDefinitionRead::TooLarge { object_size } => Err(FlowOperationError::Internal {
                detail: format!(
                    "Flow definition object {key} is {object_size} bytes, above the {max_object_bytes}-byte read limit"
                ),
            }),
        }
    }

    /// Applies the replace rule to one Flow type, retrying when a concurrent write wins.
    async fn upload_flow_definition(
        &self,
        tenant_id: TenantId,
        upload: ValidatedUpload,
        mut record: FlowDefinitionRecord,
        is_forced: bool,
    ) -> Result<FlowDefinitionUploadResult, FlowOperationError> {
        let key = flow_definition_key(tenant_id, &upload.flow_type);
        let generated_at_unix_micros = upload.generated_at_unix_micros;
        record.content = upload.content;
        let body = Bytes::from(record.encode_to_vec());
        let mut stored = self.read_stored_for_upload(&key, &upload.flow_type).await?;
        for _ in 0..MAX_WRITE_ATTEMPTS {
            let (outcome, condition) = match &stored {
                None => (
                    FlowDefinitionUploadOutcome::Created,
                    WriteCondition::CreateOnly,
                ),
                Some(existing) if existing.digest == upload.digest => {
                    return Ok(stored_upload_result(
                        upload.flow_type,
                        FlowDefinitionUploadOutcome::Unchanged,
                        existing,
                    ));
                }
                Some(existing)
                    if is_forced
                        || generated_at_unix_micros > existing.record.generated_at_unix_micros =>
                {
                    (
                        FlowDefinitionUploadOutcome::Replaced,
                        WriteCondition::ReplaceIfEtagMatches {
                            etag: existing.etag.clone(),
                        },
                    )
                }
                Some(existing) => {
                    return Ok(stored_upload_result(
                        upload.flow_type,
                        FlowDefinitionUploadOutcome::KeptExisting,
                        existing,
                    ));
                }
            };
            match self.object_store.put(&key, body.clone(), condition).await {
                Ok(WriteOutcome::Written { .. }) => {
                    return Ok(upload_result(
                        upload.flow_type,
                        outcome,
                        upload.digest,
                        generated_at_unix_micros,
                    ));
                }
                Ok(WriteOutcome::PreconditionFailed) => {
                    stored = self.read_stored_for_upload(&key, &upload.flow_type).await?;
                }
                Err(error) if error.kind == ObjectStoreErrorKind::OutcomeUnknown => {
                    stored = self.read_stored_for_upload(&key, &upload.flow_type).await?;
                    if stored
                        .as_ref()
                        .is_some_and(|existing| is_same_record(&existing.record, &record))
                    {
                        return Ok(upload_result(
                            upload.flow_type,
                            outcome,
                            upload.digest,
                            generated_at_unix_micros,
                        ));
                    }
                }
                Err(error) => return Err(object_store_unavailable("write", &key, &error)),
            }
        }
        Err(FlowOperationError::Unavailable {
            detail: format!(
                "concurrent uploads of Flow definition {:?} kept conflicting; retry",
                upload.flow_type
            ),
        })
    }
}

/// Reports whether `stored` is the record this call wrote, after an unknown write outcome.
fn is_same_record(stored: &FlowDefinitionRecord, written: &FlowDefinitionRecord) -> bool {
    stored.digest == written.digest
        && stored.uploaded_at_unix_micros == written.uploaded_at_unix_micros
        && stored.generated_at_unix_micros == written.generated_at_unix_micros
        && stored.uploader == written.uploader
}

fn upload_result(
    flow_type: String,
    outcome: FlowDefinitionUploadOutcome,
    digest: FlowDefinitionDigest,
    generated_at_unix_micros: i64,
) -> FlowDefinitionUploadResult {
    FlowDefinitionUploadResult {
        flow_type,
        outcome: outcome.into(),
        stored_digest: digest.to_hex(),
        stored_generated_at: Some(unix_micros_timestamp(generated_at_unix_micros)),
    }
}

fn stored_upload_result(
    flow_type: String,
    outcome: FlowDefinitionUploadOutcome,
    stored: &StoredFlowDefinition,
) -> FlowDefinitionUploadResult {
    upload_result(
        flow_type,
        outcome,
        stored.digest,
        stored.record.generated_at_unix_micros,
    )
}

fn stored_flow_definition(stored: StoredFlowDefinition) -> FlowDefinition {
    let record = stored.record;
    FlowDefinition {
        info: Some(FlowDefinitionInfo {
            flow_type: record.flow_type,
            digest: stored.digest.to_hex(),
            generated_at: Some(unix_micros_timestamp(record.generated_at_unix_micros)),
            uploaded_at: Some(unix_micros_timestamp(record.uploaded_at_unix_micros)),
            uploader: record.uploader,
        }),
        content: record.content,
    }
}

fn stored_key_segment<'key>(prefix: &str, key: &'key str) -> Result<&'key str, FlowOperationError> {
    key.strip_prefix(prefix)
        .ok_or_else(|| FlowOperationError::Internal {
            detail: format!("listed object key {key} is outside {prefix}"),
        })
}
