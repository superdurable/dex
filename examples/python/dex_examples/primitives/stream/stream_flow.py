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

"""Best-effort Stream writes from a Step and Client."""

from __future__ import annotations

from dex import AsyncContext, Flow, PersistenceSchema, Step, StepDecision, StepList, Stream
from dex import graceful_complete


class RenderPreview(Step[str]):
    def __init__(self, progress: Stream[str]) -> None:
        self.progress = progress

    async def execute(self, context: AsyncContext, input: str) -> StepDecision:
        progress = self.progress.buffered_text(context)
        progress.write(f"Rendering preview for {input}")
        progress.flush()
        progress.write(f"Preview ready for {input}")
        return graceful_complete(f"Rendered {input}")


class StreamFlow(Flow[str]):
    progress = Stream("Progress", str, 10 * 1024 * 1024)

    def __init__(self) -> None:
        self.render_preview = RenderPreview(self.progress)

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.render_preview)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.progress)
