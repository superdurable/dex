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
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
)

var (
	errInvalidInvocationContext = errors.New("dex: invalid invocation context")
	errPhaseNotImplemented      = errors.New("dex: operation is not implemented in phase 1")
)

type Error struct {
	Code                codes.Code
	SubStatus           ErrorSubStatus
	Detail              string
	OriginalWorkerError *WorkerError
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Detail == "" {
		return e.Code.String()
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Detail)
}

type WorkerError struct {
	Code   codes.Code
	Type   string
	Detail string
}

type ErrorSubStatus uint8

const (
	ErrorUncategorized ErrorSubStatus = iota + 1
	ErrorFlowAlreadyStarted
	ErrorFlowNotFound
	ErrorWorkerAPI
	ErrorLongPollTimeout
)
