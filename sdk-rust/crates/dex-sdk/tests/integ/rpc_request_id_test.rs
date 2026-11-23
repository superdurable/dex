// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, LazyLock};
use std::time::Duration;

use dex_sdk::{
    Attribute, Context, Flow, GrpcCode, HandlerResult, PersistenceSchema, Registry, Rpc,
    RpcInvokeOptions, RpcList, RpcResult, SdkError, StopFlowOptions,
};

use crate::support::{DexDevTestEnvironment, flow_id};

const TRANSACTIONAL_ADD: Rpc<i32, i32> = Rpc::new("request_id_transactional_add");
const NON_TRANSACTIONAL_ADD: Rpc<i32, i32> = Rpc::new("request_id_non_transactional_add");
const READ_TOTAL: Rpc<(), i32> = Rpc::new("read_request_id_total");
const TRANSACTIONAL_MARK: Rpc<(), bool> = Rpc::new("request_id_transactional_mark");
const NON_TRANSACTIONAL_MARK: Rpc<(), bool> = Rpc::new("request_id_non_transactional_mark");

static TOTAL: LazyLock<Attribute<i32>> = LazyLock::new(|| Attribute::new("request-id-total"));
static PROCESSED: LazyLock<Attribute<bool>> =
    LazyLock::new(|| Attribute::new("request-id-processed"));

#[test]
#[ignore = "requires dex-server-zd"]
fn transactional_repeat_replays_without_dispatch() {
    let fixture = RpcRequestIdFixture::start();
    let options = RpcInvokeOptions::new().request_id("counter-event");

    assert_eq!(5, fixture.add(TRANSACTIONAL_ADD, 5, options.clone()));
    assert_eq!(5, fixture.add(TRANSACTIONAL_ADD, 5, options));
    assert_eq!(5, fixture.total());
    assert_eq!(1, fixture.handler_calls());
    fixture.close();
}

#[test]
#[ignore = "requires dex-server-zd"]
fn repeat_with_different_input_replays_recorded_reply() {
    let fixture = RpcRequestIdFixture::start();
    let options = RpcInvokeOptions::new().request_id("counter-event");

    assert_eq!(5, fixture.add(TRANSACTIONAL_ADD, 5, options.clone()));
    assert_eq!(5, fixture.add(TRANSACTIONAL_ADD, 7, options));
    assert_eq!(5, fixture.total());
    assert_eq!(1, fixture.handler_calls());
    fixture.close();
}

#[test]
#[ignore = "requires dex-server-zd"]
fn omitted_request_id_applies_every_call() {
    let fixture = RpcRequestIdFixture::start();
    let options = RpcInvokeOptions::new();

    assert_eq!(3, fixture.add(TRANSACTIONAL_ADD, 3, options.clone()));
    assert_eq!(6, fixture.add(TRANSACTIONAL_ADD, 3, options));
    assert_eq!(
        9,
        fixture
            .environment
            .client
            .invoke_rpc(&fixture.flow_id, TRANSACTIONAL_ADD, 3)
            .expect("invoke without options")
    );
    assert_eq!(9, fixture.total());
    assert_eq!(3, fixture.handler_calls());
    fixture.close();
}

#[test]
#[ignore = "requires dex-server-zd"]
fn empty_request_id_returns_invalid_argument_before_dispatch() {
    let fixture = RpcRequestIdFixture::start();

    let error = fixture
        .environment
        .client
        .invoke_rpc_with_options(
            &fixture.flow_id,
            TRANSACTIONAL_ADD,
            3,
            RpcInvokeOptions::new().request_id(""),
        )
        .expect_err("reject the empty request ID");
    assert_eq!(
        GrpcCode::InvalidArgument,
        error.service_error().expect("server rejection").code()
    );
    assert_eq!(0, fixture.total());
    assert_eq!(0, fixture.handler_calls());
    fixture.close();
}

#[test]
#[ignore = "requires dex-server-zd"]
fn non_transactional_repeat_dispatches_twice_and_applies_once() {
    let fixture = RpcRequestIdFixture::start();
    let options = RpcInvokeOptions::new().request_id("counter-event");

    assert_eq!(4, fixture.add(NON_TRANSACTIONAL_ADD, 4, options.clone()));
    assert_eq!(4, fixture.add(NON_TRANSACTIONAL_ADD, 4, options));
    assert_eq!(4, fixture.total());
    assert_eq!(2, fixture.handler_calls());
    fixture.close();
}

#[test]
#[ignore = "requires dex-server-zd"]
fn zero_effect_repeat_replays_only_for_transactional_rpc() {
    let non_transactional = RpcRequestIdFixture::start();
    assert!(non_transactional.mark_processed(NON_TRANSACTIONAL_MARK));
    assert!(!non_transactional.mark_processed(NON_TRANSACTIONAL_MARK));
    assert_eq!(2, non_transactional.handler_calls());
    non_transactional.close();
    drop(non_transactional);

    let transactional = RpcRequestIdFixture::start();
    assert!(transactional.mark_processed(TRANSACTIONAL_MARK));
    assert!(transactional.mark_processed(TRANSACTIONAL_MARK));
    assert_eq!(1, transactional.handler_calls());
    transactional.close();
}

#[test]
#[ignore = "requires dex-server-zd"]
fn transactional_repeat_replays_after_flow_closes() {
    let fixture = RpcRequestIdFixture::start();
    let options = RpcInvokeOptions::new().request_id("counter-event");

    assert_eq!(7, fixture.add(TRANSACTIONAL_ADD, 7, options.clone()));
    fixture.close();
    assert_eq!(7, fixture.add(TRANSACTIONAL_ADD, 7, options));
    assert!(matches!(
        fixture.environment.client.invoke_rpc_with_options(
            &fixture.flow_id,
            TRANSACTIONAL_ADD,
            7,
            RpcInvokeOptions::new().request_id("new-counter-event"),
        ),
        Err(SdkError::FlowNotActiveOrNotFound { .. })
    ));
    assert_eq!(1, fixture.handler_calls());
}

struct RpcRequestIdFixture {
    environment: DexDevTestEnvironment,
    flow_id: String,
    handler_calls: Arc<AtomicUsize>,
}

impl RpcRequestIdFixture {
    fn start() -> Self {
        let handler_calls = Arc::new(AtomicUsize::new(0));
        let workflow = RpcRequestIdWorkflow {
            handler_calls: Arc::clone(&handler_calls),
        };
        let environment = DexDevTestEnvironment::start(Registry::new().register(workflow.clone()));
        let flow_id = flow_id("rpc-request-id");
        environment
            .client
            .start_flow(&workflow, &flow_id, ())
            .expect("start request ID counter Flow");
        Self {
            environment,
            flow_id,
            handler_calls,
        }
    }

    fn add(&self, rpc: Rpc<i32, i32>, delta: i32, options: RpcInvokeOptions) -> i32 {
        self.environment
            .client
            .invoke_rpc_with_options(&self.flow_id, rpc, delta, options)
            .expect("increment counter")
    }

    fn total(&self) -> i32 {
        self.environment
            .client
            .invoke_rpc_without_input(&self.flow_id, READ_TOTAL)
            .expect("read counter total")
    }

    fn handler_calls(&self) -> usize {
        self.handler_calls.load(Ordering::Relaxed)
    }

    fn mark_processed(&self, rpc: Rpc<(), bool>) -> bool {
        self.environment
            .client
            .invoke_rpc_without_input_with_options(
                &self.flow_id,
                rpc,
                RpcInvokeOptions::new().request_id("mark-event"),
            )
            .expect("mark event processed")
    }

    fn close(&self) {
        self.environment
            .client
            .stop_flow(&self.flow_id, StopFlowOptions::cancel())
            .expect("stop request ID counter Flow");
        self.environment
            .client
            .wait_for_flow_with_timeout(&self.flow_id, Duration::from_secs(30))
            .expect("wait for request ID counter Flow to close");
    }
}

#[derive(Clone)]
struct RpcRequestIdWorkflow {
    handler_calls: Arc<AtomicUsize>,
}

impl Flow for RpcRequestIdWorkflow {
    type StartInput = ();

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&TOTAL)
            .attribute(&PROCESSED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function(TRANSACTIONAL_ADD.is_transactional(), Self::add)
            .function(NON_TRANSACTIONAL_ADD, Self::add)
            .function_without_input(READ_TOTAL, Self::read_total)
            .function_without_input(TRANSACTIONAL_MARK.is_transactional(), Self::mark_processed)
            .function_without_input(NON_TRANSACTIONAL_MARK, Self::mark_processed)
    }
}

impl RpcRequestIdWorkflow {
    fn add(&self, context: &mut Context, delta: i32) -> HandlerResult<RpcResult<i32>> {
        self.handler_calls.fetch_add(1, Ordering::Relaxed);
        let total = TOTAL.get(context)?.unwrap_or_default() + delta;
        TOTAL.set(context, total)?;
        Ok(RpcResult::new(total))
    }

    fn read_total(&self, context: &mut Context) -> HandlerResult<RpcResult<i32>> {
        Ok(RpcResult::new(TOTAL.get(context)?.unwrap_or_default()))
    }

    fn mark_processed(&self, context: &mut Context) -> HandlerResult<RpcResult<bool>> {
        self.handler_calls.fetch_add(1, Ordering::Relaxed);
        if PROCESSED.get(context)?.unwrap_or_default() {
            return Ok(RpcResult::new(false));
        }
        PROCESSED.set(context, true)?;
        Ok(RpcResult::new(true))
    }
}
