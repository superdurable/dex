// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use dex_sdk::{
    Attribute, AttributeIndex, AttributeMap, Channel, Client, Context, Flow, FlowStatus,
    HandlerError, HandlerResult, PersistenceSchema, Registry, Rpc, RpcList, RpcResult, SdkError,
    SdkResult, Step, StepDecision, StepList, StopFlowOptions, Wait,
};

use std::sync::Arc;
use std::time::{Duration, Instant};

use crate::support::{DexDevTestEnvironment, flow_id};

struct RpcWorkflow {
    internal: Channel<()>,
    data: Attribute<String>,
    keyword: Attribute<String>,
    integer: Attribute<i32>,
    map: AttributeMap<String>,
    first: RpcFirstStep,
    output: RpcOutputStep,
}

impl RpcWorkflow {
    const RPC_OUTPUT: i64 = 100;
    const HARDCODED_VALUE: &str = "random-string";
    const PUBLISH_WITHOUT_ATTRIBUTE_ACCESS: Rpc<(), ()> =
        Rpc::new("publish_without_attribute_access");
    const FUNCTION_ONE: Rpc<String, i64> = Rpc::new("function_one");
    const FUNCTION_ZERO: Rpc<(), i64> = Rpc::new("function_zero");
    const PROCEDURE_ONE: Rpc<String, ()> = Rpc::new("procedure_one");
    const PROCEDURE_ZERO: Rpc<(), ()> = Rpc::new("procedure_zero");
    const READ_ONLY: Rpc<String, i64> = Rpc::new("read_only");
    const SET_DATA: Rpc<Option<String>, ()> = Rpc::new("set_data");
    const GET_DATA: Rpc<(), Option<String>> = Rpc::new("get_data");
    const SET_KEYWORD: Rpc<Option<String>, ()> = Rpc::new("set_keyword");
    const GET_KEYWORD: Rpc<(), Option<String>> = Rpc::new("get_keyword");
    const LOCK_MAP: Rpc<String, String> = Rpc::new("lock_map");

    fn new() -> Self {
        let internal = Channel::new("rpc-internal");
        Self {
            first: RpcFirstStep {
                internal: internal.clone(),
            },
            output: RpcOutputStep,
            internal,
            data: Attribute::new("rpc-data"),
            keyword: Attribute::new("CustomKeywordField").indexed(AttributeIndex::keyword()),
            integer: Attribute::new("CustomIntField").indexed(AttributeIndex::int()),
            map: AttributeMap::new("rpc-map"),
        }
    }

    fn publish_without_attribute_access(&self, context: &mut Context) -> HandlerResult<()> {
        Self::require_context(context)?;
        self.internal.publish(context, ())
    }

    fn function_one(&self, context: &mut Context, input: String) -> HandlerResult<RpcResult<i64>> {
        Self::require_context(context)?;
        self.data.delete(context)?;
        self.data.set(context, input.clone())?;
        self.keyword.set(context, input)?;
        self.integer.set(context, Self::RPC_OUTPUT as i32)?;
        self.internal.publish(context, ())?;
        Ok(RpcResult::new(Self::RPC_OUTPUT))
    }

    fn function_zero(&self, context: &mut Context) -> HandlerResult<RpcResult<i64>> {
        Self::require_context(context)?;
        self.data.set(context, Self::HARDCODED_VALUE.into())?;
        self.keyword.set(context, Self::HARDCODED_VALUE.into())?;
        self.integer.set(context, Self::RPC_OUTPUT as i32)?;
        self.internal.publish(context, ())?;
        Ok(RpcResult::new(Self::RPC_OUTPUT))
    }

    fn procedure_one(&self, context: &mut Context, input: String) -> HandlerResult<()> {
        Self::require_context(context)?;
        self.data.set(context, input.clone())?;
        self.keyword.set(context, input)?;
        self.integer.set(context, Self::RPC_OUTPUT as i32)?;
        self.internal.publish(context, ())
    }

    fn procedure_zero(&self, context: &mut Context) -> HandlerResult<()> {
        Self::require_context(context)?;
        self.data.set(context, Self::HARDCODED_VALUE.into())?;
        self.keyword.set(context, Self::HARDCODED_VALUE.into())?;
        self.integer.set(context, Self::RPC_OUTPUT as i32)?;
        self.internal.publish(context, ())
    }

    fn read_only(&self, context: &mut Context, _input: String) -> HandlerResult<RpcResult<i64>> {
        Self::require_context(context)?;
        Ok(RpcResult::new(Self::RPC_OUTPUT))
    }

    fn set_data(&self, context: &mut Context, input: Option<String>) -> HandlerResult<()> {
        Self::set_optional(context, &self.data, input)
    }

    fn get_data(&self, context: &mut Context) -> HandlerResult<RpcResult<Option<String>>> {
        Ok(RpcResult::new(self.data.get(context)?))
    }

    fn set_keyword(&self, context: &mut Context, input: Option<String>) -> HandlerResult<()> {
        Self::set_optional(context, &self.keyword, input)
    }

    fn get_keyword(&self, context: &mut Context) -> HandlerResult<RpcResult<Option<String>>> {
        Ok(RpcResult::new(self.keyword.get(context)?))
    }

    fn lock_map(&self, context: &mut Context, input: String) -> HandlerResult<RpcResult<String>> {
        self.map.set(context, "one", input.clone())?;
        Ok(RpcResult::new(input))
    }

    fn set_optional(
        context: &mut Context,
        attribute: &Attribute<String>,
        value: Option<String>,
    ) -> HandlerResult<()> {
        match value {
            Some(value) => attribute.set(context, value),
            None => attribute.delete(context),
        }
    }

    fn require_context(context: &Context) -> HandlerResult<()> {
        if context.flow_id().is_empty() || context.run_id().is_empty() {
            return Err(HandlerError::new("invalid RPC context"));
        }
        Ok(())
    }
}

impl Flow for RpcWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.first).and(&self.output)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&self.data)
            .attribute(&self.keyword)
            .attribute(&self.integer)
            .attribute_map(&self.map)
            .channel(&self.internal)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .procedure_without_input(
                Self::PUBLISH_WITHOUT_ATTRIBUTE_ACCESS,
                Self::publish_without_attribute_access,
            )
            .function(Self::FUNCTION_ONE, Self::function_one)
            .function_without_input(Self::FUNCTION_ZERO, Self::function_zero)
            .procedure(Self::PROCEDURE_ONE, Self::procedure_one)
            .procedure_without_input(Self::PROCEDURE_ZERO, Self::procedure_zero)
            .function(Self::READ_ONLY, Self::read_only)
            .procedure(Self::SET_DATA, Self::set_data)
            .function_without_input(Self::GET_DATA, Self::get_data)
            .procedure(Self::SET_KEYWORD, Self::set_keyword)
            .function_without_input(Self::GET_KEYWORD, Self::get_keyword)
            .function(Self::LOCK_MAP.lock(self.map.lock("one")), Self::lock_map)
    }
}

struct RpcFirstStep {
    internal: Channel<()>,
}

impl Step for RpcFirstStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, _input: i32) -> HandlerResult<Wait> {
        Ok(Wait::until(self.internal.for_one()))
    }

    fn execute(&self, _context: &mut Context, _input: i32) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(&RpcOutputStep, 0))
    }
}

struct RpcOutputStep;

impl Step for RpcOutputStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        let _ = input;
        Ok(StepDecision::graceful_complete(2_i32))
    }
}

struct RpcNoStateWorkflow {
    counter: Attribute<i32>,
}

impl RpcNoStateWorkflow {
    const INCREASE_COUNTER: Rpc<(), i32> = Rpc::new("increase_counter");
    const GET_COUNTER: Rpc<(), Option<i32>> = Rpc::new("get_counter");
    const FAIL: Rpc<String, i64> = Rpc::new("fail");
    const INVOKE: Rpc<String, i64> = Rpc::new("invoke");

    fn increase_counter(&self, context: &mut Context) -> HandlerResult<RpcResult<i32>> {
        let next = self.counter.get(context)?.unwrap_or_default() + 1;
        self.counter.set(context, next)?;
        Ok(RpcResult::new(next))
    }

    fn get_counter(&self, context: &mut Context) -> HandlerResult<RpcResult<Option<i32>>> {
        Ok(RpcResult::new(self.counter.get(context)?))
    }

    fn fail(&self, _context: &mut Context, input: String) -> HandlerResult<RpcResult<i64>> {
        Err(HandlerError::new(input))
    }

    fn invoke(&self, context: &mut Context, _input: String) -> HandlerResult<RpcResult<i64>> {
        if context.flow_id().is_empty() || context.run_id().is_empty() {
            return Err(HandlerError::new("invalid RPC context"));
        }
        Ok(RpcResult::new(RpcWorkflow::RPC_OUTPUT))
    }
}

impl Flow for RpcNoStateWorkflow {
    type StartInput = ();

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().attribute(&self.counter)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function_without_input(
                Self::INCREASE_COUNTER.lock(self.counter.lock()),
                Self::increase_counter,
            )
            .function_without_input(Self::GET_COUNTER, Self::get_counter)
            .function(Self::FAIL, Self::fail)
            .function(Self::INVOKE, Self::invoke)
    }
}

#[derive(Debug, PartialEq, serde::Deserialize, serde::Serialize)]
struct GoRpcIncrementOutput {
    value: i32,
    size_before: usize,
    size_after: usize,
    status_found: bool,
}

struct GoRpcWorkflow {
    channel: Channel<i32>,
    status: Attribute<String>,
    start: GoRpcStep,
}

impl GoRpcWorkflow {
    const INCREMENT: Rpc<i32, GoRpcIncrementOutput> = Rpc::new("increment");
    const FAIL: Rpc<i32, i32> = Rpc::new("fail");

    fn new() -> Self {
        let channel = Channel::new("rpc-values");
        let status = Attribute::new("rpc-status");
        Self {
            start: GoRpcStep {
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
    ) -> HandlerResult<RpcResult<GoRpcIncrementOutput>> {
        let status_found = self.status.get(context)?.is_some();
        let size_before = self.channel.size(context)?;
        self.status.set(context, "invoked".to_string())?;
        self.channel.publish(context, input + 1)?;
        Ok(RpcResult::new(GoRpcIncrementOutput {
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

impl Flow for GoRpcWorkflow {
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

struct GoRpcStep {
    channel: Channel<i32>,
    status: Attribute<String>,
}

impl Step for GoRpcStep {
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

fn compile_rpc_test(client: &Client) -> SdkResult<()> {
    let workflow = RpcWorkflow::new();
    client.start_flow(&workflow, "rpc", 0)?;
    client.invoke_rpc_without_input("rpc", RpcWorkflow::PUBLISH_WITHOUT_ATTRIBUTE_ACCESS)?;
    let output: i64 = client.invoke_rpc("rpc", RpcWorkflow::FUNCTION_ONE, "input".into())?;
    assert_eq!(RpcWorkflow::RPC_OUTPUT, output);
    let output: i64 = client.invoke_rpc_without_input("rpc", RpcWorkflow::FUNCTION_ZERO)?;
    assert_eq!(RpcWorkflow::RPC_OUTPUT, output);
    client.invoke_rpc("rpc", RpcWorkflow::PROCEDURE_ONE, "value".into())?;
    client.invoke_rpc_without_input("rpc", RpcWorkflow::PROCEDURE_ZERO)?;
    let read_only: i64 = client.invoke_rpc("rpc", RpcWorkflow::READ_ONLY, "input".into())?;
    assert_eq!(RpcWorkflow::RPC_OUTPUT, read_only);
    client.invoke_rpc("rpc", RpcWorkflow::SET_DATA, Some("data".into()))?;
    let data: Option<String> = client.invoke_rpc_without_input("rpc", RpcWorkflow::GET_DATA)?;
    assert_eq!(Some("data".into()), data);
    client.invoke_rpc("rpc", RpcWorkflow::SET_KEYWORD, Some("keyword".into()))?;
    let keyword: Option<String> =
        client.invoke_rpc_without_input("rpc", RpcWorkflow::GET_KEYWORD)?;
    assert_eq!(Some("keyword".into()), keyword);
    let _: String = client.invoke_rpc("rpc", RpcWorkflow::LOCK_MAP, "map".into())?;
    let completed: i32 = client.wait_for_flow("rpc")?;
    assert_eq!(2, completed);
    Ok(())
}

fn compile_rpc_with_memo_test(client: &Client) -> SdkResult<()> {
    let workflow = RpcWorkflow::new();
    client.start_flow(&workflow, "rpc-memo", 999)?;
    client.invoke_rpc("rpc-memo", RpcWorkflow::SET_DATA, Some("value".into()))?;
    let data: Option<String> =
        client.invoke_rpc_without_input("rpc-memo", RpcWorkflow::GET_DATA)?;
    assert_eq!(Some("value".into()), data);
    client.invoke_rpc("rpc-memo", RpcWorkflow::SET_DATA, None)?;
    let data: Option<String> =
        client.invoke_rpc_without_input("rpc-memo", RpcWorkflow::GET_DATA)?;
    assert_eq!(None, data);
    client.invoke_rpc("rpc-memo", RpcWorkflow::SET_KEYWORD, Some("keyword".into()))?;
    let keyword: Option<String> =
        client.invoke_rpc_without_input("rpc-memo", RpcWorkflow::GET_KEYWORD)?;
    assert_eq!(Some("keyword".into()), keyword);
    Ok(())
}

fn compile_rpc_without_steps_test(client: &Client) -> SdkResult<()> {
    let _workflow = RpcNoStateWorkflow {
        counter: Attribute::new("counter"),
    };
    let next: i32 =
        client.invoke_rpc_without_input("rpc-no-state", RpcNoStateWorkflow::INCREASE_COUNTER)?;
    let current: Option<i32> =
        client.invoke_rpc_without_input("rpc-no-state", RpcNoStateWorkflow::GET_COUNTER)?;
    assert_eq!(Some(next), current);
    let _: i64 = client.invoke_rpc(
        "rpc-no-state",
        RpcNoStateWorkflow::FAIL,
        "this is an error".into(),
    )?;
    let invoked: i64 =
        client.invoke_rpc("rpc-no-state", RpcNoStateWorkflow::INVOKE, "input".into())?;
    assert_eq!(RpcWorkflow::RPC_OUTPUT, invoked);
    Ok(())
}

#[test]
#[ignore = "requires dexcli dev"]
fn locking_rpc_serializes_successful_updates() {
    let environment = Arc::new(DexDevTestEnvironment::start(Registry::new().register(
        RpcNoStateWorkflow {
            counter: Attribute::new("counter"),
        },
    )));
    let workflow = RpcNoStateWorkflow {
        counter: Attribute::new("counter"),
    };
    let flow_id = flow_id("rpc-lock");
    environment
        .client
        .start_flow(&workflow, &flow_id, ())
        .expect("start RPC locking Flow");

    let mut threads = Vec::new();
    for _ in 0..100 {
        let environment = Arc::clone(&environment);
        let flow_id = flow_id.clone();
        threads.push(std::thread::spawn(move || {
            match environment
                .client
                .invoke_rpc_without_input::<i32>(&flow_id, RpcNoStateWorkflow::INCREASE_COUNTER)
            {
                Ok(_) => true,
                Err(SdkError::RpcLockConflict { .. }) => false,
                Err(error) => panic!("increase counter RPC failed: {error:?}"),
            }
        }));
    }
    let succeeded = threads
        .into_iter()
        .map(|thread| thread.join().expect("join RPC invocation thread"))
        .filter(|succeeded| *succeeded)
        .count();
    assert!(succeeded > 0);
    assert_eq!(
        Some(succeeded as i32),
        environment
            .client
            .invoke_rpc_without_input::<Option<i32>>(&flow_id, RpcNoStateWorkflow::GET_COUNTER,)
            .expect("read locked counter")
    );
    environment
        .client
        .stop_flow(&flow_id, StopFlowOptions::cancel())
        .expect("stop RPC locking Flow");
}

#[test]
#[ignore = "requires dexcli dev"]
fn rpc_procedure_without_attribute_access_completes_flow() {
    let (environment, workflow, flow_id) = start_rpc_workflow("rpc-no-attributes");
    environment
        .client
        .invoke_rpc_without_input(&flow_id, RpcWorkflow::PUBLISH_WITHOUT_ATTRIBUTE_ACCESS)
        .expect("publish from RPC without Attribute access");
    assert_eq!(
        2,
        environment
            .client
            .wait_for_flow_with_timeout::<i32>(&flow_id, Duration::from_secs(30))
            .expect("complete RPC Flow")
    );
    drop(workflow);
}

#[test]
#[ignore = "requires dexcli dev"]
fn rpc_function_with_input_updates_attributes_and_completes_flow() {
    run_rpc_function_with_input("rpc-func-1");
}

#[test]
#[ignore = "requires dexcli dev"]
fn rpc_function_without_input_updates_attributes_and_completes_flow() {
    run_rpc_function_without_input("rpc-func-0");
}

#[test]
#[ignore = "requires dexcli dev"]
fn rpc_procedure_with_input_updates_attributes_and_completes_flow() {
    run_rpc_procedure_with_input("rpc-proc-1");
}

#[test]
#[ignore = "requires dexcli dev"]
fn rpc_procedure_without_input_updates_attributes_and_completes_flow() {
    run_rpc_procedure_without_input("rpc-proc-0");
}

#[test]
#[ignore = "requires dexcli dev"]
fn rpc_read_only_returns_output_without_completing_flow() {
    run_rpc_read_only("rpc-read-only");
}

#[test]
#[ignore = "requires dexcli dev"]
fn rpc_user_error_preserves_worker_details() {
    let environment = DexDevTestEnvironment::start(Registry::new().register(RpcNoStateWorkflow {
        counter: Attribute::new("counter"),
    }));
    let workflow = RpcNoStateWorkflow {
        counter: Attribute::new("counter"),
    };
    let flow_id = flow_id("rpc-error");
    environment
        .client
        .start_flow(&workflow, &flow_id, ())
        .expect("start RPC error Flow");
    let error = environment
        .client
        .invoke_rpc::<String, i64>(
            &flow_id,
            RpcNoStateWorkflow::FAIL,
            "this is an error".to_string(),
        )
        .expect_err("RPC must return the user error");
    match error {
        SdkError::WorkerInvocation {
            code,
            worker_error_type,
            worker_error_detail,
            ..
        } => {
            assert_eq!(dex_sdk::GrpcCode::FailedPrecondition, code);
            assert!(worker_error_type.contains("HandlerError"));
            assert!(worker_error_detail.contains("this is an error"));
        }
        other => panic!("expected WorkerInvocation, got {other:?}"),
    }
    environment
        .client
        .stop_flow(&flow_id, StopFlowOptions::cancel())
        .expect("stop RPC error Flow");
}

#[test]
#[ignore = "requires dexcli dev"]
fn rpc_locked_write_and_publication_are_committed_atomically() {
    let environment = DexDevTestEnvironment::start(Registry::new().register(GoRpcWorkflow::new()));
    let workflow = GoRpcWorkflow::new();
    let flow_id = flow_id("go-rpc");
    environment
        .client
        .start_flow(&workflow, &flow_id, 1)
        .expect("start Go RPC compatibility Flow");

    let failure = environment
        .client
        .invoke_rpc(&flow_id, GoRpcWorkflow::FAIL, 1)
        .expect_err("planned RPC failure must be returned");
    match failure {
        SdkError::WorkerInvocation {
            worker_error_detail,
            ..
        } => assert!(worker_error_detail.contains("planned RPC failure")),
        other => panic!("expected WorkerInvocation, got {other:?}"),
    }

    assert_eq!(
        GoRpcIncrementOutput {
            value: 2,
            size_before: 0,
            size_after: 1,
            status_found: false,
        },
        environment
            .client
            .invoke_rpc(&flow_id, GoRpcWorkflow::INCREMENT, 1)
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

#[test]
#[ignore = "requires dexcli dev"]
fn no_step_flow_serves_rpc_until_stopped() {
    let environment = DexDevTestEnvironment::start(Registry::new().register(RpcNoStateWorkflow {
        counter: Attribute::new("counter"),
    }));
    let workflow = RpcNoStateWorkflow {
        counter: Attribute::new("counter"),
    };
    let flow_id = flow_id("no-state");
    environment
        .client
        .start_flow(&workflow, &flow_id, ())
        .expect("start no-Step Flow");
    let query = format!("FlowType = '{}'", workflow.flow_type());
    let deadline = Instant::now() + Duration::from_secs(20);
    loop {
        if environment
            .client
            .search_flows_page(&query, 100, "")
            .is_ok_and(|page| {
                page.flows
                    .into_iter()
                    .any(|entry| entry.flow_id == flow_id && entry.status == FlowStatus::Running)
            })
        {
            break;
        }
        assert!(
            Instant::now() < deadline,
            "no-Step Flow was not searchable while running"
        );
        std::thread::yield_now();
    }
    assert_eq!(
        RpcWorkflow::RPC_OUTPUT,
        environment
            .client
            .invoke_rpc(
                &flow_id,
                RpcNoStateWorkflow::INVOKE,
                "rpc-input".to_string(),
            )
            .expect("invoke no-Step Flow RPC")
    );
    let failure = environment
        .client
        .invoke_rpc(
            &flow_id,
            RpcNoStateWorkflow::FAIL,
            "planned no-step RPC failure".to_string(),
        )
        .expect_err("planned no-Step RPC failure must be returned");
    match failure {
        SdkError::WorkerInvocation {
            worker_error_detail,
            ..
        } => assert!(worker_error_detail.contains("planned no-step RPC failure")),
        other => panic!("expected WorkerInvocation, got {other:?}"),
    }
    environment
        .client
        .stop_flow(&flow_id, StopFlowOptions::fail().reason("test"))
        .expect("fail no-Step Flow");
    match environment
        .client
        .wait_for_flow_with_timeout::<i32>(&flow_id, Duration::from_secs(30))
        .expect_err("failed no-Step Flow must not complete")
    {
        SdkError::FlowUncompleted { status, .. } => assert_eq!(FlowStatus::Failed, status),
        other => panic!("expected FlowUncompleted, got {other:?}"),
    }
}

#[test]
#[ignore = "requires dexcli dev"]
fn memo_rpc_function_with_input_replaces_and_deletes_attributes() {
    run_rpc_function_with_input("rpc-attribute-func-1");
}

#[test]
#[ignore = "requires dexcli dev"]
fn memo_rpc_function_without_input_updates_attributes() {
    run_rpc_function_without_input("rpc-attribute-func-0");
}

#[test]
#[ignore = "requires dexcli dev"]
fn memo_rpc_procedure_with_input_updates_attributes() {
    run_rpc_procedure_with_input("rpc-attribute-proc-1");
}

#[test]
#[ignore = "requires dexcli dev"]
fn memo_rpc_procedure_without_input_updates_attributes() {
    run_rpc_procedure_without_input("rpc-attribute-proc-0");
}

#[test]
#[ignore = "requires dexcli dev"]
fn memo_rpc_read_only_returns_output_without_writes() {
    run_rpc_read_only("rpc-attribute-read-only");
}

fn start_rpc_workflow(prefix: &str) -> (DexDevTestEnvironment, RpcWorkflow, String) {
    let environment = DexDevTestEnvironment::start(Registry::new().register(RpcWorkflow::new()));
    let workflow = RpcWorkflow::new();
    let flow_id = flow_id(prefix);
    environment
        .client
        .start_flow(&workflow, &flow_id, 999)
        .expect("start RPC Flow");
    (environment, workflow, flow_id)
}

fn run_rpc_function_with_input(prefix: &str) {
    let (environment, workflow, flow_id) = start_rpc_workflow(prefix);
    verify_optional_rpc_attributes(&environment.client, &flow_id);
    assert_eq!(
        RpcWorkflow::RPC_OUTPUT,
        environment
            .client
            .invoke_rpc(&flow_id, RpcWorkflow::FUNCTION_ONE, "rpc-input".to_string(),)
            .expect("invoke RPC function with input")
    );
    assert_rpc_completion(&environment, &workflow, &flow_id, "rpc-input");
}

fn run_rpc_function_without_input(prefix: &str) {
    let (environment, workflow, flow_id) = start_rpc_workflow(prefix);
    assert_eq!(
        RpcWorkflow::RPC_OUTPUT,
        environment
            .client
            .invoke_rpc_without_input(&flow_id, RpcWorkflow::FUNCTION_ZERO)
            .expect("invoke RPC function without input")
    );
    assert_rpc_completion(
        &environment,
        &workflow,
        &flow_id,
        RpcWorkflow::HARDCODED_VALUE,
    );
}

fn run_rpc_procedure_with_input(prefix: &str) {
    let (environment, workflow, flow_id) = start_rpc_workflow(prefix);
    environment
        .client
        .invoke_rpc(
            &flow_id,
            RpcWorkflow::PROCEDURE_ONE,
            "rpc-input".to_string(),
        )
        .expect("invoke RPC procedure with input");
    assert_rpc_completion(&environment, &workflow, &flow_id, "rpc-input");
}

fn run_rpc_procedure_without_input(prefix: &str) {
    let (environment, workflow, flow_id) = start_rpc_workflow(prefix);
    environment
        .client
        .invoke_rpc_without_input(&flow_id, RpcWorkflow::PROCEDURE_ZERO)
        .expect("invoke RPC procedure without input");
    assert_rpc_completion(
        &environment,
        &workflow,
        &flow_id,
        RpcWorkflow::HARDCODED_VALUE,
    );
}

fn run_rpc_read_only(prefix: &str) {
    let (environment, _workflow, flow_id) = start_rpc_workflow(prefix);
    assert_eq!(
        RpcWorkflow::RPC_OUTPUT,
        environment
            .client
            .invoke_rpc(&flow_id, RpcWorkflow::READ_ONLY, "rpc-input".to_string(),)
            .expect("invoke read-only RPC")
    );
    environment
        .client
        .stop_flow(
            &flow_id,
            StopFlowOptions::fail().reason(RpcWorkflow::HARDCODED_VALUE),
        )
        .expect("fail read-only RPC Flow");
}

fn verify_optional_rpc_attributes(client: &Client, flow_id: &str) {
    client
        .invoke_rpc(
            flow_id,
            RpcWorkflow::SET_DATA,
            Some("test-value".to_string()),
        )
        .expect("set data Attribute");
    assert_eq!(
        Some("test-value".to_string()),
        client
            .invoke_rpc_without_input(flow_id, RpcWorkflow::GET_DATA)
            .expect("get data Attribute")
    );
    client
        .invoke_rpc(flow_id, RpcWorkflow::SET_DATA, None)
        .expect("delete data Attribute");
    assert_eq!(
        None,
        client
            .invoke_rpc_without_input::<Option<String>>(flow_id, RpcWorkflow::GET_DATA)
            .expect("get deleted data Attribute")
    );
    client
        .invoke_rpc(
            flow_id,
            RpcWorkflow::SET_KEYWORD,
            Some("test-value".to_string()),
        )
        .expect("set indexed Attribute");
    assert_eq!(
        Some("test-value".to_string()),
        client
            .invoke_rpc_without_input(flow_id, RpcWorkflow::GET_KEYWORD)
            .expect("get indexed Attribute")
    );
    client
        .invoke_rpc(flow_id, RpcWorkflow::SET_KEYWORD, None)
        .expect("delete indexed Attribute");
    assert_eq!(
        None,
        client
            .invoke_rpc_without_input::<Option<String>>(flow_id, RpcWorkflow::GET_KEYWORD)
            .expect("get deleted indexed Attribute")
    );
}

fn assert_rpc_completion(
    environment: &DexDevTestEnvironment,
    workflow: &RpcWorkflow,
    flow_id: &str,
    expected_value: &str,
) {
    assert_eq!(
        2,
        environment
            .client
            .wait_for_flow_with_timeout::<i32>(flow_id, Duration::from_secs(30))
            .expect("complete RPC Flow")
    );
    assert_eq!(
        Some(expected_value.to_string()),
        environment
            .client
            .get_attribute(flow_id, &workflow.data)
            .expect("read RPC data Attribute")
    );
    assert_eq!(
        Some(expected_value.to_string()),
        environment
            .client
            .get_attribute(flow_id, &workflow.keyword)
            .expect("read RPC indexed Attribute")
    );
    assert_eq!(
        Some(RpcWorkflow::RPC_OUTPUT as i32),
        environment
            .client
            .get_attribute(flow_id, &workflow.integer)
            .expect("read RPC integer Attribute")
    );
}
