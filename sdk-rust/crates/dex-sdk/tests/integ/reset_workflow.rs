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

pub(crate) static EXECUTION_COUNT: LazyLock<Attribute<i32>> =
    LazyLock::new(|| Attribute::new("reset-execution-count"));
pub(crate) static CHANNEL: LazyLock<Channel<()>> = LazyLock::new(|| Channel::new("rpc-channel"));
pub(crate) static DATA: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("rpc-lock-data"));
pub(crate) static KEYWORD: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("CustomKeywordField").indexed(AttributeIndex::keyword()));
pub(crate) static COUNTER: LazyLock<Attribute<i32>> =
    LazyLock::new(|| Attribute::new("CustomIntField").indexed(AttributeIndex::int()));

pub(crate) struct ResetWorkflow {
    pub(crate) items: AttributeMap<String>,
    pub(crate) first: LockWaitStep,
    second: LockCompleteStep,
}

impl ResetWorkflow {
    pub(crate) const EXPECTED_VALUE: &str = "random-string";
    pub(crate) const WITH_LOCKING: Rpc<(), ()> = Rpc::new("with_locking");
    pub(crate) const WITH_ATTRIBUTE_MAP_LOCK: Rpc<(), ()> = Rpc::new("with_attribute_map_lock");
    pub(crate) const WITHOUT_LOCKING: Rpc<(), ()> = Rpc::new("without_locking");

    pub(crate) fn new() -> Self {
        let items = AttributeMap::new("rpc-lock-items");
        Self {
            first: LockWaitStep,
            second: LockCompleteStep,
            items,
        }
    }

    fn with_locking(&self, context: &mut Context) -> HandlerResult<RpcResult<()>> {
        self.write_attributes(context)?;
        CHANNEL.publish(context, ())?;
        Ok(RpcResult::new(()).then(StepMovement::to(&self.second, ())))
    }

    fn with_attribute_map_lock(&self, context: &mut Context) -> HandlerResult<()> {
        self.items.set(context, "order-1", "locked".to_string())
    }

    fn without_locking(&self, context: &mut Context) -> HandlerResult<RpcResult<()>> {
        self.write_attributes(context)?;
        CHANNEL.publish(context, ())?;
        Ok(RpcResult::new(()).then(StepMovement::to(&self.second, ())))
    }

    fn write_attributes(&self, context: &mut Context) -> HandlerResult<()> {
        DATA.set(context, Self::EXPECTED_VALUE.to_string())?;
        KEYWORD.set(context, Self::EXPECTED_VALUE.to_string())?;
        COUNTER.set(context, 100)
    }
}

impl Flow for ResetWorkflow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.first).and(&self.second)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&DATA)
            .attribute(&KEYWORD)
            .attribute(&COUNTER)
            .attribute_map(&self.items)
            .attribute(&EXECUTION_COUNT)
            .channel(&CHANNEL)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function_without_input(
                Self::WITH_LOCKING
                    .lock(DATA.lock())
                    .lock(KEYWORD.lock())
                    .lock(COUNTER.lock()),
                Self::with_locking,
            )
            .procedure_without_input(
                Self::WITH_ATTRIBUTE_MAP_LOCK.lock(self.items.lock("order-1")),
                Self::with_attribute_map_lock,
            )
            .function_without_input(Self::WITHOUT_LOCKING, Self::without_locking)
    }
}

pub(crate) struct LockWaitStep;

impl Step for LockWaitStep {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, (): ()) -> HandlerResult<Wait> {
        Ok(Wait::until(CHANNEL.for_one()))
    }

    fn execute(&self, _context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(&LockCompleteStep, ()))
    }
}

struct LockCompleteStep;

impl Step for LockCompleteStep {
    type Input = ();

    fn execute(&self, context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        let next = EXECUTION_COUNT.get(context)?.unwrap_or_default() + 1;
        EXECUTION_COUNT.set(context, next)?;
        Ok(StepDecision::graceful_complete(next))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_lock(EXECUTION_COUNT.lock())
    }
}
