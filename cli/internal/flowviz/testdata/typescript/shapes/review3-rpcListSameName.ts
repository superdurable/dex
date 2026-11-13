// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {Channel,Stream,StepList,StepMovement,Wait,rpc,stringCodec,gracefulComplete,type Context,type Flow,type Step,type StepDecision,type RPCResult} from "@superdurable/dex";
const first=new Channel("First",stringCodec);

class Work implements Step<void>{
 getStepType(){return "Work";}
 waitFor(context:Context,input:void):Wait{return Wait.skipImmediately();}
 execute(context:Context,input:void):StepDecision{;return gracefulComplete();}}
export class ReviewFlow implements Flow<void>{readonly progress=new Stream("Progress",stringCodec,1024);private readonly work=new Work();getFlowType(){return "ReviewFlow";}getSteps(){return StepList.startStep<void>(this.work);}getPersistenceSchema(){return {channels:[first],streams:[this.progress]};}@rpc() describe(context:Context):RPCResult<void>{const nextSteps=[StepMovement.of(Work, void 0)];return {output:void 0,nextSteps:nextSteps};}}
export const reviewFlow=new ReviewFlow();