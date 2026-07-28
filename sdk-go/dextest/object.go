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

package dextest

import "github.com/superdurable/dex/sdk-go/dex"

func NewTestObject(obj interface{}) dex.Object {
	obj2, err := dex.GetDefaultObjectEncoder().Encode(obj)
	if err != nil {
		panic(err)
	}
	return dex.NewObject(obj2, dex.GetDefaultObjectEncoder())
}

func NewTestObjectWithEncoder(obj interface{}, encoder dex.ObjectEncoder) dex.Object {
	obj2, err := encoder.Encode(obj)
	if err != nil {
		panic(err)
	}
	return dex.NewObject(obj2, encoder)
}
