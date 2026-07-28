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

package dex

import (
	"fmt"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestCaptureError(t *testing.T) {
	skipCaptureErrorLogging = true
	assertions := assert.New(t)
	err := testExecuteWithError()
	assertions.NotNilf(err, "cannot be nil")
	err = testExecuteWithSuccess()
	assertions.Nil(err, "must be nil")
	err = testExecuteWithPanic()
	assertions.NotNilf(err, "cannot be nil")
}

func testExecuteWithError() (retErr error) {
	defer func() { captureStateExecutionError(recover(), &retErr) }()
	return fmt.Errorf("some error")
}

func testExecuteWithSuccess() (retErr error) {
	defer func() { captureStateExecutionError(recover(), &retErr) }()
	return nil
}

func testExecuteWithPanic() (retErr error) {
	defer func() { captureStateExecutionError(recover(), &retErr) }()
	panic("some panic")
}
