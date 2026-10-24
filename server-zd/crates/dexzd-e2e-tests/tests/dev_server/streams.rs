// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Stream writes from clients and Workers are read back through the Stream API.

use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use dexzd_protocol::dex::{
    InvokeExecuteMethodOutput, InvokeExecuteMethodResponse, ListStreamMessagesRequest,
    ReadStreamRequest, StepOptions, StepStreamWrite, StreamMessage, WriteStreamRequest,
    invoke_execute_method_output, poll_worker_task_frame,
};
use tonic::transport::Channel;

use crate::harness::{DevServer, ScriptedWorker, execute_result, force_complete, start_pull_flow};
use crate::string_value;

const STREAM_FLOW_TYPE: &str = "ProgressFlow";
const STREAM_NAME: &str = "progress";
const STREAM_CAPACITY_BYTES: i64 = 1024 * 1024;

/// ReadStream follows resume tokens oldest first; ListStreamMessages pages newest first.
#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn client_stream_writes_are_read_in_order_and_listed_newest_first() {
    let server = DevServer::start().await;
    let mut flow_client = server.flow_client();
    let flow_id = "client-stream-flow";
    for (text, source) in [
        ("10%", "importer"),
        ("50%", "importer"),
        ("100%", "reporter"),
    ] {
        flow_client
            .write_stream(WriteStreamRequest {
                flow_id: flow_id.to_owned(),
                flow_type: STREAM_FLOW_TYPE.to_owned(),
                stream_name: STREAM_NAME.to_owned(),
                stream_capacity_bytes: STREAM_CAPACITY_BYTES,
                value: Some(string_value(text)),
                source: source.to_owned(),
            })
            .await
            .expect("WriteStream succeeds");
    }

    let mut read = Vec::new();
    let mut resume_token = String::new();
    for _ in 0..3 {
        let message = read_after(&mut flow_client, flow_id, &resume_token).await;
        resume_token.clone_from(&message.resume_token);
        read.push(message);
    }
    assert_eq!(
        read.iter()
            .map(|message| (message.value.clone(), message.source.as_str()))
            .collect::<Vec<_>>(),
        vec![
            (Some(string_value("10%")), "importer"),
            (Some(string_value("50%")), "importer"),
            (Some(string_value("100%")), "reporter"),
        ]
    );
    assert!(read.iter().all(|message| message.created_time.is_some()));
    let after_last = flow_client
        .read_stream(ReadStreamRequest {
            flow_id: flow_id.to_owned(),
            flow_type: STREAM_FLOW_TYPE.to_owned(),
            stream_name: STREAM_NAME.to_owned(),
            resume_token,
            wait_time_seconds: 1,
        })
        .await
        .expect_err("no message follows the last one");
    assert_eq!(after_last.code(), tonic::Code::DeadlineExceeded);
    assert_eq!(after_last.message(), "Stream read timed out");

    let newest = list_page(&mut flow_client, flow_id, "").await;
    assert_eq!(newest.messages, vec![read[2].clone(), read[1].clone()]);
    assert_eq!(newest.next_page_token, read[1].resume_token);
    let oldest = list_page(&mut flow_client, flow_id, &newest.next_page_token).await;
    assert_eq!(oldest.messages, vec![read[0].clone()]);
    assert!(oldest.next_page_token.is_empty());

    let without_source = flow_client
        .write_stream(WriteStreamRequest {
            flow_id: flow_id.to_owned(),
            flow_type: STREAM_FLOW_TYPE.to_owned(),
            stream_name: STREAM_NAME.to_owned(),
            stream_capacity_bytes: STREAM_CAPACITY_BYTES,
            value: Some(string_value("lost")),
            source: String::new(),
        })
        .await
        .expect_err("a client write needs a source");
    assert_eq!(without_source.code(), tonic::Code::InvalidArgument);
    assert_eq!(without_source.message(), "Stream source is required");
    server.shutdown().await;
}

/// A Step's Stream write is readable with the Step execution as its source.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn worker_stream_writes_are_read_with_the_step_execution_source() {
    let server = DevServer::start().await;
    let mut flow_client = server.flow_client();
    let mut worker = ScriptedWorker::start(server.worker_client(), STREAM_FLOW_TYPE).await;
    let flow_id = "worker-stream-flow";
    start_pull_flow(
        &mut flow_client,
        flow_id,
        STREAM_FLOW_TYPE,
        "ImportStep",
        string_value("import"),
        StepOptions {
            skip_wait_for: true,
            ..StepOptions::default()
        },
    )
    .await;
    let task = worker.next_task().await;
    worker.reply(
        task.task_id.clone(),
        poll_worker_task_frame::Output::Execute(InvokeExecuteMethodOutput {
            output: Some(invoke_execute_method_output::Output::StreamWrite(
                StepStreamWrite {
                    stream_name: STREAM_NAME.to_owned(),
                    stream_capacity_bytes: STREAM_CAPACITY_BYTES,
                    value: Some(string_value("halfway")),
                },
            )),
        }),
    );
    let message = read_after(&mut flow_client, flow_id, "").await;
    assert_eq!(message.value, Some(string_value("halfway")));
    assert_eq!(message.source, "#ImportStep-1");

    worker.reply(
        task.task_id.clone(),
        execute_result(InvokeExecuteMethodResponse {
            step_decision: Some(force_complete(string_value("imported"))),
            ..InvokeExecuteMethodResponse::default()
        }),
    );
    let listed = list_page(&mut flow_client, flow_id, "").await;
    assert_eq!(listed.messages, vec![message]);
    worker.stop().await;
    server.shutdown().await;
}

async fn read_after(
    flow_client: &mut FlowServiceClient<Channel>,
    flow_id: &str,
    resume_token: &str,
) -> StreamMessage {
    flow_client
        .read_stream(ReadStreamRequest {
            flow_id: flow_id.to_owned(),
            flow_type: STREAM_FLOW_TYPE.to_owned(),
            stream_name: STREAM_NAME.to_owned(),
            resume_token: resume_token.to_owned(),
            wait_time_seconds: 10,
        })
        .await
        .expect("ReadStream returns a message")
        .into_inner()
        .message
        .expect("the response has a message")
}

async fn list_page(
    flow_client: &mut FlowServiceClient<Channel>,
    flow_id: &str,
    before_page_token: &str,
) -> dexzd_protocol::dex::ListStreamMessagesResponse {
    flow_client
        .list_stream_messages(ListStreamMessagesRequest {
            flow_id: flow_id.to_owned(),
            flow_type: STREAM_FLOW_TYPE.to_owned(),
            stream_name: STREAM_NAME.to_owned(),
            page_size: 2,
            before_page_token: before_page_token.to_owned(),
        })
        .await
        .expect("ListStreamMessages succeeds")
        .into_inner()
}
