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

//! Triggers: Transient Trigger (WCP-23) and Persistent Trigger (WCP-24).

use std::sync::LazyLock;

use dex_sdk::{
    Attribute, Channel, Context, Flow, HandlerError, HandlerResult, PersistenceSchema, Rpc,
    RpcList, RpcResult, Step, StepDecision, StepList, StepOptions, Wait,
};

pub static IS_AWAITING_MOTION_EVENT: LazyLock<Attribute<bool>> =
    LazyLock::new(|| Attribute::new("isAwaitingMotionEvent"));
static MOTION_SENSOR_ARMED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("motionSensorArmed"));
static MOTION_EVENT: LazyLock<Channel<i64>> = LazyLock::new(|| Channel::new("motionEvent"));

pub const ARM_MOTION_SENSOR: Rpc<(), ()> = Rpc::new("arm_motion_sensor");
pub const SEND_MOTION_EVENT: Rpc<i64, bool> = Rpc::new("send_motion_event");

/// WCP-23 Transient Trigger: Channels keep messages, so the RPC accepts one
/// motion event, under a shared lock, only while a Step waits for it.
#[derive(Default)]
pub struct TransientTriggerFlow {
    await_motion_sensor_armed: AwaitMotionSensorArmed,
    await_motion_event: AwaitMotionEvent,
}

impl TransientTriggerFlow {
    fn arm_motion_sensor(&self, context: &mut Context) -> HandlerResult<()> {
        MOTION_SENSOR_ARMED.publish(context, "armed".to_string())
    }

    fn send_motion_event(
        &self,
        context: &mut Context,
        value: i64,
    ) -> HandlerResult<RpcResult<bool>> {
        if !IS_AWAITING_MOTION_EVENT.get(context)?.unwrap_or(false) {
            return Ok(RpcResult::new(false));
        }
        IS_AWAITING_MOTION_EVENT.set(context, false)?;
        MOTION_EVENT.publish(context, value)?;
        Ok(RpcResult::new(true))
    }
}

impl Flow for TransientTriggerFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.await_motion_sensor_armed).and(&self.await_motion_event)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&IS_AWAITING_MOTION_EVENT)
            .channel(&MOTION_SENSOR_ARMED)
            .channel(&MOTION_EVENT)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .procedure_without_input(ARM_MOTION_SENSOR, Self::arm_motion_sensor)
            .function(
                SEND_MOTION_EVENT.lock(IS_AWAITING_MOTION_EVENT.lock()),
                Self::send_motion_event,
            )
    }
}

#[derive(Default)]
struct AwaitMotionSensorArmed;

impl Step for AwaitMotionSensorArmed {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(MOTION_SENSOR_ARMED.for_one()))
    }

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(&AwaitMotionEvent, ()))
    }
}

#[derive(Default)]
struct AwaitMotionEvent;

impl Step for AwaitMotionEvent {
    type Input = ();

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().wait_for_lock(IS_AWAITING_MOTION_EVENT.lock())
    }

    fn wait_for(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        IS_AWAITING_MOTION_EVENT.set(context, true)?;
        Ok(Wait::until(MOTION_EVENT.for_one()))
    }

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        let motion_value = MOTION_EVENT
            .condition_results(context)?
            .into_iter()
            .next()
            .ok_or_else(|| HandlerError::new("MissingMotionEvent", "no motion event consumed"))?;
        Ok(StepDecision::graceful_complete(motion_value))
    }
}

static CARRIER_READY: LazyLock<Channel<String>> = LazyLock::new(|| Channel::new("carrierReady"));
static SHIPMENT_LABEL: LazyLock<Channel<String>> = LazyLock::new(|| Channel::new("shipmentLabel"));

pub const MARK_CARRIER_READY: Rpc<(), ()> = Rpc::new("mark_carrier_ready");
pub const SEND_SHIPMENT_LABEL: Rpc<String, ()> = Rpc::new("send_shipment_label");

/// WCP-24 Persistent Trigger: a label sent early stays queued until the
/// Step that needs it starts waiting.
#[derive(Default)]
pub struct PersistentTriggerFlow {
    await_carrier_ready: AwaitCarrierReady,
    await_shipment_label: AwaitShipmentLabel,
}

impl PersistentTriggerFlow {
    fn mark_carrier_ready(&self, context: &mut Context) -> HandlerResult<()> {
        CARRIER_READY.publish(context, "ready".to_string())
    }

    fn send_shipment_label(&self, context: &mut Context, label_id: String) -> HandlerResult<()> {
        SHIPMENT_LABEL.publish(context, label_id)
    }
}

impl Flow for PersistentTriggerFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.await_carrier_ready).and(&self.await_shipment_label)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .channel(&CARRIER_READY)
            .channel(&SHIPMENT_LABEL)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .procedure_without_input(MARK_CARRIER_READY, Self::mark_carrier_ready)
            .procedure(SEND_SHIPMENT_LABEL, Self::send_shipment_label)
    }
}

#[derive(Default)]
struct AwaitCarrierReady;

impl Step for AwaitCarrierReady {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(CARRIER_READY.for_one()))
    }

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(&AwaitShipmentLabel, ()))
    }
}

#[derive(Default)]
struct AwaitShipmentLabel;

impl Step for AwaitShipmentLabel {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(SHIPMENT_LABEL.for_one()))
    }

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        let label_id = SHIPMENT_LABEL
            .condition_results(context)?
            .into_iter()
            .next()
            .ok_or_else(|| {
                HandlerError::new("MissingShipmentLabel", "no shipment label consumed")
            })?;
        Ok(StepDecision::graceful_complete(label_id))
    }
}
