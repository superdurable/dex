// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

use std::collections::{HashMap, HashSet};
use std::error::Error;
use std::fmt::{Display, Formatter};
use std::sync::{Mutex, MutexGuard};
use std::time::{Duration, Instant};

use serde::{Deserialize, Serialize};

const FAKE_JOB_DURATION: Duration = Duration::from_secs(6);

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum JobState {
    Queued,
    Running,
    Succeeded,
    Failed,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct JobStatus {
    #[serde(rename = "jobId")]
    pub job_id: String,
    pub state: JobState,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub result: String,
}

#[derive(Debug)]
pub struct JobServiceError {
    message: String,
}

impl Display for JobServiceError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        formatter.write_str(&self.message)
    }
}

impl Error for JobServiceError {}

/// FakeJobService stands in for an external job API, such as a cloud build service.
#[derive(Default)]
pub struct FakeJobService {
    records: Mutex<JobRecords>,
}

#[derive(Default)]
struct JobRecords {
    started_at: HashMap<String, Instant>,
    completed_job_ids: HashSet<String>,
    kept_running_job_ids: HashSet<String>,
    pending_check_failures: HashMap<String, u32>,
    status_checks: HashMap<String, u32>,
}

impl FakeJobService {
    /// Starts a job idempotently per job ID, so a retried Step does not start a second job.
    pub fn start_job(&self, job_id: &str, _timeout: Duration) -> Result<(), JobServiceError> {
        self.lock_records()
            .started_at
            .entry(job_id.to_string())
            .or_insert_with(Instant::now);
        Ok(())
    }

    pub fn get_job_status(
        &self,
        job_id: &str,
        _timeout: Duration,
    ) -> Result<JobStatus, JobServiceError> {
        let mut records = self.lock_records();
        *records.status_checks.entry(job_id.to_string()).or_default() += 1;
        if let Some(pending_failures) = records.pending_check_failures.get_mut(job_id)
            && *pending_failures > 0
        {
            *pending_failures -= 1;
            return Err(JobServiceError {
                message: format!("job service is unavailable for job {job_id}"),
            });
        }
        let started_at = records
            .started_at
            .get(job_id)
            .ok_or_else(|| job_not_found(job_id))?;
        let elapsed = started_at.elapsed();
        let finished_on_its_own =
            !records.kept_running_job_ids.contains(job_id) && elapsed >= FAKE_JOB_DURATION;
        let (state, result) = if records.completed_job_ids.contains(job_id) || finished_on_its_own {
            (JobState::Succeeded, format!("artifact for {job_id}"))
        } else if elapsed >= FAKE_JOB_DURATION / 3 {
            (JobState::Running, String::new())
        } else {
            (JobState::Queued, String::new())
        };
        Ok(JobStatus {
            job_id: job_id.to_string(),
            state,
            result,
        })
    }

    /// Finishes a job early so callers do not wait for its full duration.
    pub fn complete_job(&self, job_id: &str) -> Result<(), JobServiceError> {
        let mut records = self.lock_records();
        if !records.started_at.contains_key(job_id) {
            return Err(job_not_found(job_id));
        }
        records.completed_job_ids.insert(job_id.to_string());
        Ok(())
    }

    /// Keeps a job unfinished until `complete_job`. Call it before the job starts.
    #[cfg(any(test, feature = "test-util"))]
    pub fn keep_job_running_for_test(&self, job_id: &str) {
        self.lock_records()
            .kept_running_job_ids
            .insert(job_id.to_string());
    }

    /// Makes the next `count` status checks of a job fail with a transient error.
    #[cfg(any(test, feature = "test-util"))]
    pub fn fail_next_status_checks_for_test(&self, job_id: &str, count: u32) {
        *self
            .lock_records()
            .pending_check_failures
            .entry(job_id.to_string())
            .or_default() += count;
    }

    /// Returns how many status checks of a job ran, including failed ones.
    #[cfg(any(test, feature = "test-util"))]
    pub fn count_status_checks_for_test(&self, job_id: &str) -> u32 {
        self.lock_records()
            .status_checks
            .get(job_id)
            .copied()
            .unwrap_or_default()
    }

    fn lock_records(&self) -> MutexGuard<'_, JobRecords> {
        self.records
            .lock()
            .expect("fake job records lock is poisoned")
    }
}

fn job_not_found(job_id: &str) -> JobServiceError {
    JobServiceError {
        message: format!("job {job_id} not found"),
    }
}
