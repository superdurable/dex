// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

import {
  Attribute,
  Channel,
  StepList,
  Wait,
  gracefulComplete,
  rpc,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "../../src/index.js";

const label = new Attribute("clone-fork-label", stringCodec);
const release = new Channel("clone-fork-release", stringCodec);

class CloneForkWaitStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "CloneForkWaitStep";
  }

  public waitFor(context: Context, input: string): Wait {
    label.set(context, input);
    return Wait.until(release.forOne());
  }

  public execute(context: Context, _input: string): StepDecision {
    return gracefulComplete(`${label.get(context)}|${release.results(context)[0]}`);
  }
}

export class CloneForkSessionFlow implements Flow<string> {
  public readonly label = label;
  private readonly wait = new CloneForkWaitStep();

  public getFlowType(): string {
    return "CloneForkSessionFlow";
  }

  public getSteps() {
    return StepList.startStep(this.wait);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [label], channels: [release] };
  }

  @rpc({ inputCodec: stringCodec })
  public releaseSession(context: Context, answer: string): void {
    release.publish(context, answer);
  }
}
