/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
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

import {
  Channel,
  StepList,
  Timer,
  Wait,
  doubleCodec,
  goTo,
  gracefulComplete,
  rpc,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

const approval = new Channel("Approval", stringCodec);

class ChannelWait implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "ChannelWait";
  }

  public waitFor(_context: Context, input: number): Wait {
    return Wait.anyOf(
      approval.forOne(),
      Timer.byDuration(input * 1000),
    );
  }

  public execute(context: Context, input: number): StepDecision {
    const approvals = approval.results(context);
    if (approvals.length > 0) {
      return gracefulComplete(approvals[0]!);
    }
    return goTo(channelWaitStep, input);
  }
}

const channelWaitStep = new ChannelWait();

export class ChannelFlow implements Flow<number> {
  public getFlowType(): string {
    return "ChannelFlow";
  }

  public getSteps() {
    return StepList.startStep(channelWaitStep);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [approval] };
  }

  @rpc()
  public approve(context: Context): void {
    approval.publish(context, "approved");
  }
}

export const channelFlow = new ChannelFlow();
