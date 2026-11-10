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

import { setTimeout as sleep } from "node:timers/promises";

import {
  Channel,
  ExecuteFailure,
  StepList,
  StepMovement,
  Wait,
  deadEnd,
  doubleCodec,
  goTo,
  goToMany,
  gracefulComplete,
  jsonCodec,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
  type StepOptions,
} from "@superdurable/dex";

const CONTRACT_EDIT_DURATION_MS = 100;

/**
 * One reviewer's turn to edit the shared contract.
 * The should* fields simulate a failure or a withdrawal for the example.
 */
export interface ContractEdit {
  readonly reviewer: string;
  readonly shouldFailHandoff?: boolean;
  readonly shouldWithdraw?: boolean;
  readonly shouldFailEdit?: boolean;
  readonly startedAtMillis?: number;
}

/** Reports when one edit held the contract and how it ended. */
export interface ContractEditRecord {
  readonly reviewer: string;
  readonly outcome: string;
  readonly startedAtMillis: number;
  readonly finishedAtMillis: number;
}

export const contractEditToken = new Channel("contractEditToken", stringCodec);
export const contractEditFinished = new Channel(
  "contractEditFinished",
  jsonCodec<ContractEditRecord>({ typeName: "ContractEditRecord" }),
);

/** Publishes the only token and starts one branch per reviewer. */
class StartContractEdits implements Step<ContractEdit[]> {
  public getStepType(): string {
    return "StartContractEdits";
  }

  public execute(context: Context, edits: ContractEdit[]): StepDecision {
    contractEditToken.publish(context, "contract");
    return goToMany(
      ...edits.map((edit) => StepMovement.of(EnterContractEdit, edit)),
      StepMovement.of(FinishContractEdits, edits.length),
    );
  }
}

/**
 * Takes the token and hands off; it does no protected work, so canceling it
 * is safe.
 */
class EnterContractEdit implements Step<ContractEdit> {
  public getStepType(): string {
    return "EnterContractEdit";
  }

  public getStepOptions(): StepOptions {
    return {
      executeRetry: { initialIntervalMs: 1_000, maximumAttempts: 2 },
      executeFailure: ExecuteFailure.proceedTo(ReleaseContractEditToken),
    };
  }

  public waitFor(_context: Context, _edit: ContractEdit): Wait {
    return Wait.until(contractEditToken.forOne());
  }

  public execute(_context: Context, edit: ContractEdit): StepDecision {
    if (edit.shouldFailHandoff) {
      throw new Error("contract handoff failed");
    }
    return goTo(EditContract, edit);
  }
}

/** The first Step that works on the contract. Never cancel it. */
class EditContract implements Step<ContractEdit> {
  public getStepType(): string {
    return "EditContract";
  }

  public getStepOptions(): StepOptions {
    return {
      executeRetry: { initialIntervalMs: 1_000, maximumAttempts: 2 },
      executeFailure: ExecuteFailure.proceedTo(ReleaseContractEditToken),
    };
  }

  public async execute(context: Context, edit: ContractEdit): Promise<StepDecision> {
    const startedEdit = { ...edit, startedAtMillis: Date.now() };
    if (startedEdit.shouldWithdraw) {
      leaveContractEdit(context, startedEdit, "withdrawn");
      return deadEnd();
    }
    await sleep(CONTRACT_EDIT_DURATION_MS);
    return goTo(SaveContractEdit, startedEdit);
  }
}

/** The last Step of the section; it publishes the token back. */
class SaveContractEdit implements Step<ContractEdit> {
  public getStepType(): string {
    return "SaveContractEdit";
  }

  public getStepOptions(): StepOptions {
    return {
      executeRetry: { initialIntervalMs: 1_000, maximumAttempts: 2 },
      executeFailure: ExecuteFailure.proceedTo(ReleaseContractEditToken),
    };
  }

  public async execute(context: Context, edit: ContractEdit): Promise<StepDecision> {
    await sleep(CONTRACT_EDIT_DURATION_MS);
    if (edit.shouldFailEdit) {
      throw new Error("contract edit failed");
    }
    leaveContractEdit(context, edit, "saved");
    return deadEnd();
  }
}

/** The failure exit of the section. */
class ReleaseContractEditToken implements Step<ContractEdit> {
  public getStepType(): string {
    return "ReleaseContractEditToken";
  }

  public execute(context: Context, edit: ContractEdit): StepDecision {
    leaveContractEdit(context, edit, "failed");
    return deadEnd();
  }
}

/** Joins the branches after every reviewer leaves the section. */
class FinishContractEdits implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "FinishContractEdits";
  }

  public waitFor(_context: Context, editCount: number): Wait {
    return Wait.until(contractEditFinished.forN(editCount));
  }

  public execute(context: Context, _editCount: number): StepDecision {
    return gracefulComplete(contractEditFinished.results(context));
  }
}

/**
 * Records the edit and publishes the token for the next reviewer.
 * An edit that never started has an empty interval at its finish time.
 */
function leaveContractEdit(context: Context, edit: ContractEdit, outcome: string): void {
  const finishedAtMillis = Date.now();
  contractEditFinished.publish(context, {
    reviewer: edit.reviewer,
    outcome,
    startedAtMillis: edit.startedAtMillis || finishedAtMillis,
    finishedAtMillis,
  });
  contractEditToken.publish(context, "contract");
}

/**
 * Lets several reviewers edit one contract, one at a time.
 * A one-message Channel is the token that guards the edit section.
 */
export class ContractEditFlow implements Flow<ContractEdit[]> {
  public getFlowType(): string {
    return "ContractEditFlow";
  }

  public getSteps(): StepList<ContractEdit[]> {
    return StepList.startStep(new StartContractEdits()).otherSteps(
      new EnterContractEdit(),
      new EditContract(),
      new SaveContractEdit(),
      new ReleaseContractEditToken(),
      new FinishContractEdits(),
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [contractEditToken, contractEditFinished] };
  }
}

export const contractEditFlow = new ContractEditFlow();
