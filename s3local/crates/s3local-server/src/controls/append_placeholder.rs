// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The `append_placeholder` and `interrupted_append` faults: the empty object of a COS append.

use std::sync::Arc;

use s3local::{
    ChecksumAlgorithm, PutObjectRequest, Store, StoreError, UploadChecksumExpectations,
    WriteCondition,
};

use crate::server::run_store_operation;

/// Creates an empty appendable object without metadata at a missing `key`, logging any error.
pub(crate) async fn create_append_placeholder(store: &Arc<Store>, bucket: &str, key: &str) {
    let (bucket, key) = (bucket.to_owned(), key.to_owned());
    let created = run_store_operation(store, move |store| {
        let writer = store.begin_put(&bucket, &key, ChecksumAlgorithm::Crc64Nvme)?;
        let staged = writer.finish(&UploadChecksumExpectations::default())?;
        let request = PutObjectRequest {
            write_condition: WriteCondition::IfNoneMatchAny,
            is_appendable: true,
            ..PutObjectRequest::default()
        };
        match store.commit_put(&bucket, &key, staged, request) {
            Ok(_) | Err(StoreError::PreconditionFailed) => Ok(()),
            Err(error) => Err(error),
        }
    })
    .await;
    if let Err(error) = created {
        tracing::warn!(?error, "failed to create the append placeholder");
    }
}
