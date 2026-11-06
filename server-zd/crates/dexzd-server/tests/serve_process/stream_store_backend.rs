// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Stream RPCs of `serve` with the default memory backend and with an explicit disabled backend.

use std::fs;

use dexzd_protocol::dex::{
    ErrorSubStatus, ListStreamMessagesRequest, ReadStreamRequest, ServiceErrorResponse,
    WriteStreamRequest,
};
use prost::Message;

use super::*;
use crate::purging_reads::{GoogleRpcStatus, SERVICE_ERROR_RESPONSE_TYPE_URL};

const STREAM_FLOW_TYPE: &str = "StreamBackendFlow";
const STREAM_FLOW_ID: &str = "stream-backend-flow-1";
const STREAM_NAME: &str = "Progress";

/// A `serve` process whose YAML has no streamStore section serves Stream writes and reads.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn serve_without_stream_store_section_serves_streams_from_memory() {
    let primary = S3Emulator::start(PRIMARY_BUCKET, SignatureVerification::Strict).await;
    let directory = TempDir::new().expect("temporary directory exists");
    let mut process = spawn_serve(&primary, &directory, None);
    let mut client = FlowServiceClient::new(process.wait_until_serving().await);

    client
        .write_stream(write_stream_request())
        .await
        .expect("WriteStream succeeds with the default memory backend");
    let read = client
        .read_stream(read_stream_request())
        .await
        .expect("ReadStream succeeds with the default memory backend")
        .into_inner();
    let message = read
        .message
        .expect("ReadStream returns the written message");
    assert_eq!(message.value, Some(string_value("step 1 of 3")));
    assert_eq!(message.source, "stream-backend-test");
    let listed = client
        .list_stream_messages(list_stream_messages_request())
        .await
        .expect("ListStreamMessages succeeds with the default memory backend")
        .into_inner();
    assert_eq!(listed.messages.len(), 1);

    let status = process.terminate(SHUTDOWN_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", process.log());
    primary.shutdown().await;
}

/// An explicit `streamStore.backend: disabled` makes every client Stream call fail loudly.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn serve_with_disabled_stream_store_rejects_stream_calls() {
    let primary = S3Emulator::start(PRIMARY_BUCKET, SignatureVerification::Strict).await;
    let directory = TempDir::new().expect("temporary directory exists");
    let mut process = spawn_serve(&primary, &directory, Some("  backend: disabled\n"));
    let mut client = FlowServiceClient::new(process.wait_until_serving().await);

    let write_status = client
        .write_stream(write_stream_request())
        .await
        .expect_err("WriteStream fails with a disabled backend");
    assert_stream_store_disabled("WriteStream", &write_status);
    let read_status = client
        .read_stream(read_stream_request())
        .await
        .expect_err("ReadStream fails with a disabled backend");
    assert_stream_store_disabled("ReadStream", &read_status);
    let list_status = client
        .list_stream_messages(list_stream_messages_request())
        .await
        .expect_err("ListStreamMessages fails with a disabled backend");
    assert_stream_store_disabled("ListStreamMessages", &list_status);

    let status = process.terminate(SHUTDOWN_BUDGET).await;
    let log = process.log();
    assert_eq!(status.code(), Some(0), "{log}");
    assert!(
        log.contains("streamStore.backend is disabled; Step Stream writes will be dropped"),
        "startup warns about the disabled backend: {log}"
    );
    primary.shutdown().await;
}

/// A WriteStream past `streamStore.maxTotalBytes` returns RESOURCE_EXHAUSTED.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn serve_rejects_a_stream_write_past_the_total_byte_bound() {
    let primary = S3Emulator::start(PRIMARY_BUCKET, SignatureVerification::Strict).await;
    let directory = TempDir::new().expect("temporary directory exists");
    let mut process = spawn_serve(
        &primary,
        &directory,
        Some("  maxTotalBytes: 4096\n  maxStreamCapacityBytes: 4096\n"),
    );
    let mut client = FlowServiceClient::new(process.wait_until_serving().await);
    let large_write = || WriteStreamRequest {
        value: Some(string_value(&"a".repeat(2048))),
        ..write_stream_request()
    };

    client
        .write_stream(large_write())
        .await
        .expect("the first write fits in maxTotalBytes");
    let status = client
        .write_stream(large_write())
        .await
        .expect_err("the second write exceeds maxTotalBytes");
    assert_service_error(
        "WriteStream",
        &status,
        tonic::Code::ResourceExhausted,
        "Stream Store total capacity is exhausted; retry after background trimming",
    );

    let exit_status = process.terminate(SHUTDOWN_BUDGET).await;
    assert_eq!(exit_status.code(), Some(0), "{}", process.log());
    primary.shutdown().await;
}

/// Starts `serve`, appending a streamStore section with `stream_store_fields` when set.
fn spawn_serve(
    primary: &S3Emulator,
    directory: &TempDir,
    stream_store_fields: Option<&str>,
) -> ServeProcess {
    let api_address = free_loopback_address();
    let configuration_path = directory.path().join("stream-store.yaml");
    ServeConfig {
        primary_endpoint: &primary.endpoint(),
        hard_delete_endpoint: None,
        listen: api_address,
        local_directory: &directory.path().join("store"),
        lease_duration: HELD_LEASE_DURATION,
        fail_if_held: true,
        parade_db: None,
    }
    .write(&configuration_path);
    if let Some(fields) = stream_store_fields {
        let mut configuration =
            fs::read_to_string(&configuration_path).expect("configuration is readable");
        configuration.push_str(&format!("streamStore:\n{fields}"));
        fs::write(&configuration_path, configuration).expect("configuration is writable");
    }
    ServeProcess::spawn(
        &configuration_path,
        api_address,
        directory.path().join("stream-store.log"),
    )
}

fn assert_stream_store_disabled(api: &str, status: &tonic::Status) {
    assert_service_error(
        api,
        status,
        tonic::Code::FailedPrecondition,
        "Stream Store is disabled",
    );
}

/// Asserts the gRPC code, the UNCATEGORIZED sub-status, and the detail of a Stream failure.
fn assert_service_error(api: &str, status: &tonic::Status, code: tonic::Code, detail: &str) {
    assert_eq!(status.code(), code, "{api}: {status:?}");
    let details = GoogleRpcStatus::decode(status.details())
        .unwrap_or_else(|error| panic!("{api}: {status:?} has no google.rpc.Status: {error}"));
    let response = details
        .details
        .iter()
        .find(|detail| detail.type_url == SERVICE_ERROR_RESPONSE_TYPE_URL)
        .map(|detail| ServiceErrorResponse::decode(detail.value.as_slice()))
        .unwrap_or_else(|| panic!("{api}: {status:?} has no ServiceErrorResponse"))
        .expect("the ServiceErrorResponse decodes");
    assert_eq!(
        response.sub_status,
        ErrorSubStatus::Uncategorized as i32,
        "{api}"
    );
    assert_eq!(response.detail, detail, "{api}");
}

fn write_stream_request() -> WriteStreamRequest {
    WriteStreamRequest {
        flow_id: STREAM_FLOW_ID.to_owned(),
        flow_type: STREAM_FLOW_TYPE.to_owned(),
        stream_name: STREAM_NAME.to_owned(),
        stream_capacity_bytes: 1 << 20,
        value: Some(string_value("step 1 of 3")),
        source: "stream-backend-test".to_owned(),
    }
}

fn read_stream_request() -> ReadStreamRequest {
    ReadStreamRequest {
        flow_id: STREAM_FLOW_ID.to_owned(),
        flow_type: STREAM_FLOW_TYPE.to_owned(),
        stream_name: STREAM_NAME.to_owned(),
        wait_time_seconds: 1,
        ..ReadStreamRequest::default()
    }
}

fn list_stream_messages_request() -> ListStreamMessagesRequest {
    ListStreamMessagesRequest {
        flow_id: STREAM_FLOW_ID.to_owned(),
        flow_type: STREAM_FLOW_TYPE.to_owned(),
        stream_name: STREAM_NAME.to_owned(),
        page_size: 10,
        ..ListStreamMessagesRequest::default()
    }
}
