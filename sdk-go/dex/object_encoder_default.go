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
	"encoding/json"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
)

func GetDefaultObjectEncoder() ObjectEncoder {
	return &builtinJsonEncoder{}
}

type builtinJsonEncoder struct {
}

const encodingType = "builtinGolangJson"

func (b *builtinJsonEncoder) GetEncodingType() string {
	return encodingType
}

func (b *builtinJsonEncoder) Encode(obj interface{}) (*dexpb.EncodedObject, error) {
	if obj == nil {
		return &dexpb.EncodedObject{}, nil
	}
	data, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	return &dexpb.EncodedObject{
		Encoding: ptr.Any(encodingType),
		Data:     ptr.Any(string(data)),
	}, nil
}

func (b *builtinJsonEncoder) Decode(encodedObj *dexpb.EncodedObject, resultPtr interface{}) error {
	if encodedObj == nil || resultPtr == nil || encodedObj.GetData() == "" {
		return nil
	}
	return json.Unmarshal([]byte(encodedObj.GetData()), resultPtr)
}
