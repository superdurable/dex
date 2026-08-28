/*
 * Copyright (c) 2026 Super Durable, Inc.
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
  StepList,
  Stream,
  gracefulComplete,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

export const progress = new Stream("Progress", stringCodec, 10 * 1024 * 1024);

class RenderPreview implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "RenderPreview";
  }

  public async execute(context: Context, input: string): Promise<StepDecision> {
    await progress.write(context, `Rendering preview for ${input}`);
    return gracefulComplete(`Rendered ${input}`);
  }
}

const renderPreview = new RenderPreview();

export class StreamFlow implements Flow<string> {
  public getFlowType(): string {
    return "StreamFlow";
  }

  public getSteps() {
    return StepList.startStep(renderPreview);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { streams: [progress] };
  }
}

export const streamFlow = new StreamFlow();
