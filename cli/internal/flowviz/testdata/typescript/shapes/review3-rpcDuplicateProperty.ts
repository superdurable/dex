// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {Attribute,Channel,StepList,StepMovement,Wait,rpc,stringCodec,gracefulComplete,type Context,type Flow,type Step,type StepDecision,type RPCResult} from "@superdurable/dex";
const first = new Channel("First",stringCodec);
const second = new Channel("Second",stringCodec);

class Work implements Step<void>{getStepType(){return "Work";} waitFor(context:Context){return Wait.skipImmediately();} execute(context:Context):StepDecision{;return gracefulComplete();}}
export class ReviewFlow implements Flow<void>{private readonly work=new Work();getFlowType(){return "ReviewFlow";}getSteps(){return StepList.startStep<void>(this.work);}getPersistenceSchema(){return {channels:[first,second]};}@rpc() describe(context:Context):RPCResult<void>{return {nextSteps:[StepMovement.of(Work)], ["nextSteps"]:[]};}}
export const reviewFlow=new ReviewFlow();