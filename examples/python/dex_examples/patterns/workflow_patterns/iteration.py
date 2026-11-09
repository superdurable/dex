# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Iteration Workflow Patterns: WCP-10, WCP-21, and WCP-22."""

from __future__ import annotations

from dataclasses import dataclass

from dex import (
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    SubFlow,
    Wait,
    go_to,
    graceful_complete,
)

# WCP-10 Arbitrary Cycles: the Draft and Edit cycle has two entry points.


@dataclass(frozen=True)
class ArticleSubmission:
    has_draft: bool


@dataclass(frozen=True)
class ArticleRevision:
    revision: int
    trail: list[str]


LAST_ARTICLE_REVISION = 1


class RouteSubmission(Step[ArticleSubmission]):
    def execute(self, context: Context, input: ArticleSubmission) -> StepDecision:
        revision = ArticleRevision(0, ["RouteSubmission"])
        if input.has_draft:
            return go_to(EditArticle, revision)
        return go_to(DraftArticle, revision)


class DraftArticle(Step[ArticleRevision]):
    def execute(self, context: Context, input: ArticleRevision) -> StepDecision:
        return go_to(
            EditArticle, ArticleRevision(input.revision, [*input.trail, "DraftArticle"])
        )


class EditArticle(Step[ArticleRevision]):
    def execute(self, context: Context, input: ArticleRevision) -> StepDecision:
        trail = [*input.trail, "EditArticle"]
        if input.revision >= LAST_ARTICLE_REVISION:
            return go_to(PublishArticle, trail)
        return go_to(DraftArticle, ArticleRevision(input.revision + 1, trail))


class PublishArticle(Step[list[str]]):
    def execute(self, context: Context, input: list[str]) -> StepDecision:
        return graceful_complete([*input, "PublishArticle"])


class ArbitraryCyclesFlow(Flow[ArticleSubmission]):
    def __init__(self) -> None:
        self.route_submission = RouteSubmission()
        self.draft_article = DraftArticle()
        self.edit_article = EditArticle()
        self.publish_article = PublishArticle()

    def get_steps(self) -> StepList[ArticleSubmission]:
        return StepList.start_step(self.route_submission).other_steps(
            self.draft_article, self.edit_article, self.publish_article
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of()


# WCP-21 Structured Loop: ProcessBatches tests its condition before each pass.


@dataclass(frozen=True)
class BatchProgress:
    remaining_batches: int
    processed_batches: int


class ProcessBatches(Step[BatchProgress]):
    def execute(self, context: Context, input: BatchProgress) -> StepDecision:
        if input.remaining_batches == 0:
            return go_to(FinishBatches, input.processed_batches)
        return go_to(
            ProcessBatches,
            BatchProgress(input.remaining_batches - 1, input.processed_batches + 1),
        )


class FinishBatches(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        return graceful_complete(input)


class StructuredLoopFlow(Flow[BatchProgress]):
    def __init__(self) -> None:
        self.process_batches = ProcessBatches()
        self.finish_batches = FinishBatches()

    def get_steps(self) -> StepList[BatchProgress]:
        return StepList.start_step(self.process_batches).other_steps(self.finish_batches)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of()


# WCP-22 Recursion: ComputeFactorial waits for a SubFlow of its own Flow type.


class ComputeFactorial(Step[int]):
    def __init__(self, factorial_recursion_flow: FactorialRecursionFlow) -> None:
        self.factorial_recursion_flow = factorial_recursion_flow

    def wait_for(self, context: Context, input: int) -> Wait:
        if input <= 1:
            return Wait.skip_immediately()
        return Wait.until(SubFlow.run(self.factorial_recursion_flow, input - 1))

    def execute(self, context: Context, input: int) -> StepDecision:
        if input <= 1:
            return graceful_complete(1)
        return graceful_complete(
            input * SubFlow.get_condition_results(context).single_output(int)
        )


class FactorialRecursionFlow(Flow[int]):
    def __init__(self) -> None:
        self.compute_factorial = ComputeFactorial(self)

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.compute_factorial)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of()
