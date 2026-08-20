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
  Attribute,
  Channel,
  IndexType,
  StepList,
  Timer,
  Wait,
  forceComplete,
  goTo,
  int64Codec,
  rpc,
  stringCodec,
  voidCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

import { getClient } from "../../client-holder.js";
import {
  myDependencyService,
  type MyDependencyService,
} from "../../shared/my-dependency-service.js";
import { employerOptInFlow } from "./employer-opt-in-flow.js";
import { type ShortlistInput } from "./shortlist-input.js";
import { isOptedIn } from "./workflow-ids.js";

export const SA_KEY_EMPLOYER_ID = "SHORTLIST_EmployerId";
export const SA_KEY_CANDIDATE_ID = "SHORTLIST_CandidateId";
export const DA_EMAIL_SENT_TIMESTAMP = "SHORTLIST_EmailSentTimestamp";
export const SIGNAL_REVOKE_SHORTLIST = "SHORTLIST_SIGNAL_RevokeShortlist";

const SEND_EMAIL_WAIT_MS = 5 * 60 * 1_000;

export const revokeShortlist = new Channel(SIGNAL_REVOKE_SHORTLIST, voidCodec);

export class ShortlistFlow implements Flow<ShortlistInput> {
  public readonly employerId = new Attribute(SA_KEY_EMPLOYER_ID, stringCodec, {
    type: IndexType.KEYWORD,
    indexKey: "CustomKeywordField",
  });
  public readonly candidateId = new Attribute(SA_KEY_CANDIDATE_ID, stringCodec);
  public readonly emailSentTimestamp = new Attribute(DA_EMAIL_SENT_TIMESTAMP, int64Codec);

  public readonly shortlist = new Shortlist(this);
  public readonly sendEmail = new SendEmail(this);

  public constructor(public readonly service: MyDependencyService = myDependencyService) {}

  public getFlowType(): string {
    return "ShortlistFlow";
  }

  public getSteps() {
    return StepList.startStep(this.shortlist).otherSteps(this.sendEmail);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [this.employerId, this.candidateId, this.emailSentTimestamp],
      channels: [revokeShortlist],
    };
  }

  @rpc({ outputCodec: int64Codec })
  public getEmailSentTimestamp(context: Context): RPCResult<bigint> {
    return { output: this.emailSentTimestamp.get(context) };
  }
}

class Shortlist implements Step<ShortlistInput> {
  public constructor(private readonly flow: ShortlistFlow) {}

  public getStepType(): string {
    return "Shortlist";
  }

  public execute(context: Context, input: ShortlistInput): StepDecision {
    this.flow.employerId.set(context, input.employerId);
    this.flow.candidateId.set(context, input.candidateId);
    this.flow.emailSentTimestamp.set(context, 0n);
    return goTo(this.flow.sendEmail, undefined);
  }
}

class SendEmail implements Step<void> {
  public constructor(private readonly flow: ShortlistFlow) {}

  public getStepType(): string {
    return "SendEmail";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.anyOf(
      Timer.byDuration(SEND_EMAIL_WAIT_MS),
      revokeShortlist.forOne(),
    );
  }

  public async execute(context: Context, _input: void): Promise<StepDecision> {
    const employer = this.flow.employerId.get(context);
    const candidate = this.flow.candidateId.get(context);

    if (revokeShortlist.results(context).length > 0) {
      console.log(`Not sending the email to ${employer}-${candidate} because of revoking`);
      return forceComplete();
    }

    if (!(await isOptedIn(getClient(), employerOptInFlow, employer))) {
      console.log(`Not sending the email to ${employer}-${candidate} because of not opted-in`);
      return forceComplete();
    }

    this.flow.service.sendEmail(
      `${employer}-${candidate}`,
      `Employer ${employer} wants to know more about you`,
      "Hello xxx, ...",
    );

    this.flow.emailSentTimestamp.set(context, BigInt(Date.now()));
    return forceComplete();
  }
}

export const shortlistFlow = new ShortlistFlow();
