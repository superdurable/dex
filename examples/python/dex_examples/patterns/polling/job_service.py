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

from __future__ import annotations

import threading
import time
from dataclasses import dataclass
from enum import StrEnum

FAKE_JOB_DURATION_SECONDS = 6.0


class JobState(StrEnum):
    QUEUED = "QUEUED"
    RUNNING = "RUNNING"
    SUCCEEDED = "SUCCEEDED"
    FAILED = "FAILED"


@dataclass(frozen=True)
class JobStatus:
    job_id: str
    state: JobState
    result: str = ""


class FakeJobService:
    """Stands in for an external job API, such as a cloud build service."""

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._started_at: dict[str, float] = {}
        self._completed_ids: set[str] = set()
        self._kept_running_ids: set[str] = set()
        self._pending_check_failures: dict[str, int] = {}
        self._status_checks: dict[str, int] = {}

    async def start_job(self, job_id: str) -> None:
        """Starts a job once per job ID, so a retried Step does not start a second job."""
        with self._lock:
            self._started_at.setdefault(job_id, time.monotonic())

    async def get_job_status(self, job_id: str) -> JobStatus:
        with self._lock:
            self._status_checks[job_id] = self._status_checks.get(job_id, 0) + 1
            if self._pending_check_failures.get(job_id, 0) > 0:
                self._pending_check_failures[job_id] -= 1
                raise ConnectionError(f"job service is unavailable for job {job_id}")
            started_at = self._started_at.get(job_id)
            if started_at is None:
                raise LookupError(f"job {job_id} not found")
            elapsed = time.monotonic() - started_at
            finished_on_its_own = (
                job_id not in self._kept_running_ids and elapsed >= FAKE_JOB_DURATION_SECONDS
            )
            if job_id in self._completed_ids or finished_on_its_own:
                return JobStatus(job_id, JobState.SUCCEEDED, f"artifact for {job_id}")
            if elapsed >= FAKE_JOB_DURATION_SECONDS / 3:
                return JobStatus(job_id, JobState.RUNNING)
            return JobStatus(job_id, JobState.QUEUED)

    def complete_job(self, job_id: str) -> None:
        """Finishes a job early so callers do not wait for its full duration."""
        with self._lock:
            if job_id not in self._started_at:
                raise LookupError(f"job {job_id} not found")
            self._completed_ids.add(job_id)

    def keep_job_running(self, job_id: str) -> None:
        """Keeps a job unfinished until complete_job. Call it before the job starts."""
        with self._lock:
            self._kept_running_ids.add(job_id)

    def fail_next_status_checks(self, job_id: str, count: int) -> None:
        """Makes the next count status checks of a job fail with a transient error."""
        with self._lock:
            self._pending_check_failures[job_id] = (
                self._pending_check_failures.get(job_id, 0) + count
            )

    def count_status_checks(self, job_id: str) -> int:
        """Returns how many status checks of a job ran, including failed ones."""
        with self._lock:
            return self._status_checks.get(job_id, 0)
