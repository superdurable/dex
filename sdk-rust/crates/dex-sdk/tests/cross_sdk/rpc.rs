// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::time::Duration;

use dex_sdk::{
    Attribute, Channel, Context, Flow, HandlerError, HandlerResult, PersistenceSchema, Registry,
    Rpc, RpcList, RpcResult, SdkError, Step, StepDecision, StepList, Wait,
};

use crate::support::{DexDevTestEnvironment, flow_id};

#[derive(Debug, PartialEq, serde::Deserialize, serde::Serialize)]
struct RpcIncrementOutput {
    value: i32,
    size_before: usize,
    size_after: usize,
    status_found: bool,
}

struct RpcWorkflow {
    channel: Channel<i32>,
    status: Attribute<String>,
    start: RpcStep,
}

impl RpcWorkflow {
    const INCREMENT: Rpc<i32, RpcIncrementOutput> = Rpc::new("increment");
    const FAIL: Rpc<i32, i32> = Rpc::new("fail");

    fn new() -> Self {
        let channel = Channel::new("rpc-values");
        let status = Attribute::new("rpc-status");
        Self {
            start: RpcStep {
                channel: channel.clone(),
                status: status.clone(),
            },
            channel,
            status,
        }
    }

    fn increment(
        &self,
        context: &mut Context,
        input: i32,
    ) -> HandlerResult<RpcResult<RpcIncrementOutput>> {
        let status_found = self.status.get(context)?.is_some();
        let size_before = self.channel.size(context)?;
        self.status.set(context, "invoked".to_string())?;
        self.channel.publish(context, input + 1)?;
        Ok(RpcResult::new(RpcIncrementOutput {
            value: input + 1,
            size_before,
            size_after: self.channel.size(context)?,
            status_found,
        }))
    }

    fn fail(&self, _context: &mut Context, _input: i32) -> HandlerResult<RpcResult<i32>> {
        Err(HandlerError::new("planned RPC failure"))
    }
}

impl Flow for RpcWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&self.status)
            .channel(&self.channel)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function(Self::INCREMENT.lock(self.status.lock()), Self::increment)
            .function(Self::FAIL, Self::fail)
    }
}

struct RpcStep {
    channel: Channel<i32>,
    status: Attribute<String>,
}

impl Step for RpcStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, _input: i32) -> HandlerResult<Wait> {
        Ok(Wait::until(self.channel.for_one()))
    }

    fn execute(&self, context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        let values = self.channel.condition_results(context)?;
        if values != [input + 1] {
            return Err(HandlerError::new(format!(
                "unexpected RPC channel values {values:?}"
            )));
        }
        if self.status.get_required(context)? != "invoked" {
            return Err(HandlerError::new("RPC attribute write was not committed"));
        }
        Ok(StepDecision::graceful_complete(values[0] + 1))
    }
}

#[test]
#[ignore = "requires dexcli dev"]
fn locked_write_and_publication_are_committed_atomically() {
    let environment = DexDevTestEnvironment::start(Registry::new().register(RpcWorkflow::new()));
    let workflow = RpcWorkflow::new();
    let flow_id = flow_id("go-rpc");
    environment
        .client
        .start_flow(&workflow, &flow_id, 1)
        .expect("start Go RPC compatibility Flow");

    match environment
        .client
        .invoke_rpc(&flow_id, RpcWorkflow::FAIL, 1)
        .expect_err("planned RPC failure must be returned")
    {
        SdkError::WorkerInvocation {
            worker_error_detail,
            ..
        } => assert!(worker_error_detail.contains("planned RPC failure")),
        error => panic!("expected WorkerInvocation, got {error:?}"),
    }

    assert_eq!(
        RpcIncrementOutput {
            value: 2,
            size_before: 0,
            size_after: 1,
            status_found: false,
        },
        environment
            .client
            .invoke_rpc(&flow_id, RpcWorkflow::INCREMENT, 1)
            .expect("invoke locked increment RPC")
    );
    assert_eq!(
        3,
        environment
            .client
            .wait_for_flow_with_timeout::<i32>(&flow_id, Duration::from_secs(30))
            .expect("complete Go RPC compatibility Flow")
    );
}
