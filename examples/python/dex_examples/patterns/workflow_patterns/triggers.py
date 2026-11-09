# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Trigger Workflow Patterns: WCP-23 (transient) and WCP-24 (persistent)."""

from __future__ import annotations

from dex import (
    Attribute,
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    RPCResult,
    Step,
    StepDecision,
    StepList,
    Wait,
    go_to,
    graceful_complete,
    rpc,
)

# WCP-23: Channels keep messages, so the RPC drops events while no Step waits.

IS_AWAITING_MOTION_EVENT = Attribute("isAwaitingMotionEvent", bool)
MOTION_SENSOR_ARMED = Channel("motionSensorArmed", str)
MOTION_EVENT = Channel("motionEvent", int)


class AwaitMotionSensorArmed(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(MOTION_SENSOR_ARMED.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        return go_to(AwaitMotionEvent, None)


class AwaitMotionEvent(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        IS_AWAITING_MOTION_EVENT.set(context, True)
        return Wait.until(MOTION_EVENT.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        IS_AWAITING_MOTION_EVENT.set(context, False)
        return graceful_complete(MOTION_EVENT.results(context)[0])


class TransientTriggerFlow(Flow[None]):
    def __init__(self) -> None:
        self.await_motion_sensor_armed = AwaitMotionSensorArmed()
        self.await_motion_event = AwaitMotionEvent()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.await_motion_sensor_armed).other_steps(
            self.await_motion_event
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            IS_AWAITING_MOTION_EVENT, MOTION_SENSOR_ARMED, MOTION_EVENT
        )

    @rpc
    def arm_motion_sensor(self, context: Context) -> None:
        MOTION_SENSOR_ARMED.publish(context, "armed")

    @rpc
    def send_motion_event(self, context: Context, input: int) -> RPCResult[bool]:
        if not IS_AWAITING_MOTION_EVENT.get(context):
            return RPCResult(False)
        MOTION_EVENT.publish(context, input)
        return RPCResult(True)


# WCP-24 Persistent Trigger: the label waits in the Channel until a Step consumes it.

CARRIER_READY = Channel("carrierReady", str)
SHIPMENT_LABEL = Channel("shipmentLabel", str)


class AwaitCarrierReady(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(CARRIER_READY.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        return go_to(AwaitShipmentLabel, None)


class AwaitShipmentLabel(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(SHIPMENT_LABEL.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete(SHIPMENT_LABEL.results(context)[0])


class PersistentTriggerFlow(Flow[None]):
    def __init__(self) -> None:
        self.await_carrier_ready = AwaitCarrierReady()
        self.await_shipment_label = AwaitShipmentLabel()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.await_carrier_ready).other_steps(
            self.await_shipment_label
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(CARRIER_READY, SHIPMENT_LABEL)

    @rpc
    def mark_carrier_ready(self, context: Context) -> None:
        CARRIER_READY.publish(context, "ready")

    @rpc
    def send_shipment_label(self, context: Context, input: str) -> None:
        SHIPMENT_LABEL.publish(context, input)
