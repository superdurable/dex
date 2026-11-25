// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use std::sync::LazyLock;

use dex_sdk::{
    Attribute, AttributeMap, Channel, ChannelMap, Context, Flow, FlowTimeoutHandler, HandlerError,
    HandlerResult, PersistenceSchema, RetryPolicy, Rpc, RpcList, RpcResult, Step, StepDecision,
    StepList, StepMovement, StepOptions, Wait, WaitForFailurePolicy,
};

/// Needs percent-encoding, so the tests cover encoded AttributeMap lock keys.
pub(crate) const SPACED_INSTANCE: &str = "item a";
pub(crate) const WRITTEN: &str = "written";

pub(crate) static LOCK_REQ_TOTAL: LazyLock<Attribute<i64>> =
    LazyLock::new(|| Attribute::new("lock-req-total").writes_require_lock());
pub(crate) static LOCK_REQ_COORDINATION: LazyLock<Attribute<bool>> =
    LazyLock::new(|| Attribute::new("lock-req-coordination"));
pub(crate) static LOCK_REQ_ITEMS: LazyLock<AttributeMap<i64>> =
    LazyLock::new(|| AttributeMap::new("lock-req-items").writes_require_lock());
pub(crate) static LOCK_REQ_SHARED: LazyLock<AttributeMap<i64>> = LazyLock::new(|| {
    AttributeMap::new("lock-req-shared").writes_require_attribute_lock(&LOCK_REQ_COORDINATION)
});
pub(crate) static LOCK_REQ_FREE: LazyLock<Attribute<i64>> =
    LazyLock::new(|| Attribute::new("lock-req-free"));
static LOCK_REQ_REPLIES: LazyLock<Channel<String>> = LazyLock::new(|| {
    Channel::new("lock-req-replies").publishes_require_attribute_lock(&LOCK_REQ_COORDINATION)
});
static LOCK_REQ_EVENTS: LazyLock<ChannelMap<String>> = LazyLock::new(|| {
    ChannelMap::new("lock-req-events").publishes_require_attribute_lock(&LOCK_REQ_COORDINATION)
});
static LOCK_REQ_FREE_CHANNEL: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("lock-req-free-channel"));

fn lock_requirement_schema() -> PersistenceSchema {
    PersistenceSchema::new()
        .attribute(&LOCK_REQ_TOTAL)
        .attribute(&LOCK_REQ_COORDINATION)
        .attribute_map(&LOCK_REQ_ITEMS)
        .attribute_map(&LOCK_REQ_SHARED)
        .attribute(&LOCK_REQ_FREE)
        .channel(&LOCK_REQ_REPLIES)
        .channel_map(&LOCK_REQ_EVENTS)
        .channel(&LOCK_REQ_FREE_CHANNEL)
}

/// Runs one attempt and routes an `execute` failure to the recovery Step.
fn single_attempt_options(locks: &[dex_sdk::AttributeLock]) -> StepOptions<()> {
    let options = StepOptions::new()
        .wait_for_retry(RetryPolicy::new().maximum_attempts(1))
        .execute_retry(RetryPolicy::new().maximum_attempts(1))
        .on_execute_failure_proceed_to(&LockReqRecoveryStep);
    locks
        .iter()
        .fold(options, |options, lock| options.execute_lock(lock.clone()))
}

/// Routes its start input to one Step per case.
#[derive(Default)]
pub(crate) struct AttributeLockRequirementStepFlow {
    dispatch: LockReqDispatchStep,
    unlocked_total: LockReqUnlockedTotalStep,
    wait_for_total: LockReqWaitForTotalStep,
    locked_wait_for_total: LockReqLockedWaitForTotalStep,
    locked_total: LockReqLockedTotalStep,
    locked_item: LockReqLockedItemStep,
    other_item: LockReqOtherItemStep,
    locked_shared: LockReqLockedSharedStep,
    unlocked_shared: LockReqUnlockedSharedStep,
    undeclared: LockReqUndeclaredStep,
    recovery: LockReqRecoveryStep,
}

impl Flow for AttributeLockRequirementStepFlow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.dispatch)
            .and(&self.unlocked_total)
            .and(&self.wait_for_total)
            .and(&self.locked_wait_for_total)
            .and(&self.locked_total)
            .and(&self.locked_item)
            .and(&self.other_item)
            .and(&self.locked_shared)
            .and(&self.unlocked_shared)
            .and(&self.undeclared)
            .and(&self.recovery)
    }

    fn persistence(&self) -> PersistenceSchema {
        lock_requirement_schema()
    }
}

#[derive(Default)]
struct LockReqDispatchStep;

impl Step for LockReqDispatchStep {
    type Input = String;

    fn execute(&self, _context: &mut Context, step_case: String) -> HandlerResult<StepDecision> {
        Ok(match step_case.as_str() {
            "unlocked-total" => StepDecision::go_to(&LockReqUnlockedTotalStep, ()),
            // A per-movement override does not count, even when it names the required lock.
            "movement-lock-total" => StepDecision::go_to_many([StepMovement::to_with_options(
                &LockReqUnlockedTotalStep,
                (),
                single_attempt_options(&[LOCK_REQ_TOTAL.lock()]),
            )]),
            "wait-for-total" => StepDecision::go_to(&LockReqWaitForTotalStep, ()),
            "locked-wait-for-total" => StepDecision::go_to(&LockReqLockedWaitForTotalStep, ()),
            "locked-total" => StepDecision::go_to(&LockReqLockedTotalStep, ()),
            "locked-item" => StepDecision::go_to(&LockReqLockedItemStep, ()),
            "other-item" => StepDecision::go_to(&LockReqOtherItemStep, ()),
            "locked-shared" => StepDecision::go_to(&LockReqLockedSharedStep, ()),
            "unlocked-shared" => StepDecision::go_to(&LockReqUnlockedSharedStep, ()),
            "undeclared" => StepDecision::go_to(&LockReqUndeclaredStep, ()),
            _ => {
                return Err(HandlerError::new(
                    "UnknownLockRequirementCase",
                    format!("unknown lock requirement case {step_case:?}"),
                ));
            }
        })
    }
}

#[derive(Default)]
struct LockReqUnlockedTotalStep;

impl Step for LockReqUnlockedTotalStep {
    type Input = ();

    fn execute(&self, context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        LOCK_REQ_TOTAL.set(context, 1)?;
        Ok(StepDecision::graceful_complete(WRITTEN.to_string()))
    }

    fn options(&self) -> StepOptions<()> {
        single_attempt_options(&[])
    }
}

/// Registers the lock for `execute` only, so its `wait_for` write fails.
#[derive(Default)]
struct LockReqWaitForTotalStep;

impl Step for LockReqWaitForTotalStep {
    type Input = ();

    fn wait_for(&self, context: &mut Context, _input: ()) -> HandlerResult<Wait> {
        LOCK_REQ_TOTAL.set(context, 1)?;
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        Ok(wait_for_result(context))
    }

    fn options(&self) -> StepOptions<()> {
        single_attempt_options(&[LOCK_REQ_TOTAL.lock()])
            .wait_for_failure(WaitForFailurePolicy::Proceed)
    }
}

/// Registers the lock for `wait_for`, so its `wait_for` write commits.
#[derive(Default)]
struct LockReqLockedWaitForTotalStep;

impl Step for LockReqLockedWaitForTotalStep {
    type Input = ();

    fn wait_for(&self, context: &mut Context, _input: ()) -> HandlerResult<Wait> {
        LOCK_REQ_TOTAL.set(context, 1)?;
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        Ok(wait_for_result(context))
    }

    fn options(&self) -> StepOptions<()> {
        single_attempt_options(&[])
            .wait_for_lock(LOCK_REQ_TOTAL.lock())
            .wait_for_failure(WaitForFailurePolicy::Proceed)
    }
}

/// Completes with the `wait_for` failure, or [`WRITTEN`] when `wait_for` succeeded.
fn wait_for_result(context: &Context) -> StepDecision {
    if !context.wait_for_method_failed() {
        return StepDecision::graceful_complete(WRITTEN.to_string());
    }
    match context.recovery_error() {
        Some(recovery) => StepDecision::graceful_complete(format!(
            "{}: {}",
            recovery.error_type(),
            recovery.detail()
        )),
        None => {
            StepDecision::graceful_complete("wait_for failed without a recovery error".to_string())
        }
    }
}

#[derive(Default)]
struct LockReqLockedTotalStep;

impl Step for LockReqLockedTotalStep {
    type Input = ();

    fn execute(&self, context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        LOCK_REQ_TOTAL.set(context, 1)?;
        Ok(StepDecision::graceful_complete(WRITTEN.to_string()))
    }

    fn options(&self) -> StepOptions<()> {
        single_attempt_options(&[LOCK_REQ_TOTAL.lock()])
    }
}

#[derive(Default)]
struct LockReqLockedItemStep;

impl Step for LockReqLockedItemStep {
    type Input = ();

    fn execute(&self, context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        LOCK_REQ_ITEMS.set(context, SPACED_INSTANCE, 1)?;
        Ok(StepDecision::graceful_complete(WRITTEN.to_string()))
    }

    fn options(&self) -> StepOptions<()> {
        single_attempt_options(&[LOCK_REQ_ITEMS.lock(SPACED_INSTANCE)])
    }
}

/// Holds one instance's lock and writes another instance.
#[derive(Default)]
struct LockReqOtherItemStep;

impl Step for LockReqOtherItemStep {
    type Input = ();

    fn execute(&self, context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        LOCK_REQ_ITEMS.set(context, SPACED_INSTANCE, 1)?;
        LOCK_REQ_ITEMS.set(context, "item b", 1)?;
        Ok(StepDecision::graceful_complete(WRITTEN.to_string()))
    }

    fn options(&self) -> StepOptions<()> {
        single_attempt_options(&[LOCK_REQ_ITEMS.lock(SPACED_INSTANCE)])
    }
}

#[derive(Default)]
struct LockReqLockedSharedStep;

impl Step for LockReqLockedSharedStep {
    type Input = ();

    fn execute(&self, context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        LOCK_REQ_SHARED.set(context, "x", 1)?;
        Ok(StepDecision::graceful_complete(WRITTEN.to_string()))
    }

    fn options(&self) -> StepOptions<()> {
        single_attempt_options(&[LOCK_REQ_COORDINATION.lock()])
    }
}

/// Holds the written instance's own lock, not the declared coordination lock.
#[derive(Default)]
struct LockReqUnlockedSharedStep;

impl Step for LockReqUnlockedSharedStep {
    type Input = ();

    fn execute(&self, context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        LOCK_REQ_SHARED.set(context, "x", 1)?;
        Ok(StepDecision::graceful_complete(WRITTEN.to_string()))
    }

    fn options(&self) -> StepOptions<()> {
        single_attempt_options(&[LOCK_REQ_SHARED.lock("x")])
    }
}

#[derive(Default)]
struct LockReqUndeclaredStep;

impl Step for LockReqUndeclaredStep {
    type Input = ();

    fn execute(&self, context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        LOCK_REQ_FREE.set(context, 1)?;
        LOCK_REQ_FREE_CHANNEL.publish(context, "free".to_string())?;
        Ok(StepDecision::graceful_complete(WRITTEN.to_string()))
    }

    fn options(&self) -> StepOptions<()> {
        single_attempt_options(&[])
    }
}

/// Completes with the failed attempt's error type and detail.
#[derive(Default)]
struct LockReqRecoveryStep;

impl Step for LockReqRecoveryStep {
    type Input = ();

    fn execute(&self, context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        let recovery = context.recovery_error().ok_or_else(|| {
            HandlerError::new(
                "MissingRecoveryError",
                "recovery Step has no recovery error",
            )
        })?;
        Ok(StepDecision::graceful_complete(format!(
            "{}: {}",
            recovery.error_type(),
            recovery.detail()
        )))
    }
}

/// Has no Steps, so it stays running for RPC cases.
pub(crate) struct AttributeLockRequirementRpcFlow;

impl AttributeLockRequirementRpcFlow {
    pub(crate) const PUBLISH_REPLY_UNLOCKED: Rpc<String, ()> = Rpc::new("publish_reply_unlocked");
    pub(crate) const PUBLISH_REPLY_LOCKED: Rpc<String, ()> = Rpc::new("publish_reply_locked");
    pub(crate) const COUNT_REPLIES: Rpc<(), usize> = Rpc::new("count_replies");
    pub(crate) const SET_ITEM: Rpc<String, ()> = Rpc::new("set_item");
    pub(crate) const SET_ITEM_LOCKED: Rpc<String, ()> = Rpc::new("set_item_locked");
    pub(crate) const PUBLISH_EVENT_UNLOCKED: Rpc<String, ()> = Rpc::new("publish_event_unlocked");
    pub(crate) const COUNT_EVENTS: Rpc<String, usize> = Rpc::new("count_events");
    pub(crate) const SET_TOTAL_LOCKED: Rpc<i64, ()> = Rpc::new("set_total_locked");
    pub(crate) const DELETE_TOTAL_UNLOCKED: Rpc<(), ()> = Rpc::new("delete_total_unlocked");

    /// Also writes an undeclared Attribute, which the failure must discard too.
    fn publish_reply_unlocked(&self, context: &mut Context, reply: String) -> HandlerResult<()> {
        LOCK_REQ_FREE.set(context, 1)?;
        LOCK_REQ_REPLIES.publish(context, reply)
    }

    fn publish_reply_locked(&self, context: &mut Context, reply: String) -> HandlerResult<()> {
        LOCK_REQ_REPLIES.publish(context, reply)
    }

    fn count_replies(&self, context: &mut Context) -> HandlerResult<RpcResult<usize>> {
        Ok(RpcResult::new(LOCK_REQ_REPLIES.size(context)?))
    }

    fn set_item(&self, context: &mut Context, instance: String) -> HandlerResult<()> {
        LOCK_REQ_ITEMS.set(context, &instance, 1)
    }

    /// Also writes an undeclared Attribute, which the failure must discard too.
    fn publish_event_unlocked(&self, context: &mut Context, instance: String) -> HandlerResult<()> {
        LOCK_REQ_FREE.set(context, 1)?;
        LOCK_REQ_EVENTS.publish(context, &instance, "event".to_string())
    }

    fn count_events(
        &self,
        context: &mut Context,
        instance: String,
    ) -> HandlerResult<RpcResult<usize>> {
        Ok(RpcResult::new(LOCK_REQ_EVENTS.size(context, &instance)?))
    }

    fn set_total(&self, context: &mut Context, total: i64) -> HandlerResult<()> {
        LOCK_REQ_TOTAL.set(context, total)
    }

    fn delete_total(&self, context: &mut Context) -> HandlerResult<()> {
        LOCK_REQ_TOTAL.delete(context)
    }
}

impl Flow for AttributeLockRequirementRpcFlow {
    type StartInput = ();

    fn persistence(&self) -> PersistenceSchema {
        lock_requirement_schema()
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .procedure(Self::PUBLISH_REPLY_UNLOCKED, Self::publish_reply_unlocked)
            .procedure(
                Self::PUBLISH_REPLY_LOCKED.lock(LOCK_REQ_COORDINATION.lock()),
                Self::publish_reply_locked,
            )
            .function_without_input(Self::COUNT_REPLIES, Self::count_replies)
            .procedure(Self::SET_ITEM, Self::set_item)
            .procedure(
                Self::SET_ITEM_LOCKED.lock(LOCK_REQ_ITEMS.lock(SPACED_INSTANCE)),
                Self::set_item,
            )
            .procedure(Self::PUBLISH_EVENT_UNLOCKED, Self::publish_event_unlocked)
            .function(Self::COUNT_EVENTS, Self::count_events)
            .procedure(
                Self::SET_TOTAL_LOCKED.lock(LOCK_REQ_TOTAL.lock()),
                Self::set_total,
            )
            .procedure_without_input(Self::DELETE_TOTAL_UNLOCKED, Self::delete_total)
    }
}

/// Writes a declared Attribute from its unchecked timeout handler.
pub(crate) struct AttributeLockRequirementTimeoutFlow;

impl AttributeLockRequirementTimeoutFlow {
    fn handle_timeout(&self, context: &mut Context) -> HandlerResult<StepDecision> {
        LOCK_REQ_TOTAL.set(context, 5)?;
        Ok(StepDecision::force_complete("expired".to_string()))
    }
}

impl Flow for AttributeLockRequirementTimeoutFlow {
    type StartInput = ();

    fn persistence(&self) -> PersistenceSchema {
        lock_requirement_schema()
    }

    fn timeout_handler(&self) -> Option<FlowTimeoutHandler<Self>> {
        Some(Self::handle_timeout)
    }
}
