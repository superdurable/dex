// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package polling

import (
	"context"
	"fmt"
	"sync"
	"time"
)

const fakeJobDuration = 6 * time.Second

type JobState string

const (
	JobQueued    JobState = "QUEUED"
	JobRunning   JobState = "RUNNING"
	JobSucceeded JobState = "SUCCEEDED"
	JobFailed    JobState = "FAILED"
)

type JobStatus struct {
	JobID  string   `json:"jobId"`
	State  JobState `json:"state"`
	Result string   `json:"result,omitempty"`
}

// FakeJobService stands in for an external job API, such as a cloud build service.
type FakeJobService struct {
	mutex        sync.Mutex
	startedAt    map[string]time.Time
	completedIDs map[string]bool
}

func NewFakeJobService() *FakeJobService {
	return &FakeJobService{
		startedAt:    map[string]time.Time{},
		completedIDs: map[string]bool{},
	}
}

// StartJob is idempotent per job ID, so a retried Step does not start a second job.
func (service *FakeJobService) StartJob(ctx context.Context, jobID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	service.mutex.Lock()
	defer service.mutex.Unlock()
	if _, found := service.startedAt[jobID]; !found {
		service.startedAt[jobID] = time.Now()
	}
	return nil
}

func (service *FakeJobService) GetJobStatus(ctx context.Context, jobID string) (JobStatus, error) {
	if err := ctx.Err(); err != nil {
		return JobStatus{}, err
	}
	service.mutex.Lock()
	defer service.mutex.Unlock()
	startedAt, found := service.startedAt[jobID]
	if !found {
		return JobStatus{}, fmt.Errorf("job %s not found", jobID)
	}
	elapsed := time.Since(startedAt)
	switch {
	case service.completedIDs[jobID] || elapsed >= fakeJobDuration:
		return JobStatus{JobID: jobID, State: JobSucceeded, Result: "artifact for " + jobID}, nil
	case elapsed >= fakeJobDuration/3:
		return JobStatus{JobID: jobID, State: JobRunning}, nil
	default:
		return JobStatus{JobID: jobID, State: JobQueued}, nil
	}
}

// CompleteJob finishes a job early so callers do not wait for its full duration.
func (service *FakeJobService) CompleteJob(jobID string) error {
	service.mutex.Lock()
	defer service.mutex.Unlock()
	if _, found := service.startedAt[jobID]; !found {
		return fmt.Errorf("job %s not found", jobID)
	}
	service.completedIDs[jobID] = true
	return nil
}
