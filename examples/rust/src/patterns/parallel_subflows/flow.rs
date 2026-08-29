// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

use std::sync::{Arc, LazyLock};
use std::thread;
use std::time::Duration;

use dex_sdk::{
    Attribute, Channel, Client, Condition, ConditionCombination, Context, Flow, FlowStatus,
    HandlerError, HandlerResult, PersistenceSchema, Rpc, RpcList, RpcResult, Step, StepDecision,
    StepList, StepMovement, StepOptions, StopFlowOptions, SubFlow, SubFlowOptions, Wait,
};
use serde::{Deserialize, Serialize};

const DEFAULT_CONCURRENCY: usize = 10;
const MAX_BUFFERED_REQUESTS: usize = 100;

static REQUEST_CHANNEL: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("RequestChannel"));
static STOPPED: LazyLock<Attribute<bool>> = LazyLock::new(|| Attribute::new("Stopped"));
static CURR_SUB_FLOW_NUM: LazyLock<Attribute<usize>> =
    LazyLock::new(|| Attribute::new("CurrSubFlowNum"));

#[derive(Clone, Deserialize, Serialize)]
pub struct ParentInput {
    pub requests: Vec<String>,
    pub concurrency: usize,
}

#[derive(Clone, Deserialize, Serialize)]
pub struct SubmitRequestInput {
    pub request: String,
    pub parent_ids: Vec<String>,
}

#[derive(Default)]
pub struct ExampleSubFlow {
    do_work: DoWorkStep,
}

impl Flow for ExampleSubFlow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.do_work)
    }
}

#[derive(Default)]
struct DoWorkStep;

impl Step for DoWorkStep {
    type Input = String;

    fn execute(&self, _context: &mut Context, request: Self::Input) -> HandlerResult<StepDecision> {
        thread::sleep(Duration::from_millis(50 + (request.len() as u64 % 10) * 50));
        Ok(StepDecision::graceful_complete(request))
    }
}

#[derive(Default)]
pub struct BasicParentFlow {
    sub_flows: SubFlowsStep,
}

impl BasicParentFlow {
    pub fn new(client: Arc<Client>) -> Self {
        Self {
            sub_flows: SubFlowsStep {
                client: Some(client),
            },
        }
    }
}

impl Flow for BasicParentFlow {
    type StartInput = Vec<String>;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.sub_flows)
    }
}

#[derive(Default)]
struct SubFlowsStep {
    client: Option<Arc<Client>>,
}

impl Step for SubFlowsStep {
    type Input = Vec<String>;

    fn wait_for(&self, _context: &mut Context, requests: Self::Input) -> HandlerResult<Wait> {
        let child = ExampleSubFlow::default();
        let mut conditions = Vec::with_capacity(requests.len());
        for (index, request) in requests.into_iter().enumerate() {
            conditions.push(
                SubFlow::run_with_options(
                    &child,
                    request,
                    SubFlowOptions::new().condition_id(format!("subflow-{index}")),
                )
                .map_err(HandlerError::from_error)?,
            );
        }
        let winner_count = conditions.len().div_ceil(2);
        Ok(Wait::any_combination_of(condition_combinations(
            &conditions,
            winner_count,
        )))
    }

    fn execute(&self, context: &mut Context, requests: Self::Input) -> HandlerResult<StepDecision> {
        let client = self.client.as_ref().ok_or_else(|| {
            HandlerError::new("ParallelSubFlows", "Dex client is not initialized")
        })?;
        for index in 0..requests.len() {
            if SubFlow::condition_result_at(context, index)?.status() == FlowStatus::Running {
                let flow_id = SubFlow::flow_id_at(context, index)?;
                client
                    .stop_flow(
                        &flow_id,
                        StopFlowOptions::cancel().reason("enough SubFlows completed"),
                    )
                    .map_err(HandlerError::from_error)?;
            }
        }
        Ok(StepDecision::graceful_complete(()))
    }
}

fn condition_combinations(conditions: &[Condition], size: usize) -> Vec<ConditionCombination> {
    fn collect(
        conditions: &[Condition],
        size: usize,
        start: usize,
        selected: &mut Vec<Condition>,
        result: &mut Vec<ConditionCombination>,
    ) {
        if selected.len() == size {
            result.push(ConditionCombination::all_of(selected.clone()));
            return;
        }
        let remaining = size - selected.len();
        for index in start..=conditions.len() - remaining {
            selected.push(conditions[index].clone());
            collect(conditions, size, index + 1, selected, result);
            selected.pop();
        }
    }

    let mut result = Vec::new();
    collect(conditions, size, 0, &mut Vec::new(), &mut result);
    result
}

pub const LONG_LIVE_SEND_REQUEST: Rpc<String, bool> = Rpc::new("SendRequest");
pub const LONG_LIVE_STOP: Rpc<(), ()> = Rpc::new("Stop");

#[derive(Default)]
pub struct AdvancedLongLiveParentFlow {
    init: LongLiveInitStep,
    handle_request: LongLiveHandleRequestStep,
    handle_sub_flow: LongLiveHandleSubFlowStep,
}

impl AdvancedLongLiveParentFlow {
    fn send_request(
        &self,
        context: &mut Context,
        request: String,
    ) -> HandlerResult<RpcResult<bool>> {
        if REQUEST_CHANNEL.size(context)? >= MAX_BUFFERED_REQUESTS {
            return Ok(RpcResult::new(false));
        }
        REQUEST_CHANNEL.publish(context, request)?;
        Ok(RpcResult::new(true))
    }

    fn stop(&self, context: &mut Context) -> HandlerResult<RpcResult<()>> {
        STOPPED.set(context, true)?;
        Ok(RpcResult::new(()))
    }
}

impl Flow for AdvancedLongLiveParentFlow {
    type StartInput = ParentInput;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.init)
            .and(&self.handle_request)
            .and(&self.handle_sub_flow)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&STOPPED)
            .channel(&REQUEST_CHANNEL)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function(LONG_LIVE_SEND_REQUEST, Self::send_request)
            .function_without_input(LONG_LIVE_STOP, Self::stop)
    }
}

#[derive(Default)]
struct LongLiveInitStep;

impl Step for LongLiveInitStep {
    type Input = ParentInput;

    fn step_type(&self) -> &'static str {
        "InitStep"
    }

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        for request in input.requests {
            REQUEST_CHANNEL.publish(context, request)?;
        }
        STOPPED.set(context, false)?;
        let concurrency = if input.concurrency == 0 {
            DEFAULT_CONCURRENCY
        } else {
            input.concurrency
        };
        Ok(StepDecision::go_to_many(
            (0..concurrency).map(|_| StepMovement::to(&LongLiveHandleRequestStep, ())),
        ))
    }
}

#[derive(Default)]
struct LongLiveHandleRequestStep;

impl Step for LongLiveHandleRequestStep {
    type Input = ();

    fn step_type(&self) -> &'static str {
        "HandleRequestStep"
    }

    fn wait_for(&self, _context: &mut Context, _input: ()) -> HandlerResult<Wait> {
        Ok(Wait::until(REQUEST_CHANNEL.for_one()))
    }

    fn execute(&self, context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        let request = REQUEST_CHANNEL
            .condition_results(context)?
            .into_iter()
            .next()
            .unwrap_or_default();
        Ok(StepDecision::go_to(&LongLiveHandleSubFlowStep, request))
    }
}

#[derive(Default)]
struct LongLiveHandleSubFlowStep;

impl Step for LongLiveHandleSubFlowStep {
    type Input = String;

    fn step_type(&self) -> &'static str {
        "HandleSubFlowStep"
    }

    fn wait_for(&self, _context: &mut Context, request: String) -> HandlerResult<Wait> {
        let child = ExampleSubFlow::default();
        Ok(Wait::until(
            SubFlow::run(&child, request).map_err(HandlerError::from_error)?,
        ))
    }

    fn execute(&self, context: &mut Context, _request: String) -> HandlerResult<StepDecision> {
        if STOPPED.get(context)?.unwrap_or(false) {
            return Ok(StepDecision::graceful_complete(()));
        }
        Ok(StepDecision::go_to(&LongLiveHandleRequestStep, ()))
    }
}

pub const SHORT_LIVE_SEND_REQUEST: Rpc<String, bool> = Rpc::new("SendRequest");

#[derive(Default)]
pub struct AdvancedShortLiveParentFlow {
    init: ShortLiveInitStep,
    handle_request: ShortLiveHandleRequestStep,
    handle_sub_flow: ShortLiveHandleSubFlowStep,
}

impl AdvancedShortLiveParentFlow {
    fn send_request(
        &self,
        context: &mut Context,
        request: String,
    ) -> HandlerResult<RpcResult<bool>> {
        if REQUEST_CHANNEL.size(context)? >= MAX_BUFFERED_REQUESTS {
            return Ok(RpcResult::new(false));
        }
        REQUEST_CHANNEL.publish(context, request)?;
        Ok(RpcResult::new(true))
    }
}

impl Flow for AdvancedShortLiveParentFlow {
    type StartInput = ParentInput;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.init)
            .and(&self.handle_request)
            .and(&self.handle_sub_flow)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&CURR_SUB_FLOW_NUM)
            .channel(&REQUEST_CHANNEL)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().function(SHORT_LIVE_SEND_REQUEST, Self::send_request)
    }
}

#[derive(Default)]
struct ShortLiveInitStep;

impl Step for ShortLiveInitStep {
    type Input = ParentInput;

    fn step_type(&self) -> &'static str {
        "InitStep"
    }

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        for request in input.requests {
            REQUEST_CHANNEL.publish(context, request)?;
        }
        CURR_SUB_FLOW_NUM.set(context, 0)?;
        let concurrency = if input.concurrency == 0 {
            DEFAULT_CONCURRENCY
        } else {
            input.concurrency
        };
        Ok(StepDecision::go_to_many((0..concurrency).map(|_| {
            StepMovement::to(&ShortLiveHandleRequestStep, ())
        })))
    }
}

#[derive(Default)]
struct ShortLiveHandleRequestStep;

impl Step for ShortLiveHandleRequestStep {
    type Input = ();

    fn step_type(&self) -> &'static str {
        "HandleRequestStep"
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_lock(CURR_SUB_FLOW_NUM.lock())
    }

    fn wait_for(&self, _context: &mut Context, _input: ()) -> HandlerResult<Wait> {
        Ok(Wait::until(REQUEST_CHANNEL.for_one()))
    }

    fn execute(&self, context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        let request = REQUEST_CHANNEL
            .condition_results(context)?
            .into_iter()
            .next()
            .unwrap_or_default();
        let current = CURR_SUB_FLOW_NUM.get(context)?.unwrap_or(0);
        CURR_SUB_FLOW_NUM.set(context, current + 1)?;
        Ok(StepDecision::go_to(&ShortLiveHandleSubFlowStep, request))
    }
}

#[derive(Default)]
struct ShortLiveHandleSubFlowStep;

impl Step for ShortLiveHandleSubFlowStep {
    type Input = String;

    fn step_type(&self) -> &'static str {
        "HandleSubFlowStep"
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_lock(CURR_SUB_FLOW_NUM.lock())
    }

    fn wait_for(&self, _context: &mut Context, request: String) -> HandlerResult<Wait> {
        let child = ExampleSubFlow::default();
        Ok(Wait::until(
            SubFlow::run(&child, request).map_err(HandlerError::from_error)?,
        ))
    }

    fn execute(&self, context: &mut Context, _request: String) -> HandlerResult<StepDecision> {
        let current = CURR_SUB_FLOW_NUM.get(context)?.unwrap_or(1) - 1;
        CURR_SUB_FLOW_NUM.set(context, current)?;
        if current == 0 {
            return Ok(StepDecision::force_complete_if_channels_empty(
                (),
                StepMovement::to(&ShortLiveHandleRequestStep, ()),
                [REQUEST_CHANNEL.when_empty()],
            ));
        }
        Ok(StepDecision::go_to(&ShortLiveHandleRequestStep, ()))
    }
}

#[derive(Default)]
pub struct SubmitRequestFlow {
    submit: SubmitStep,
}

impl SubmitRequestFlow {
    pub fn new(client: Arc<Client>) -> Self {
        Self {
            submit: SubmitStep {
                client: Some(client),
            },
        }
    }
}

impl Flow for SubmitRequestFlow {
    type StartInput = SubmitRequestInput;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.submit)
    }
}

#[derive(Default)]
struct SubmitStep {
    client: Option<Arc<Client>>,
}

impl Step for SubmitStep {
    type Input = SubmitRequestInput;

    fn execute(&self, _context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        if input.parent_ids.is_empty() {
            return Err(HandlerError::new(
                "ParallelSubFlows",
                "at least one parent Flow ID is required",
            ));
        }
        let parent_id = &input.parent_ids[partition(&input.request, input.parent_ids.len())];
        let client = self.client.as_ref().ok_or_else(|| {
            HandlerError::new("ParallelSubFlows", "Dex client is not initialized")
        })?;
        let accepted = client
            .invoke_rpc(parent_id, SHORT_LIVE_SEND_REQUEST, input.request)
            .map_err(HandlerError::from_error)?;
        if !accepted {
            return Err(HandlerError::new(
                "ParallelSubFlows",
                format!("parent {parent_id} rejected the request"),
            ));
        }
        Ok(StepDecision::graceful_complete(parent_id.clone()))
    }
}

fn partition(request: &str, partitions: usize) -> usize {
    let mut hash = 2_166_136_261_u32;
    for byte in request.bytes() {
        hash ^= u32::from(byte);
        hash = hash.wrapping_mul(16_777_619);
    }
    hash as usize % partitions
}
