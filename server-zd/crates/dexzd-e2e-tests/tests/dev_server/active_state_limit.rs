// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The run state limit counts only active state, so rewriting one large Attribute never fails a run.

use std::time::Duration;

use dexzd_protocol::dex::{
    AttributeWrite, FlowStatus, InvokeExecuteMethodResponse, StepDecision, StepMovement,
    StepOptions, Value, WaitForFlowRequest, value,
};

use crate::harness::{DevServer, PullWorker, force_complete, start_pull_flow};

const FLOW_TYPE: &str = "RewriteConversationFlow";
const STEP_TYPE: &str = "RewriteConversationStep";
const FLOW_ID: &str = "rewrite-conversation-1";
const REWRITE_STEPS: u32 = 200;
const CONVERSATION_BYTES: usize = 2 * 1024 * 1024;

fn string_value(text: &str) -> Value {
    Value {
        kind: Some(value::Kind::StringValue(text.to_owned())),
    }
}

fn step_options() -> StepOptions {
    StepOptions {
        skip_wait_for: true,
        ..StepOptions::default()
    }
}

/// Writes a distinct 2 MiB conversation, then moves to the next Step until the last one.
fn rewrite_conversation(step_input: Option<&Value>) -> InvokeExecuteMethodResponse {
    let step_index: u32 = match step_input.and_then(|input| input.kind.as_ref()) {
        Some(value::Kind::StringValue(text)) => text.parse().expect("the input is a Step index"),
        other => panic!("expected a Step index input, got {other:?}"),
    };
    let prefix = format!("{step_index:04}-");
    let conversation = prefix.clone() + &"c".repeat(CONVERSATION_BYTES - prefix.len());
    let step_decision = if step_index + 1 == REWRITE_STEPS {
        force_complete(string_value("rewrote every turn"))
    } else {
        StepDecision {
            next_steps: vec![StepMovement {
                step_type: STEP_TYPE.to_owned(),
                step_input: Some(string_value(&(step_index + 1).to_string())),
                step_options: Some(step_options()),
                ..StepMovement::default()
            }],
            ..StepDecision::default()
        }
    };
    InvokeExecuteMethodResponse {
        upsert_attributes: vec![AttributeWrite {
            key: "conversation".to_owned(),
            value: Some(string_value(&conversation)),
            ..AttributeWrite::default()
        }],
        step_decision: Some(step_decision),
        ..InvokeExecuteMethodResponse::default()
    }
}

/// 200 rewrites of a 2 MiB Attribute complete; only the last Step completes the Flow.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn rewriting_a_large_attribute_every_step_completes_at_default_limits() {
    let server = DevServer::start().await;
    let mut flow_client = server.flow_client();
    let worker = PullWorker::start(
        server.worker_client(),
        FLOW_TYPE,
        Box::new(|request| rewrite_conversation(request.step_input.as_ref())),
    )
    .await;
    let started = start_pull_flow(
        &mut flow_client,
        FLOW_ID,
        FLOW_TYPE,
        STEP_TYPE,
        string_value("0"),
        step_options(),
    )
    .await;

    let result = tokio::time::timeout(Duration::from_secs(600), async {
        loop {
            let result = flow_client
                .wait_for_flow(WaitForFlowRequest {
                    flow_id: FLOW_ID.to_owned(),
                    run_id: started.run_id.clone(),
                    wait_time_seconds: 20,
                    ..WaitForFlowRequest::default()
                })
                .await;
            match result {
                Ok(response) => return response.into_inner(),
                Err(status) if status.code() == tonic::Code::DeadlineExceeded => {}
                Err(status) => panic!("WaitForFlow failed: {status:?}"),
            }
        }
    })
    .await
    .expect("the run closes within 10 minutes");
    assert_eq!(
        result.flow_status,
        FlowStatus::Completed as i32,
        "{}",
        result.error_message
    );

    let shutdown_time = server.shutdown().await;
    assert!(shutdown_time < Duration::from_secs(10));
    worker.stop().await;
}
