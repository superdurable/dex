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

//go:generate mockgen -source=./persistence.go -package=dextest -destination=../dextest/persistence.go

import (
	"time"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

// Persistence APIs are for read/write persistence of workflow
// All API would panic on error but the error --
// if you really need to do some customized handling(mostly you don't need to):
// 1. capturing panic yourself
// 2. get the error from WorkerService API, because WorkerService will use captureStateExecutionError to capture the error
type Persistence interface {
	GetDataAttribute(key string, valuePtr interface{})
	SetDataAttribute(key string, value interface{})

	GetSearchAttributeInt(key string) int64
	SetSearchAttributeInt(key string, value int64)

	GetSearchAttributeKeyword(key string) string
	SetSearchAttributeKeyword(key string, value string)

	GetSearchAttributeBool(key string) bool
	SetSearchAttributeBool(key string, value bool)

	GetSearchAttributeDouble(key string) float64
	SetSearchAttributeDouble(key string, value float64)

	GetSearchAttributeText(key string) string
	SetSearchAttributeText(key string, value string)

	GetSearchAttributeDatetime(key string) time.Time
	SetSearchAttributeDatetime(key string, value time.Time)

	GetSearchAttributeKeywordArray(key string) []string
	SetSearchAttributeKeywordArray(key string, value []string)

	// GetStateExecutionLocal retrieves a local state attribute
	// User code must make sure using the same type for both get and set
	GetStateExecutionLocal(key string, valuePtr interface{})
	// SetStateExecutionLocal sets a local attribute. The scope of the attribute is only within the execution of this state.
	// Usually it's for passing from State WaitUntil API to State Execute API
	// User code must make sure using the same type for both get and set
	SetStateExecutionLocal(key string, value interface{})

	// RecordEvent records an arbitrary event in State WaitUntil/Execute API for debugging/tracking purpose
	//  Name is the name of the event. Within a WaitUntil/Execute API, the same Name cannot be used for more than once.
	//  eventData is the data of the event.
	RecordEvent(key string, value interface{})

	// below is for internal implementation
	persistenceInternal
}

type persistenceInternal interface {
	GetToReturn() (
		dataObjectsToReturn []dexpb.KeyValue,
		stateLocalToReturn []dexpb.KeyValue,
		recordEvents []dexpb.KeyValue,
		searchAttributes []dexpb.SearchAttribute,
	)
}
