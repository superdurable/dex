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

from dataclasses import dataclass, replace

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

# WCP-10 Arbitrary Cycles: the Draft and Edit cycle has two entries and two exits.


@dataclass(frozen=True)
class ArticleSubmission:
    has_draft: bool
    should_abandon: bool


@dataclass(frozen=True)
class ArticleRevision:
    revision: int
    should_abandon: bool
    trail: list[str]


LAST_ARTICLE_REVISION = 1


class RouteSubmission(Step[ArticleSubmission]):
    def execute(self, context: Context, input: ArticleSubmission) -> StepDecision:
        revision = ArticleRevision(0, input.should_abandon, ["RouteSubmission"])
        if input.has_draft:
            return go_to(EditArticle, revision)
        return go_to(DraftArticle, revision)


class DraftArticle(Step[ArticleRevision]):
    def execute(self, context: Context, input: ArticleRevision) -> StepDecision:
        trail = [*input.trail, "DraftArticle"]
        if input.should_abandon and input.revision >= LAST_ARTICLE_REVISION:
            return go_to(AbandonArticle, trail)
        return go_to(EditArticle, replace(input, trail=trail))


class EditArticle(Step[ArticleRevision]):
    def execute(self, context: Context, input: ArticleRevision) -> StepDecision:
        trail = [*input.trail, "EditArticle"]
        if input.revision >= LAST_ARTICLE_REVISION:
            return go_to(PublishArticle, trail)
        return go_to(
            DraftArticle, replace(input, revision=input.revision + 1, trail=trail)
        )


class PublishArticle(Step[list[str]]):
    def execute(self, context: Context, input: list[str]) -> StepDecision:
        return graceful_complete([*input, "PublishArticle"])


class AbandonArticle(Step[list[str]]):
    def execute(self, context: Context, input: list[str]) -> StepDecision:
        return graceful_complete([*input, "AbandonArticle"])


class ArbitraryCyclesFlow(Flow[ArticleSubmission]):
    def __init__(self) -> None:
        self.route_submission = RouteSubmission()
        self.draft_article = DraftArticle()
        self.edit_article = EditArticle()
        self.publish_article = PublishArticle()
        self.abandon_article = AbandonArticle()

    def get_steps(self) -> StepList[ArticleSubmission]:
        return StepList.start_step(self.route_submission).other_steps(
            self.draft_article,
            self.edit_article,
            self.publish_article,
            self.abandon_article,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of()


# WCP-21 Structured Loop: CheckBatches tests before each pass; ProcessBatch is the body.


@dataclass(frozen=True)
class BatchProgress:
    remaining_batches: int
    processed_batches: list[str]


class CheckBatches(Step[BatchProgress]):
    def execute(self, context: Context, input: BatchProgress) -> StepDecision:
        if input.remaining_batches == 0:
            return go_to(FinishBatches, input.processed_batches)
        return go_to(ProcessBatch, input)


class ProcessBatch(Step[BatchProgress]):
    def execute(self, context: Context, input: BatchProgress) -> StepDecision:
        return go_to(
            CheckBatches,
            BatchProgress(
                input.remaining_batches - 1,
                [*input.processed_batches, f"batch-{input.remaining_batches}"],
            ),
        )


class FinishBatches(Step[list[str]]):
    def execute(self, context: Context, input: list[str]) -> StepDecision:
        return graceful_complete(input)


class StructuredLoopFlow(Flow[BatchProgress]):
    def __init__(self) -> None:
        self.check_batches = CheckBatches()
        self.process_batch = ProcessBatch()
        self.finish_batches = FinishBatches()

    def get_steps(self) -> StepList[BatchProgress]:
        return StepList.start_step(self.check_batches).other_steps(
            self.process_batch, self.finish_batches
        )

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
