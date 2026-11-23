// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dex_sdk::*;
use std::sync::LazyLock;
use std::time::Duration;

static STATUS: LazyLock<Attribute<String>> = LazyLock::new(|| Attribute::new("status"));
static ITEMS: LazyLock<AttributeMap<String>> = LazyLock::new(|| AttributeMap::new("items"));
static REQUESTS: LazyLock<Channel<String>> = LazyLock::new(|| Channel::new("requests"));
static MESSAGES: LazyLock<ChannelMap<String>> = LazyLock::new(|| ChannelMap::new("messages"));
static BRANCHES: LazyLock<Join<String>> = LazyLock::new(|| Join::new("branches"));
static PROGRESS: LazyLock<Stream<String>> = LazyLock::new(|| Stream::new("progress", 1024));
const SUBMIT: Rpc<String, String> = Rpc::new("Submit");

struct DeliveryFlow {
    start: Submit,
    complete: Complete,
    confirm: Confirm,
}

impl Flow for DeliveryFlow {
    type StartInput = String;

    fn flow_type(&self) -> &'static str {
        "Delivery"
    }

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
            .and(&self.complete)
            .and(&self.confirm)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&STATUS)
            .attribute_map(&ITEMS)
            .channel(&REQUESTS)
            .channel_map(&MESSAGES)
            .join(&BRANCHES)
            .stream(&PROGRESS)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().function(SUBMIT.lock(STATUS.lock()), Self::submit)
    }

    fn timeout_handler(&self) -> Option<FlowTimeoutHandler<Self>> {
        Some(Self::on_timeout)
    }
}

impl DeliveryFlow {
    fn submit(&self, ctx: &mut Context, input: String) -> HandlerResult<RpcResult<String>> {
        REQUESTS.publish(ctx, input.clone())?;
        Ok(RpcResult::new(input).then(StepMovement::to(&self.complete, ())))
    }

    fn on_timeout(&self, _: &mut Context) -> HandlerResult<StepDecision> {
        Ok(StepDecision::force_fail("Delivery timed out"))
    }
}

// dex:group group-id:delivery group-label:"Delivery"
// dex:explanation text:"Records a delivery request."
struct Submit;

impl Step for Submit {
    type Input = String;

    fn execute(&self, ctx: &mut Context, input: String) -> HandlerResult<StepDecision> {
        STATUS.set(ctx, input.clone())?;
        ITEMS.set(ctx, "first", input.clone())?;
        PROGRESS.write(ctx, input)?;
        Ok(StepDecision::go_to_many([
            StepMovement::to(&Complete, ()),
            StepMovement::to(&Confirm, ()),
        ]))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .execute_lock(STATUS.lock())
            .on_execute_failure_proceed_to(&Complete)
    }
}

struct Complete;

// dex:group group-id:delivery group-label:"Delivery"
// dex:explanation text:"Completes the delivery."
impl Step for Complete {
    type Input = ();

    fn step_type(&self) -> &'static str {
        "CompleteDelivery"
    }

    fn wait_for(&self, _: &mut Context, _: ()) -> HandlerResult<Wait> {
        Ok(Wait::any_of([
            REQUESTS.for_one(),
            MESSAGES.for_one("first"),
            Timer::by_duration(Duration::from_secs(1)),
            Condition::graceful_completion_requested(),
            SubFlow::run(
                &VerifyAddress {
                    start: CheckAddress,
                },
                (),
            )
            .map_err(HandlerError::from_error)?,
        ]))
    }

    fn execute(&self, ctx: &mut Context, _: ()) -> HandlerResult<StepDecision> {
        let status = STATUS.get_required(ctx)?;
        if status.is_empty() {
            Ok(StepDecision::force_complete_if_channels_empty(
                status,
                StepMovement::to(&Confirm, ()),
                [REQUESTS.when_empty()],
            ))
        } else {
            Ok(StepDecision::graceful_complete(status).cancel_step(&Confirm))
        }
    }
}

// dex:group group-id:delivery group-label:"Delivery"
// dex:explanation text:"Confirms the delivery."
struct Confirm;

impl Step for Confirm {
    type Input = ();

    fn wait_for(&self, _: &mut Context, _: ()) -> HandlerResult<Wait> {
        Ok(Wait::until(BRANCHES.for_first_branch(&JoinRound {
            round_key: "delivery".to_owned(),
            branch_count: 1,
        })))
    }

    fn execute(&self, _: &mut Context, _: ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::dead_end())
    }
}

struct VerifyAddress {
    start: CheckAddress,
}

impl Flow for VerifyAddress {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }
}

// dex:group group-id:address group-label:"Address"
// dex:explanation text:"Verifies a delivery address."
struct CheckAddress;

impl Step for CheckAddress {
    type Input = ();

    fn execute(&self, _: &mut Context, _: ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::force_complete(()))
    }
}
