/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Trigger Workflow Patterns: WCP-23 (transient trigger) and WCP-24 (persistent
// trigger).

import {
  Attribute,
  Channel,
  StepList,
  Wait,
  booleanCodec,
  doubleCodec,
  goTo,
  gracefulComplete,
  rpc,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

// WCP-23 Transient Trigger: Channels keep messages, so the RPC drops a motion
// event unless WaitFor recorded that a Step is waiting for one.

const isAwaitingMotionEvent = new Attribute("isAwaitingMotionEvent", booleanCodec);
const motionSensorArmed = new Channel("motionSensorArmed", stringCodec);
const motionEvent = new Channel("motionEvent", doubleCodec);

class AwaitMotionSensorArmed implements Step<void> {
  public getStepType(): string {
    return "AwaitMotionSensorArmed";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(motionSensorArmed.forOne());
  }

  public execute(_context: Context, _input: void): StepDecision {
    return goTo(AwaitMotionEvent, undefined);
  }
}

class AwaitMotionEvent implements Step<void> {
  public getStepType(): string {
    return "AwaitMotionEvent";
  }

  public waitFor(context: Context, _input: void): Wait {
    isAwaitingMotionEvent.set(context, true);
    return Wait.until(motionEvent.forOne());
  }

  public execute(context: Context, _input: void): StepDecision {
    isAwaitingMotionEvent.set(context, false);
    return gracefulComplete(motionEvent.results(context)[0]);
  }
}

export class TransientTriggerFlow implements Flow<void> {
  private readonly awaitMotionSensorArmed = new AwaitMotionSensorArmed();
  private readonly awaitMotionEvent = new AwaitMotionEvent();

  public getFlowType(): string {
    return "TransientTriggerFlow";
  }

  public getSteps(): StepList<void> {
    return StepList.startStep(this.awaitMotionSensorArmed).otherSteps(this.awaitMotionEvent);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [isAwaitingMotionEvent], channels: [motionSensorArmed, motionEvent] };
  }

  @rpc()
  public armMotionSensor(context: Context): void {
    motionSensorArmed.publish(context, "armed");
  }

  @rpc({ inputCodec: doubleCodec, outputCodec: booleanCodec })
  public sendMotionEvent(context: Context, value: number): RPCResult<boolean> {
    if (!isAwaitingMotionEvent.get(context)) {
      return { output: false };
    }
    motionEvent.publish(context, value);
    return { output: true };
  }
}

export { isAwaitingMotionEvent };

// WCP-24 Persistent Trigger: the label waits in the Channel until a Step
// consumes it.

const carrierReady = new Channel("carrierReady", stringCodec);
const shipmentLabel = new Channel("shipmentLabel", stringCodec);

class AwaitCarrierReady implements Step<void> {
  public getStepType(): string {
    return "AwaitCarrierReady";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(carrierReady.forOne());
  }

  public execute(_context: Context, _input: void): StepDecision {
    return goTo(AwaitShipmentLabel, undefined);
  }
}

class AwaitShipmentLabel implements Step<void> {
  public getStepType(): string {
    return "AwaitShipmentLabel";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(shipmentLabel.forOne());
  }

  public execute(context: Context, _input: void): StepDecision {
    return gracefulComplete(shipmentLabel.results(context)[0]);
  }
}

export class PersistentTriggerFlow implements Flow<void> {
  private readonly awaitCarrierReady = new AwaitCarrierReady();
  private readonly awaitShipmentLabel = new AwaitShipmentLabel();

  public getFlowType(): string {
    return "PersistentTriggerFlow";
  }

  public getSteps(): StepList<void> {
    return StepList.startStep(this.awaitCarrierReady).otherSteps(this.awaitShipmentLabel);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [carrierReady, shipmentLabel] };
  }

  @rpc()
  public markCarrierReady(context: Context): void {
    carrierReady.publish(context, "ready");
  }

  @rpc({ inputCodec: stringCodec })
  public sendShipmentLabel(context: Context, labelId: string): void {
    shipmentLabel.publish(context, labelId);
  }
}

export const transientTriggerFlow = new TransientTriggerFlow();
export const persistentTriggerFlow = new PersistentTriggerFlow();
