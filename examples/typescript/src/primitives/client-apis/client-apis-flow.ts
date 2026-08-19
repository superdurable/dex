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
  IndexType,
  StepList,
  gracefulComplete,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

export const KEYWORD_KEY = "CustomKeywordField";

class ClientApisStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly flow: ClientApisFlow) {}

  public getStepType(): string {
    return "ClientApisStep";
  }

  public execute(context: Context, input: string): StepDecision {
    this.flow.keyword.set(context, input);
    return gracefulComplete(input);
  }
}

export class ClientApisFlow implements Flow<string> {
  public readonly keyword = new Attribute(KEYWORD_KEY, stringCodec, {
    type: IndexType.KEYWORD,
  });
  private readonly index = new ClientApisStep(this);

  public getFlowType(): string {
    return "ClientApisFlow";
  }

  public getSteps() {
    return StepList.startStep(this.index);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [this.keyword] };
  }
}

export const clientApisFlow = new ClientApisFlow();
