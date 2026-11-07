// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! GetHistoryEvents over runs whose Steps write multi-MiB values: each page fits the gRPC
//! message limit, and the page token loop that Dex Web and dexcli use reaches the last event.

use std::collections::BTreeSet;

use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use dexzd_protocol::dex::{
    AttributeWrite, FlowHistoryEvent, FlowStatus, GetHistoryEventsRequest,
    InvokeExecuteMethodResponse, LoadBlobRequestEntry, LoadBlobsRequest, StepDecision,
    StepExecuteCompletedOutput, StepMovement, StepOptions, Value, WaitForFlowRequest,
    flow_history_event, value,
};
use prost::Message;
use tonic::transport::Channel;

use crate::harness::{
    DevServer, ScriptedWorker, execute_request, execute_result, force_complete, start_pull_flow,
};

const FLOW_TYPE: &str = "LargeHistoryFlow";
const TURN_STEP_TYPE: &str = "TurnStep";
/// The default `api.grpcMaxMessageBytes`, which is also the SDK and Dex Web receive limit.
const GRPC_MAX_MESSAGE_BYTES: usize = 32 * 1024 * 1024;
const LARGE_VALUE_BYTES: usize = 2 * 1024 * 1024;
/// Twenty 2 MiB values exceed one gRPC message.
const TURNS: usize = 20;

/// Each Step rewrites a 2 MiB Attribute, like an agent session that keeps its transcript.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn history_of_steps_rewriting_a_large_attribute_pages_to_the_end() {
    let server = DevServer::start().await;
    let mut flow_client = history_client(&server);
    let mut worker = ScriptedWorker::start(server.worker_client(), FLOW_TYPE).await;
    let flow_id = "large-attribute-history";
    start_turn_flow(&mut flow_client, flow_id).await;

    let mut transcripts = Vec::with_capacity(TURNS);
    for turn in 0..TURNS {
        let task = worker.next_task().await;
        execute_request(&task);
        let transcript = large_text(turn);
        worker.reply(
            task.task_id.clone(),
            execute_result(InvokeExecuteMethodResponse {
                upsert_attributes: vec![AttributeWrite {
                    key: "transcript".to_owned(),
                    value: Some(transcript.clone()),
                    ..AttributeWrite::default()
                }],
                step_decision: Some(next_decision(turn, text_value("next turn"))),
                ..InvokeExecuteMethodResponse::default()
            }),
        );
        transcripts.push(transcript);
    }
    assert_flow_completed(&mut flow_client, flow_id).await;

    let pages = read_history_pages(&mut flow_client, flow_id).await;
    let events: Vec<FlowHistoryEvent> = pages.into_iter().flatten().collect();
    assert_contiguous_from_first_event(&events);
    let outputs = execute_completed_outputs(&events);
    assert_eq!(outputs.len(), TURNS, "one completed event per turn");
    for output in &outputs {
        let transcript = output.upsert_attributes[0].value.as_ref();
        assert!(
            is_value_reference(transcript),
            "a completed event references the 2 MiB Attribute instead of embedding it"
        );
    }
    let last_transcript = outputs[TURNS - 1].upsert_attributes[0].value.as_ref();
    assert_eq!(
        load_value(&mut flow_client, flow_id, last_transcript).await,
        transcripts[TURNS - 1],
        "LoadBlobs resolves the newest transcript reference"
    );
    worker.stop().await;
    server.shutdown().await;
}

/// Each Step moves to the next with a 2 MiB input, which completed events still embed.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn history_of_steps_passing_large_inputs_pages_within_the_message_limit() {
    let server = DevServer::start().await;
    let mut flow_client = history_client(&server);
    let mut worker = ScriptedWorker::start(server.worker_client(), FLOW_TYPE).await;
    let flow_id = "large-input-history";
    start_turn_flow(&mut flow_client, flow_id).await;

    for turn in 0..TURNS {
        let task = worker.next_task().await;
        execute_request(&task);
        worker.reply(
            task.task_id.clone(),
            execute_result(InvokeExecuteMethodResponse {
                step_decision: Some(next_decision(turn, large_text(turn))),
                ..InvokeExecuteMethodResponse::default()
            }),
        );
    }
    assert_flow_completed(&mut flow_client, flow_id).await;

    let pages = read_history_pages(&mut flow_client, flow_id).await;
    let event_bytes: usize = pages
        .iter()
        .flatten()
        .map(|event| prost::encoding::message::encoded_len(1, event))
        .sum();
    assert!(
        pages.len() >= event_bytes.div_ceil(GRPC_MAX_MESSAGE_BYTES),
        "{event_bytes} event bytes need more than {} pages",
        pages.len()
    );
    let events: Vec<FlowHistoryEvent> = pages.into_iter().flatten().collect();
    assert_contiguous_from_first_event(&events);
    assert_eq!(execute_completed_outputs(&events).len(), TURNS);
    worker.stop().await;
    server.shutdown().await;
}

/// A FlowService client that receives messages up to the default gRPC limit, as SDKs do.
fn history_client(server: &DevServer) -> FlowServiceClient<Channel> {
    server
        .flow_client()
        .max_decoding_message_size(GRPC_MAX_MESSAGE_BYTES)
}

async fn start_turn_flow(flow_client: &mut FlowServiceClient<Channel>, flow_id: &str) {
    start_pull_flow(
        flow_client,
        flow_id,
        FLOW_TYPE,
        TURN_STEP_TYPE,
        text_value("first turn"),
        turn_step_options(),
    )
    .await;
}

fn turn_step_options() -> StepOptions {
    StepOptions {
        skip_wait_for: true,
        ..StepOptions::default()
    }
}

/// Moves to the next turn with `input`, or completes the Flow after the last turn.
fn next_decision(turn: usize, input: Value) -> StepDecision {
    if turn + 1 == TURNS {
        return force_complete(text_value("done"));
    }
    StepDecision {
        next_steps: vec![StepMovement {
            step_type: TURN_STEP_TYPE.to_owned(),
            step_input: Some(input),
            step_options: Some(turn_step_options()),
            ..StepMovement::default()
        }],
        ..StepDecision::default()
    }
}

/// Follows page tokens from the first event, as Dex Web does, and returns every page.
///
/// Fails when a page is empty, exceeds the message limit, or repeats a token.
async fn read_history_pages(
    flow_client: &mut FlowServiceClient<Channel>,
    flow_id: &str,
) -> Vec<Vec<FlowHistoryEvent>> {
    let mut pages = Vec::new();
    let mut seen_page_tokens = BTreeSet::new();
    let mut start_internal_event_id = 1;
    let mut next_page_token = Vec::new();
    loop {
        let response = flow_client
            .get_history_events(GetHistoryEventsRequest {
                flow_id: flow_id.to_owned(),
                start_internal_event_id,
                next_page_token: next_page_token.clone(),
                ..GetHistoryEventsRequest::default()
            })
            .await
            .unwrap_or_else(|status| panic!("page {} reads: {status:?}", pages.len() + 1))
            .into_inner();
        assert!(
            response.encoded_len() <= GRPC_MAX_MESSAGE_BYTES,
            "page {} is {} bytes",
            pages.len() + 1,
            response.encoded_len()
        );
        assert!(!response.events.is_empty(), "every page holds an event");
        let is_last_page = response.next_page_token.is_empty();
        assert!(
            is_last_page || seen_page_tokens.insert(response.next_page_token.clone()),
            "a page token never repeats"
        );
        start_internal_event_id = response.next_internal_event_id;
        next_page_token = response.next_page_token;
        pages.push(response.events);
        if is_last_page {
            return pages;
        }
    }
}

fn assert_contiguous_from_first_event(events: &[FlowHistoryEvent]) {
    let event_ids: Vec<i64> = events.iter().map(|event| event.event_id).collect();
    let expected: Vec<i64> =
        (1..=i64::try_from(events.len()).expect("event count fits i64")).collect();
    assert_eq!(
        event_ids, expected,
        "the pages hold every event once, in order"
    );
}

fn execute_completed_outputs(events: &[FlowHistoryEvent]) -> Vec<&StepExecuteCompletedOutput> {
    events
        .iter()
        .filter_map(|event| match event.payload.as_ref() {
            Some(flow_history_event::Payload::StepExecuteCompleted(completed)) => {
                completed.output.as_ref()
            }
            _ => None,
        })
        .collect()
}

async fn assert_flow_completed(flow_client: &mut FlowServiceClient<Channel>, flow_id: &str) {
    let result = flow_client
        .wait_for_flow(WaitForFlowRequest {
            flow_id: flow_id.to_owned(),
            wait_time_seconds: 30,
            ..WaitForFlowRequest::default()
        })
        .await
        .expect("WaitForFlow succeeds")
        .into_inner();
    assert_eq!(
        result.flow_status,
        FlowStatus::Completed as i32,
        "{result:?}"
    );
}

fn is_value_reference(value: Option<&Value>) -> bool {
    matches!(
        value.and_then(|value| value.kind.as_ref()),
        Some(
            value::Kind::InternalBlobIdForStringValue(_)
                | value::Kind::InternalBlobIdForObjValue(_)
        )
    )
}

/// Returns the value a reference names, resolved through LoadBlobs.
async fn load_value(
    flow_client: &mut FlowServiceClient<Channel>,
    flow_id: &str,
    reference: Option<&Value>,
) -> Value {
    let reference = reference.expect("the event carries the value").clone();
    let Some(
        value::Kind::InternalBlobIdForStringValue(reference_text)
        | value::Kind::InternalBlobIdForObjValue(reference_text),
    ) = reference.kind.clone()
    else {
        panic!("expected a value reference, got {reference:?}");
    };
    flow_client
        .load_blobs(LoadBlobsRequest {
            entries: vec![LoadBlobRequestEntry {
                flow_id: flow_id.to_owned(),
                blob_value: Some(reference),
            }],
        })
        .await
        .expect("LoadBlobs succeeds")
        .into_inner()
        .values
        .remove(&reference_text)
        .unwrap_or_else(|| panic!("LoadBlobs omitted {reference_text}"))
}

/// A distinct 2 MiB string for each turn.
fn large_text(turn: usize) -> Value {
    let mut text = format!("turn {turn}: ");
    text.extend(std::iter::repeat_n('x', LARGE_VALUE_BYTES - text.len()));
    text_value(&text)
}

fn text_value(text: &str) -> Value {
    Value {
        kind: Some(value::Kind::StringValue(text.to_owned())),
    }
}
