// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

import {
  Attribute,
  IndexType,
  StepList,
  Wait,
  gracefulComplete,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "../../src/index.js";

export const KEYWORD_KEY = "CustomKeywordField";

class IndexStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly flow: SearchFlowsFlow) {}

  public getStepType(): string {
    return "IndexStep";
  }

  public waitFor(): Wait {
    return Wait.skipImmediately();
  }

  public execute(context: Context, input: string): StepDecision {
    this.flow.keyword.set(context, input);
    return gracefulComplete(input);
  }
}

export class SearchFlowsFlow implements Flow<string> {
  public readonly keyword = new Attribute(KEYWORD_KEY, stringCodec, {
    type: IndexType.KEYWORD,
  });
  private readonly start = new IndexStep(this);

  public getFlowType(): string {
    return "SearchFlowsFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [this.keyword] };
  }
}
