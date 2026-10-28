// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dex_protocol::dex::WorkerErrorResponse;
use tonic::Code;

use crate::HandlerError;

// Keep the complete worker error status below default gRPC trailer limits after
// the server wraps WorkerErrorResponse in ServiceErrorResponse.
const MAX_WORKER_ERROR_DETAIL_BYTES: usize = 1024;
const MAX_WORKER_ERROR_TYPE_BYTES: usize = 256;
const MAX_WORKER_STACK_TRACE_BYTES: usize = 4 * 1024;
const ERROR_DETAIL_TRUNCATION_MARKER: &str = "\n... error detail truncated by Dex Rust SDK ...";
const ERROR_TYPE_TRUNCATION_MARKER: &str = "\n... error type truncated by Dex Rust SDK ...";
const STACK_TRACE_TRUNCATION_MARKER: &str = "\n... stack trace truncated by Dex Rust SDK ...";

pub(crate) fn worker_error_response(error: HandlerError) -> WorkerErrorResponse {
    let message = truncate_worker_failure_field(
        &error.to_string(),
        MAX_WORKER_ERROR_DETAIL_BYTES,
        ERROR_DETAIL_TRUNCATION_MARKER,
    );
    let error_type = truncate_worker_failure_field(
        error.error_type(),
        MAX_WORKER_ERROR_TYPE_BYTES,
        ERROR_TYPE_TRUNCATION_MARKER,
    );
    let stack_trace = truncate_worker_failure_field(
        &format!("{message}\n{}", error.stack_trace()),
        MAX_WORKER_STACK_TRACE_BYTES,
        STACK_TRACE_TRUNCATION_MARKER,
    );
    WorkerErrorResponse {
        detail: message.clone(),
        error_type,
        stack_trace,
        retry_after_seconds: error.retry_after_seconds(),
        grpc_status: Code::Unknown as i32,
    }
}

fn truncate_worker_failure_field(
    value: &str,
    maximum_bytes: usize,
    truncation_marker: &str,
) -> String {
    let encoded = value.as_bytes();
    if encoded.len() <= maximum_bytes {
        return value.to_string();
    }
    let mut prefix_length = maximum_bytes - truncation_marker.len();
    while prefix_length > 0 && encoded[prefix_length] & 0xc0 == 0x80 {
        prefix_length -= 1;
    }
    format!(
        "{}{}",
        std::str::from_utf8(&encoded[..prefix_length]).expect("UTF-8 boundary"),
        truncation_marker
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn worker_error_bounds_all_text_fields_at_utf8_boundaries() {
        let error = HandlerError::new(
            "世界".repeat(MAX_WORKER_ERROR_TYPE_BYTES),
            "世界".repeat(MAX_WORKER_STACK_TRACE_BYTES),
        );

        let worker = worker_error_response(error);

        assert!(worker.detail.len() <= MAX_WORKER_ERROR_DETAIL_BYTES);
        assert!(worker.error_type.len() <= MAX_WORKER_ERROR_TYPE_BYTES);
        assert!(worker.stack_trace.len() <= MAX_WORKER_STACK_TRACE_BYTES);
        assert!(
            worker
                .detail
                .ends_with("... error detail truncated by Dex Rust SDK ...")
        );
        assert!(
            worker
                .error_type
                .ends_with("... error type truncated by Dex Rust SDK ...")
        );
        assert!(!worker.detail.contains('\u{fffd}'));
        assert!(!worker.error_type.contains('\u{fffd}'));
        assert!(!worker.stack_trace.contains('\u{fffd}'));
    }

    #[test]
    fn truncation_preserves_utf8_boundaries_and_markers() {
        let truncated = truncate_worker_failure_field(
            &"世界".repeat(MAX_WORKER_STACK_TRACE_BYTES),
            MAX_WORKER_STACK_TRACE_BYTES,
            STACK_TRACE_TRUNCATION_MARKER,
        );

        assert!(truncated.len() <= MAX_WORKER_STACK_TRACE_BYTES);
        assert!(truncated.ends_with("... stack trace truncated by Dex Rust SDK ..."));
        assert!(!truncated.contains('\u{fffd}'));
    }
}
