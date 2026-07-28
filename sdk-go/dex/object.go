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

import "github.com/superdurable/dex/sdk-go/gen/dexpb"

// Object is a representation of EncodedObject
type Object struct {
	EncodedObject *dexpb.EncodedObject
	ObjectEncoder ObjectEncoder
}

func NewObject(EncodedObject *dexpb.EncodedObject, ObjectEncoder ObjectEncoder) Object {
	return Object{
		EncodedObject: EncodedObject,
		ObjectEncoder: ObjectEncoder,
	}
}

// Get retrieves the actual object
// It just panics on error but the error can still be accessible if really need to do some customized handling(mostly you don't need to):
// 1. capturing panic yourself
// 2. get the error from WorkerService API, because WorkerService will use captureStateExecutionError to capture the error
func (o Object) Get(resultPtr interface{}) {
	err := o.ObjectEncoder.Decode(o.EncodedObject, resultPtr)
	if err != nil {
		panic(err)
	}
}
