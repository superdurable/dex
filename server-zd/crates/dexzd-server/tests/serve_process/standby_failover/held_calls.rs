// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Public calls that stay open at one process while it steps down or shuts down.

use std::net::SocketAddr;
use std::time::Duration;

use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use dexzd_protocol::dex::{
    AttributeWrite, FlowConfig, FlowStartOptions, GetAttributesRequest, ReadStreamRequest,
    StartFlowRequest, StepOptions, WorkerTarget,
};
use tokio::task::JoinHandle;
use tokio::time::Instant;
use tonic::transport::Channel;

use crate::string_value;

/// Lets a held call reach the process before the test acts on it.
pub(super) const HELD_CALL_START_WINDOW: Duration = Duration::from_millis(300);
/// What a public call answers when a step down ends it.
pub(super) const STOPPED_SERVING_REFUSAL: &str = "stopped serving during the call";
/// A Flow type that no Worker serves, so its first Step stays pending.
const HELD_CALL_FLOW_TYPE: &str = "HeldCallFlow";
const HELD_CALL_STEP_TYPE: &str = "HeldCallStep";
const HARD_DELETE_ATTRIBUTE: &str = "secret";
/// Longer than any step down or shutdown, so only the stopping process ends the read.
const STREAM_WAIT_SECONDS: i32 = 30;
const START_ATTEMPTS: usize = 100;
const START_RETRY_INTERVAL: Duration = Duration::from_millis(100);

/// A public call that runs at one process until that process ends it.
pub(super) struct HeldCall {
    task: JoinHandle<(Result<(), tonic::Status>, Instant)>,
}

impl HeldCall {
    /// Starts a ReadStream at `address` that waits for a Stream message that never comes.
    pub(super) fn wait_for_stream_message(address: SocketAddr, flow_id: &str) -> Self {
        let request = ReadStreamRequest {
            flow_id: flow_id.to_owned(),
            flow_type: HELD_CALL_FLOW_TYPE.to_owned(),
            stream_name: "never-written".to_owned(),
            resume_token: String::new(),
            wait_time_seconds: STREAM_WAIT_SECONDS,
        };
        let mut client = direct_client(address);
        Self::spawn(async move { client.read_stream(request).await.map(drop) })
    }

    /// Starts a GetAttributes of `flow_id`'s hard-delete Attribute at `address`.
    ///
    /// A held hard-delete object read keeps the call open.
    pub(super) fn read_hard_delete_attribute(address: SocketAddr, flow_id: &str) -> Self {
        let request = GetAttributesRequest {
            flow_id: flow_id.to_owned(),
            keys: vec![HARD_DELETE_ATTRIBUTE.to_owned()],
            ..GetAttributesRequest::default()
        };
        let mut client = direct_client(address);
        Self::spawn(async move { client.get_attributes(request).await.map(drop) })
    }

    fn spawn(call: impl Future<Output = Result<(), tonic::Status>> + Send + 'static) -> Self {
        let task = tokio::spawn(async move {
            let answer = call.await;
            (answer, Instant::now())
        });
        Self { task }
    }

    /// Waits for the answer; returns it and when it arrived.
    pub(super) async fn finish(self) -> (Result<(), tonic::Status>, Instant) {
        self.task.await.expect("a held call does not panic")
    }
}

/// Starts `flow_id` with a hard-delete Attribute through `channel`; no Worker ever runs it.
pub(super) async fn start_flow_with_hard_delete_attribute(channel: Channel, flow_id: &str) {
    let mut client = FlowServiceClient::new(channel);
    let request = StartFlowRequest {
        flow_id: flow_id.to_owned(),
        flow_type: HELD_CALL_FLOW_TYPE.to_owned(),
        start_step_type: HELD_CALL_STEP_TYPE.to_owned(),
        step_input: Some(string_value("held")),
        step_options: Some(StepOptions {
            skip_wait_for: true,
            ..StepOptions::default()
        }),
        flow_start_options: Some(FlowStartOptions {
            attributes: vec![AttributeWrite {
                key: HARD_DELETE_ATTRIBUTE.to_owned(),
                value: Some(string_value("classified")),
                is_hard_delete_on_null: true,
                ..AttributeWrite::default()
            }],
            flow_config_override: Some(FlowConfig {
                worker_target: Some(WorkerTarget { is_pull_mode: true }),
                ..FlowConfig::default()
            }),
            ..FlowStartOptions::default()
        }),
        request_id: format!("{flow_id}-start"),
        ..StartFlowRequest::default()
    };
    for attempt in 1..=START_ATTEMPTS {
        match client.start_flow(request.clone()).await {
            Ok(_) => return,
            Err(status) => assert!(
                attempt < START_ATTEMPTS,
                "{flow_id} was never started: {status}"
            ),
        }
        tokio::time::sleep(START_RETRY_INTERVAL).await;
    }
}

fn direct_client(address: SocketAddr) -> FlowServiceClient<Channel> {
    let channel = Channel::from_shared(format!("http://{address}"))
        .expect("the slot address is a valid URI")
        .connect_lazy();
    FlowServiceClient::new(channel)
}
