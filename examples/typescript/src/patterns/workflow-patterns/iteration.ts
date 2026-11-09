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

// Iteration Workflow Patterns: WCP-10 (arbitrary cycles), WCP-21 (structured
// loop), and WCP-22 (recursion).

import {
  StepList,
  SubFlow,
  Wait,
  doubleCodec,
  goTo,
  gracefulComplete,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

// WCP-10 Arbitrary Cycles: the Draft and Edit cycle has two entry points.

export interface ArticleSubmission {
  readonly hasDraft: boolean;
}

export interface ArticleRevision {
  readonly revision: number;
  readonly trail: string[];
}

const LAST_ARTICLE_REVISION = 1;

class RouteSubmission implements Step<ArticleSubmission> {
  public getStepType(): string {
    return "RouteSubmission";
  }

  public execute(_context: Context, submission: ArticleSubmission): StepDecision {
    const revision: ArticleRevision = { revision: 0, trail: ["RouteSubmission"] };
    if (submission.hasDraft) {
      return goTo(EditArticle, revision);
    }
    return goTo(DraftArticle, revision);
  }
}

class DraftArticle implements Step<ArticleRevision> {
  public getStepType(): string {
    return "DraftArticle";
  }

  public execute(_context: Context, article: ArticleRevision): StepDecision {
    return goTo(EditArticle, { revision: article.revision, trail: [...article.trail, "DraftArticle"] });
  }
}

class EditArticle implements Step<ArticleRevision> {
  public getStepType(): string {
    return "EditArticle";
  }

  public execute(_context: Context, article: ArticleRevision): StepDecision {
    const trail = [...article.trail, "EditArticle"];
    if (article.revision >= LAST_ARTICLE_REVISION) {
      return goTo(PublishArticle, trail);
    }
    return goTo(DraftArticle, { revision: article.revision + 1, trail });
  }
}

class PublishArticle implements Step<string[]> {
  public getStepType(): string {
    return "PublishArticle";
  }

  public execute(_context: Context, trail: string[]): StepDecision {
    return gracefulComplete([...trail, "PublishArticle"]);
  }
}

export class ArbitraryCyclesFlow implements Flow<ArticleSubmission> {
  private readonly routeSubmission = new RouteSubmission();
  private readonly draftArticle = new DraftArticle();
  private readonly editArticle = new EditArticle();
  private readonly publishArticle = new PublishArticle();

  public getFlowType(): string {
    return "ArbitraryCyclesFlow";
  }

  public getSteps(): StepList<ArticleSubmission> {
    return StepList.startStep(this.routeSubmission).otherSteps(
      this.draftArticle,
      this.editArticle,
      this.publishArticle,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

// WCP-21 Structured Loop: ProcessBatches tests its condition before each pass
// and goes to itself until no batch remains.

export interface BatchProgress {
  readonly remainingBatches: number;
  readonly processedBatches: number;
}

class ProcessBatches implements Step<BatchProgress> {
  public getStepType(): string {
    return "ProcessBatches";
  }

  public execute(_context: Context, progress: BatchProgress): StepDecision {
    if (progress.remainingBatches === 0) {
      return goTo(FinishBatches, progress.processedBatches);
    }
    return goTo(ProcessBatches, {
      remainingBatches: progress.remainingBatches - 1,
      processedBatches: progress.processedBatches + 1,
    });
  }
}

class FinishBatches implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "FinishBatches";
  }

  public execute(_context: Context, processedBatches: number): StepDecision {
    return gracefulComplete(processedBatches);
  }
}

export class StructuredLoopFlow implements Flow<BatchProgress> {
  private readonly processBatches = new ProcessBatches();
  private readonly finishBatches = new FinishBatches();

  public getFlowType(): string {
    return "StructuredLoopFlow";
  }

  public getSteps(): StepList<BatchProgress> {
    return StepList.startStep(this.processBatches).otherSteps(this.finishBatches);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

// WCP-22 Recursion: ComputeFactorial waits for a SubFlow of its own Flow type.

class ComputeFactorial implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(private readonly factorialRecursionFlow: FactorialRecursionFlow) {}

  public getStepType(): string {
    return "ComputeFactorial";
  }

  public waitFor(_context: Context, n: number): Wait {
    if (n <= 1) {
      return Wait.skipImmediately();
    }
    return Wait.until(SubFlow.run(this.factorialRecursionFlow, n - 1));
  }

  public execute(context: Context, n: number): StepDecision {
    if (n <= 1) {
      return gracefulComplete(1);
    }
    return gracefulComplete(n * SubFlow.getConditionResults(context).singleOutput(doubleCodec));
  }
}

export class FactorialRecursionFlow implements Flow<number> {
  private readonly computeFactorial = new ComputeFactorial(this);

  public getFlowType(): string {
    return "FactorialRecursionFlow";
  }

  public getSteps(): StepList<number> {
    return StepList.startStep(this.computeFactorial);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

export const arbitraryCyclesFlow = new ArbitraryCyclesFlow();
export const structuredLoopFlow = new StructuredLoopFlow();
export const factorialRecursionFlow = new FactorialRecursionFlow();
