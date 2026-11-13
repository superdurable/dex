// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0


import { Channel, StepList, StepMovement, Stream, goTo, gracefulComplete, rpc, stringCodec, type Context, type Flow, type RPCResult, type Step, type StepDecision } from "@superdurable/dex";
const approvals = new Channel("Approvals", stringCodec);
const alternateApprovals = new Channel("AlternateApprovals", stringCodec);
const progress = new Stream("Progress", stringCodec, 1024);
class InitialStep implements Step<string> {
 getStepType() {return "InitialStep";}
 execute(context: Context, input: string): StepDecision { return goTo(FinalStep, input); }
}
class FinalStep implements Step<string> {
 getStepType() {return "FinalStep";}
 execute(context: Context, input: string): StepDecision {return gracefulComplete(input);}
}
export class ReviewFlow implements Flow<string> {
 private readonly initial = new InitialStep();
 private readonly final = new FinalStep();
 
 getFlowType(){return "ReviewFlow";}
 getSteps(){return StepList.startStep(this.initial).otherSteps(this.final);}
 getPersistenceSchema(){return {channels:[approvals,alternateApprovals], streams:[progress]};}
 @rpc({ inputCodec:stringCodec, outputCodec:stringCodec })
 approve(context: Context, decision: string): RPCResult<string> | void { const contextCache = new WeakMap<Context,string>(); contextCache.set(context,decision); return {output:decision}; }
 
}

export const reviewFlow = new ReviewFlow();
