// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use std::sync::LazyLock;

use dex_sdk::{
    Attribute, AttributeIndex, AttributeMap, Channel, Context, Flow, HandlerResult,
    PersistenceSchema, Rpc, RpcList, RpcResult, Step, StepDecision, StepList, StepMovement,
    StepOptions, Wait,
};

static EXECUTION_COUNT: LazyLock<Attribute<i32>> =
    LazyLock::new(|| Attribute::new("reset-execution-count"));
static CHANNEL: LazyLock<Channel<()>> = LazyLock::new(|| Channel::new("rpc-channel"));
static DATA: LazyLock<Attribute<String>> = LazyLock::new(|| Attribute::new("rpc-lock-data"));
static KEYWORD: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("CustomKeywordField").indexed(AttributeIndex::keyword()));
static COUNTER: LazyLock<Attribute<i32>> =
    LazyLock::new(|| Attribute::new("CustomIntField").indexed(AttributeIndex::int()));

pub(crate) struct ResetWorkflow {
    channel: Channel<()>,
    pub(crate) data: Attribute<String>,
    pub(crate) keyword: Attribute<String>,
    pub(crate) counter: Attribute<i32>,
    pub(crate) items: AttributeMap<String>,
    pub(crate) execution_count: Attribute<i32>,
    pub(crate) first: LockWaitStep,
    second: LockCompleteStep,
}

impl ResetWorkflow {
    pub(crate) const EXPECTED_VALUE: &str = "random-string";
    pub(crate) const WITH_LOCKING: Rpc<(), ()> = Rpc::new("with_locking");
    pub(crate) const WITH_ATTRIBUTE_MAP_LOCK: Rpc<(), ()> = Rpc::new("with_attribute_map_lock");
    pub(crate) const WITHOUT_LOCKING: Rpc<(), ()> = Rpc::new("without_locking");

    pub(crate) fn new() -> Self {
        let channel = CHANNEL.clone();
        let data = DATA.clone();
        let keyword = KEYWORD.clone();
        let counter = COUNTER.clone();
        let items = AttributeMap::new("rpc-lock-items");
        let execution_count = EXECUTION_COUNT.clone();
        Self {
            first: LockWaitStep {
                channel: channel.clone(),
            },
            second: LockCompleteStep {
                execution_count: execution_count.clone(),
            },
            channel,
            data,
            keyword,
            counter,
            items,
            execution_count,
        }
    }

    fn with_locking(&self, context: &mut Context) -> HandlerResult<RpcResult<()>> {
        self.write_attributes(context)?;
        self.channel.publish(context, ())?;
        Ok(RpcResult::new(()).then(StepMovement::to(&self.second, ())))
    }

    fn with_attribute_map_lock(&self, context: &mut Context) -> HandlerResult<()> {
        self.items.set(context, "order-1", "locked".to_string())
    }

    fn without_locking(&self, context: &mut Context) -> HandlerResult<RpcResult<()>> {
        self.write_attributes(context)?;
        self.channel.publish(context, ())?;
        Ok(RpcResult::new(()).then(StepMovement::to(&self.second, ())))
    }

    fn write_attributes(&self, context: &mut Context) -> HandlerResult<()> {
        self.data.set(context, Self::EXPECTED_VALUE.to_string())?;
        self.keyword
            .set(context, Self::EXPECTED_VALUE.to_string())?;
        self.counter.set(context, 100)
    }
}

impl Flow for ResetWorkflow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.first).and(&self.second)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&self.data)
            .attribute(&self.keyword)
            .attribute(&self.counter)
            .attribute_map(&self.items)
            .attribute(&self.execution_count)
            .channel(&self.channel)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function_without_input(
                Self::WITH_LOCKING
                    .lock(self.data.lock())
                    .lock(self.keyword.lock())
                    .lock(self.counter.lock()),
                Self::with_locking,
            )
            .procedure_without_input(
                Self::WITH_ATTRIBUTE_MAP_LOCK.lock(self.items.lock("order-1")),
                Self::with_attribute_map_lock,
            )
            .function_without_input(Self::WITHOUT_LOCKING, Self::without_locking)
    }
}

pub(crate) struct LockWaitStep {
    channel: Channel<()>,
}

impl Step for LockWaitStep {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, (): ()) -> HandlerResult<Wait> {
        Ok(Wait::until(self.channel.for_one()))
    }

    fn execute(&self, _context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(
            &LockCompleteStep {
                execution_count: EXECUTION_COUNT.clone(),
            },
            (),
        ))
    }
}

struct LockCompleteStep {
    execution_count: Attribute<i32>,
}

impl Step for LockCompleteStep {
    type Input = ();

    fn execute(&self, context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        let next = self.execution_count.get(context)?.unwrap_or_default() + 1;
        self.execution_count.set(context, next)?;
        Ok(StepDecision::graceful_complete(next))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_lock(self.execution_count.lock())
    }
}
