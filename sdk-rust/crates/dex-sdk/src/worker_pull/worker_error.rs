// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dex_protocol::dex::WorkerErrorResponse;
use tonic::Status;

pub(super) fn worker_error_from_status(status: Status) -> WorkerErrorResponse {
    #[derive(prost::Message)]
    struct RpcStatus {
        #[prost(int32, tag = "1")]
        code: i32,
        #[prost(string, tag = "2")]
        message: String,
        #[prost(message, repeated, tag = "3")]
        details: Vec<prost_types::Any>,
    }
    use prost::Message;
    if let Ok(details) = RpcStatus::decode(status.details())
        && let Some(worker) = details
            .details
            .iter()
            .find(|detail| detail.type_url.ends_with("/dex.WorkerErrorResponse"))
        && let Ok(mut error) = WorkerErrorResponse::decode(worker.value.as_slice())
    {
        error.grpc_status = status.code() as i32;
        return error;
    }
    WorkerErrorResponse {
        detail: status.message().to_string(),
        grpc_status: status.code() as i32,
        ..Default::default()
    }
}
