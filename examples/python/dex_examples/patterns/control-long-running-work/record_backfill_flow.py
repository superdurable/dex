# Copyright (c) 2022-2026 Super Durable, Inc.
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
"""Process records in batches that two control Attributes steer."""

from __future__ import annotations

from dataclasses import dataclass
from datetime import timedelta

from dex import (
    Attribute,
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    Timer,
    Wait,
    go_to,
    graceful_complete,
    rpc,
)

TOTAL_RECORDS = 1000
DEFAULT_BATCH_SIZE = 10
MAX_BATCH_SIZE = 1000
BATCH_INTERVAL = timedelta(seconds=1)

BATCH_SIZE = Attribute("batchSize", int)
STOP_REQUESTED = Attribute("stopRequested", bool)


@dataclass
class BackfillProgress:
    """The range of records that the loop has not processed yet."""

    next_record: int
    last_record: int


# dex:group group-id:start group-label:"Start"
# dex:explanation text:"Start the backfill at the first record."
class StartRecordBackfill(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        return go_to(ProcessRecordBatch, BackfillProgress(1, TOTAL_RECORDS))


# dex:group group-id:batches group-label:"Batches"
# dex:explanation text:"Read the controls, then process one batch of records or stop."
class ProcessRecordBatch(Step[BackfillProgress]):
    def wait_for(self, context: Context, progress: BackfillProgress) -> Wait:
        return Wait.until(Timer.by_duration(BATCH_INTERVAL))

    def execute(self, context: Context, progress: BackfillProgress) -> StepDecision:
        if STOP_REQUESTED.get(context):
            return graceful_complete(f"stopped before record {progress.next_record}")
        # An unset int Attribute reads as 0, and a set batch size is at least 1.
        size = BATCH_SIZE.get(context) or DEFAULT_BATCH_SIZE
        last = min(progress.next_record + size - 1, progress.last_record)
        print(f"processing records {progress.next_record} to {last}")
        if last == progress.last_record:
            return graceful_complete(f"processed {last} records")
        return go_to(ProcessRecordBatch, BackfillProgress(last + 1, progress.last_record))


class RecordBackfillFlow(Flow[None]):
    """Processes records in batches that two control Attributes steer."""

    def __init__(self) -> None:
        self.start_record_backfill = StartRecordBackfill()
        self.process_record_batch = ProcessRecordBatch()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.start_record_backfill).other_steps(
            self.process_record_batch
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(BATCH_SIZE, STOP_REQUESTED)

    @rpc
    def set_batch_size(self, context: Context, size: int) -> None:
        """Changes how many records each later batch processes."""
        if size < 1 or size > MAX_BATCH_SIZE:
            raise ValueError(f"batch size must be between 1 and {MAX_BATCH_SIZE}")
        BATCH_SIZE.set(context, size)

    @rpc
    def request_stop(self, context: Context) -> None:
        """Asks the loop to stop before its next batch."""
        STOP_REQUESTED.set(context, True)
