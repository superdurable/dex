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
type FakeAccountArchive struct {
	mutex              sync.Mutex
	unavailableFlowIDs map[string]bool
	archiveCounts      map[string]int
}

func NewFakeAccountArchive() *FakeAccountArchive {
	return &FakeAccountArchive{
		unavailableFlowIDs: map[string]bool{},
		archiveCounts:      map[string]int{},
	}
}

func (archive *FakeAccountArchive) ArchiveAccount(flowID string) error {
	archive.mutex.Lock()
	defer archive.mutex.Unlock()
	if archive.unavailableFlowIDs[flowID] {
		return errors.New("account archive is unavailable")
	}
	archive.archiveCounts[flowID]++
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

func (archive *FakeAccountArchive) ArchiveCount(flowID string) int {
	archive.mutex.Lock()
	defer archive.mutex.Unlock()
	return archive.archiveCounts[flowID]
}
