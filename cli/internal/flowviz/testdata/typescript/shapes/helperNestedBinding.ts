// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  Attribute,
  Channel,
  ChannelMap,
  ConditionCombination,
  StepList,
  StepMovement,
  Stream,
  SubFlow,
  Timer,
  Wait,
  forceFail,
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
import { reviewFlow } from "./review-flow.js";

const approvals = new Channel("Approvals", stringCodec);
const buckets = new ChannelMap("Buckets", stringCodec);
const status = new Attribute("Status", stringCodec);
const progress = new Stream("Progress", stringCodec, 1024);
const reminderMs = 5 * 60_000;

interface ApprovalInput {
  readonly useSignal: boolean;
  readonly minutes: number;
}

// dex:group group-id:approval group-label:"Approval"
// dex:explanation text:"Wait for an approval signal or a timer."
class AwaitApproval implements Step<ApprovalInput> {
  getStepType(): string {
    return "AwaitApproval";
  }

  waitFor(context: Context, input: ApprovalInput): Wait {
    if (buckets.size(context, "priority") > 0) {
      return Wait.skipImmediately();
    }
    if (input.useSignal) {
      return Wait.anyOf(approvals.forOne("approval"), Timer.byDuration(2_000, "deadline"));
    }
    return input.minutes > 10
      ? Wait.until(Timer.byDuration(input.minutes * 60_000))
      : Wait.anyCombinationOf(
          ConditionCombination.of(approvals.forN(2, "pair"), Timer.byDuration(reminderMs, "reminder")),
          ConditionCombination.of(buckets.range("priority", 1, undefined, "priority")),
        );
  }

  execute(context: Context, input: ApprovalInput): StepDecision {
    progress.write(context, "approved");
    this.recordStatus(context);
    return goTo(ReviewBounds, input.minutes);
  }

  private recordStatus(context: Context): void {
    status.set(context, approvals.results(context).join(","));
  }
}

// dex:group group-id:approval group-label:"Approval"
// dex:explanation text:"Wait for a bounded number of messages and a review SubFlow."
class ReviewBounds implements Step<number> {
  getStepType(): string {
    return "ReviewBounds";
  }

  waitFor(_context: Context, input: number): Wait {
    return Wait.allOf(
      approvals.atLeast(3),
      approvals.atMost(4),
      approvals.range(2, 5),
      buckets.forN("priority", input),
      SubFlow.run(reviewFlow, input),
    );
  }

  execute(_context: Context, _input: number): StepDecision {
    return gracefulComplete();
  }
}

// dex:group group-id:approval group-label:"Approval"
// dex:explanation text:"Fail its waitFor so execute can recover."
class FailingWait implements Step<string> {
  getStepType(): string {
    return "FailingWait";
  }

  waitFor(_context: Context, _input: string): Wait {
    throw new Error("planned waitFor failure");
  }

  execute(_context: Context, _input: string): StepDecision {
    return gracefulComplete();
  }
}

export class ApprovalFlow implements Flow<ApprovalInput> {
  private readonly approval = new AwaitApproval();
  private readonly bounds = new ReviewBounds();
  private readonly failing = new FailingWait();

  getFlowType(): string {
    return "ApprovalFlow";
  }

  getSteps() {
    return StepList.startStep(this.approval).otherSteps(this.bounds, this.failing);
  }

  getPersistenceSchema(): PersistenceSchema {
    return { attributes: [status], channels: [approvals, buckets], streams: [progress] };
  }

  @rpc({ inputCodec: stringCodec, outputCodec: stringCodec, lockAttributes: [status.lock()] })
  approve(context: Context, decision: string): RPCResult<string> {
    firstPublish(approvals, context, decision);
    if (decision === "skip") {
      return { output: decision, nextSteps: [StepMovement.of(FailingWait, decision)], cancelingSteps: [AwaitApproval] };
    }
    return { output: status.get(context) };
  }

  @rpc({ outputCodec: stringCodec })
  describe(context: Context): RPCResult<string> {
    return this.approve(context, "describe");
  }

  @rpc({ inputCodec: stringCodec })
  note(context: Context, text: string): void {
    buckets.publish(context, "notes", text);
  }

  handleTimeout(context: Context): StepDecision {
    status.set(context, "timed out");
    return forceFail("approval timed out");
  }
}

export const approvalFlow = new ApprovalFlow();

function firstPublish(channel: Channel<string>, context: Context, value: string) { secondPublish(channel, context, value); }
function secondPublish(channel: Channel<string>, context: Context, value: string) { channel.publish(context, value); }