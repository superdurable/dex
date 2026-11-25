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

package entityflow

import (
	"errors"
	"sync"
)

// FakeAccountArchive is an in-memory stand-in for the system that keeps records of closed accounts.
// Archiving is idempotent per account, as a unique insert keyed by the Flow ID would be.
type FakeAccountArchive struct {
	mutex                   sync.Mutex
	unavailableFlowIDs      map[string]bool
	lostResponseFlowIDs     map[string]bool
	archiveRecords          map[string]string
	attemptStepExecutionIDs map[string][]string
}

func NewFakeAccountArchive() *FakeAccountArchive {
	return &FakeAccountArchive{
		unavailableFlowIDs:      map[string]bool{},
		lostResponseFlowIDs:     map[string]bool{},
		archiveRecords:          map[string]string{},
		attemptStepExecutionIDs: map[string][]string{},
	}
}

func (archive *FakeAccountArchive) ArchiveAccount(flowID, stepExecutionID string) error {
	archive.mutex.Lock()
	defer archive.mutex.Unlock()
	archive.attemptStepExecutionIDs[flowID] = append(
		archive.attemptStepExecutionIDs[flowID],
		stepExecutionID,
	)
	if _, archived := archive.archiveRecords[flowID]; archived {
		return nil
	}
	if archive.unavailableFlowIDs[flowID] {
		return errors.New("account archive is unavailable")
	}
	archive.archiveRecords[flowID] = stepExecutionID
	if archive.lostResponseFlowIDs[flowID] {
		delete(archive.lostResponseFlowIDs, flowID)
		return errors.New("account archive response was lost")
	}
	return nil
}

func (archive *FakeAccountArchive) MakeUnavailable(flowID string) {
	archive.mutex.Lock()
	defer archive.mutex.Unlock()
	archive.unavailableFlowIDs[flowID] = true
}

func (archive *FakeAccountArchive) MakeAvailable(flowID string) {
	archive.mutex.Lock()
	defer archive.mutex.Unlock()
	delete(archive.unavailableFlowIDs, flowID)
}

func (archive *FakeAccountArchive) LoseNextResponse(flowID string) {
	archive.mutex.Lock()
	defer archive.mutex.Unlock()
	archive.lostResponseFlowIDs[flowID] = true
}

func (archive *FakeAccountArchive) ArchiveRecordCount(flowID string) int {
	archive.mutex.Lock()
	defer archive.mutex.Unlock()
	if _, archived := archive.archiveRecords[flowID]; archived {
		return 1
	}
	return 0
}

func (archive *FakeAccountArchive) AttemptStepExecutionIDs(flowID string) []string {
	archive.mutex.Lock()
	defer archive.mutex.Unlock()
	return append([]string(nil), archive.attemptStepExecutionIDs[flowID]...)
}
