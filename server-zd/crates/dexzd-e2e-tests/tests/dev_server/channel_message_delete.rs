// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! DeleteChannelMessage removes one pending message by its ID and deduplicates by request ID.

use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use dexzd_protocol::dex::{
    ChannelMessage, ChannelMessageDeletion, DeleteChannelMessageRequest, ErrorSubStatus,
    GetChannelMessagesRequest, GetFlowStateRequest, GetHistoryEventsRequest,
    PublishToChannelRequest, StepOptions, flow_history_event,
};
use tonic::transport::Channel;

use crate::harness::{DevServer, service_error, start_pull_flow};
use crate::string_value;

/// No Worker serves this Flow type, so no Step consumes the published messages.
const FLOW_TYPE: &str = "IdleChannelFlow";
const FLOW_ID: &str = "delete-channel-message-flow";
const CHANNEL_NAME: &str = "orders";

/// A delete removes exactly one message; a retry with its request ID succeeds without changes.
#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn delete_channel_message_removes_one_pending_message_per_request_id() {
    let server = DevServer::start().await;
    let mut flow_client = server.flow_client();
    let started = start_pull_flow(
        &mut flow_client,
        FLOW_ID,
        FLOW_TYPE,
        "IdleStep",
        string_value("idle"),
        StepOptions::default(),
    )
    .await;
    flow_client
        .publish_to_channel(PublishToChannelRequest {
            flow_id: FLOW_ID.to_owned(),
            messages: ["first", "second", "third"]
                .into_iter()
                .map(|text| ChannelMessage {
                    channel_name: CHANNEL_NAME.to_owned(),
                    value: Some(string_value(text)),
                    ..ChannelMessage::default()
                })
                .collect(),
            ..PublishToChannelRequest::default()
        })
        .await
        .expect("PublishToChannel succeeds");
    let published = pending_messages(&mut flow_client).await;
    assert_eq!(published.len(), 3, "{published:?}");
    let deleted_id = published[0].message_id.clone();
    let remaining = published[1..].to_vec();

    delete(&mut flow_client, &deleted_id, "delete-first")
        .await
        .expect("DeleteChannelMessage succeeds");
    assert_eq!(pending_messages(&mut flow_client).await, remaining);
    let state = flow_client
        .get_flow_state(GetFlowStateRequest {
            flow_id: FLOW_ID.to_owned(),
            ..GetFlowStateRequest::default()
        })
        .await
        .expect("GetFlowState succeeds")
        .into_inner();
    assert_eq!(
        state
            .pending_channel_messages
            .get(CHANNEL_NAME)
            .map(|values| values.messages.clone()),
        Some(remaining.clone())
    );

    delete(&mut flow_client, &deleted_id, "delete-first")
        .await
        .expect("a retried delete returns the recorded success");
    assert_eq!(pending_messages(&mut flow_client).await, remaining);

    let reused = delete(&mut flow_client, &remaining[0].message_id, "delete-first")
        .await
        .expect_err("a request ID cannot name a different delete");
    assert_eq!(reused.code(), tonic::Code::InvalidArgument);
    assert_eq!(
        reused.message(),
        "request ID delete-first was already used for a different request"
    );
    assert_eq!(pending_messages(&mut flow_client).await, remaining);

    let missing = delete(&mut flow_client, &deleted_id, "delete-first-again")
        .await
        .expect_err("a deleted message is no longer pending");
    assert_eq!(missing.code(), tonic::Code::NotFound);
    assert_eq!(
        missing.message(),
        format!("Channel message {deleted_id:?} was not found in channel {CHANNEL_NAME:?}")
    );
    assert_eq!(
        service_error(&missing).sub_status,
        ErrorSubStatus::ChannelMessageNotFound as i32
    );
    let unknown = delete(&mut flow_client, "unknown-message-id", "delete-unknown")
        .await
        .expect_err("an unknown message ID is not pending");
    assert_eq!(unknown.code(), tonic::Code::NotFound);
    assert_eq!(
        service_error(&unknown).sub_status,
        ErrorSubStatus::ChannelMessageNotFound as i32
    );
    assert_eq!(pending_messages(&mut flow_client).await, remaining);

    let deletions: Vec<_> = flow_client
        .get_history_events(GetHistoryEventsRequest {
            flow_id: FLOW_ID.to_owned(),
            run_id: started.run_id,
            estimate_page_size: 100,
            ..GetHistoryEventsRequest::default()
        })
        .await
        .expect("GetHistoryEvents succeeds")
        .into_inner()
        .events
        .into_iter()
        .filter_map(|event| match event.payload {
            Some(flow_history_event::Payload::ChannelExternalDelete(delete)) => {
                Some(delete.messages)
            }
            _ => None,
        })
        .collect();
    assert_eq!(
        deletions,
        vec![vec![ChannelMessageDeletion {
            channel_name: CHANNEL_NAME.to_owned(),
            message_id: deleted_id,
        }]],
        "only the first delete is recorded"
    );
    server.shutdown().await;
}

async fn delete(
    flow_client: &mut FlowServiceClient<Channel>,
    message_id: &str,
    request_id: &str,
) -> Result<tonic::Response<()>, tonic::Status> {
    flow_client
        .delete_channel_message(DeleteChannelMessageRequest {
            flow_id: FLOW_ID.to_owned(),
            channel_name: CHANNEL_NAME.to_owned(),
            message_id: message_id.to_owned(),
            request_id: request_id.to_owned(),
            ..DeleteChannelMessageRequest::default()
        })
        .await
}

async fn pending_messages(flow_client: &mut FlowServiceClient<Channel>) -> Vec<ChannelMessage> {
    flow_client
        .get_channel_messages(GetChannelMessagesRequest {
            flow_id: FLOW_ID.to_owned(),
            channel_name: CHANNEL_NAME.to_owned(),
            ..GetChannelMessagesRequest::default()
        })
        .await
        .expect("GetChannelMessages succeeds")
        .into_inner()
        .messages
}
