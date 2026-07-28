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
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
	"time"
)

type persistenceImpl struct {
	encoder ObjectEncoder

	// for data attributes
	dataAttributesKeyMap   map[string]bool
	currentDataAttributes  map[string]dexpb.EncodedObject
	dataAttributesToReturn map[string]dexpb.EncodedObject

	// for stateExecutionLocals
	recordedEvents        map[string]dexpb.EncodedObject
	currentStateExeLocal  map[string]dexpb.EncodedObject
	stateExeLocalToReturn map[string]dexpb.EncodedObject

	// for search attributes
	saKeyToType          map[string]dexpb.SearchAttributeValueType
	saCurrentIntValue    map[string]int64
	saIntToReturn        map[string]int64
	saCurrentStringValue map[string]string
	saStringToReturn     map[string]string
	saCurrentDoubleValue map[string]float64
	saDoubleToReturn     map[string]float64
	saCurrentBoolValue   map[string]bool
	saBoolToReturn       map[string]bool
	saCurrentStrArrValue map[string][]string
	saStrArrToReturn     map[string][]string
}

func newPersistence(
	encoder ObjectEncoder, dataAttrsKeyMap map[string]bool, saKeyToType map[string]dexpb.SearchAttributeValueType,
	dataObjects []dexpb.KeyValue, searchAttributes []dexpb.SearchAttribute, stateLocals []dexpb.KeyValue,
) (Persistence, error) {
	currentDataObjects := make(map[string]dexpb.EncodedObject)
	currentStateLocal := make(map[string]dexpb.EncodedObject)
	saCurrentIntValue := make(map[string]int64)
	saCurrentStringValue := make(map[string]string)
	saCurrentDoubleValue := make(map[string]float64)
	saCurrentBoolValue := make(map[string]bool)
	saCurrentStrArrValue := make(map[string][]string)

	for _, do := range dataObjects {
		currentDataObjects[do.GetKey()] = do.GetValue()
	}

	for _, sl := range stateLocals {
		currentStateLocal[sl.GetKey()] = sl.GetValue()
	}

	for _, sa := range searchAttributes {
		switch sa.GetValueType() {
		case dexpb.KEYWORD, dexpb.DATETIME, dexpb.TEXT:
			saCurrentStringValue[sa.GetKey()] = sa.GetStringValue()
		case dexpb.BOOL:
			saCurrentBoolValue[sa.GetKey()] = sa.GetBoolValue()
		case dexpb.DOUBLE:
			saCurrentDoubleValue[sa.GetKey()] = sa.GetDoubleValue()
		case dexpb.KEYWORD_ARRAY:
			saCurrentStrArrValue[sa.GetKey()] = sa.GetStringArrayValue()
		case dexpb.INT:
			saCurrentIntValue[sa.GetKey()] = sa.GetIntegerValue()
		default:
			return nil, newInternalError("invalid search attribute type %v for key %v ", sa.GetValueType(), sa.GetKey())
		}
	}

	return &persistenceImpl{
		encoder:                encoder,
		dataAttributesKeyMap:   dataAttrsKeyMap,
		currentDataAttributes:  currentDataObjects,
		dataAttributesToReturn: make(map[string]dexpb.EncodedObject),
		recordedEvents:         make(map[string]dexpb.EncodedObject),
		currentStateExeLocal:   currentStateLocal,
		stateExeLocalToReturn:  make(map[string]dexpb.EncodedObject),
		saKeyToType:            saKeyToType,
		saCurrentIntValue:      saCurrentIntValue,
		saIntToReturn:          make(map[string]int64),
		saCurrentStringValue:   saCurrentStringValue,
		saStringToReturn:       make(map[string]string),
		saCurrentDoubleValue:   saCurrentDoubleValue,
		saDoubleToReturn:       make(map[string]float64),
		saCurrentBoolValue:     saCurrentBoolValue,
		saBoolToReturn:         make(map[string]bool),
		saCurrentStrArrValue:   saCurrentStrArrValue,
		saStrArrToReturn:       make(map[string][]string),
	}, nil
}

func (p *persistenceImpl) GetDataAttribute(key string, valuePtr interface{}) {
	if !p.dataAttributesKeyMap[key] {
		panic(NewWorkflowDefinitionErrorFmt("key %v is not registered as a data object", key))
	}
	err := p.encoder.Decode(ptr.Any(p.currentDataAttributes[key]), valuePtr)
	if err != nil {
		panic(err)
	}
}

func (p *persistenceImpl) SetDataAttribute(key string, value interface{}) {
	if !p.dataAttributesKeyMap[key] {
		panic(NewWorkflowDefinitionErrorFmt("key %v is not registered as a data object", key))
	}
	v, err := p.encoder.Encode(value)
	if err != nil {
		panic(err)
	}
	p.dataAttributesToReturn[key] = *v
	p.currentDataAttributes[key] = *v
}

func (p *persistenceImpl) GetSearchAttributeInt(key string) int64 {
	if p.saKeyToType[key] != dexpb.INT {
		panic(NewWorkflowDefinitionErrorFmt("key %v is not registered as a INT search attribute", key))
	}
	return p.saCurrentIntValue[key]
}

func (p *persistenceImpl) SetSearchAttributeInt(key string, value int64) {
	if p.saKeyToType[key] != dexpb.INT {
		panic(NewWorkflowDefinitionErrorFmt("key %v is not registered as a INT search attribute", key))
	}
	p.saCurrentIntValue[key] = value
	p.saIntToReturn[key] = value
}

func (p *persistenceImpl) GetSearchAttributeKeyword(key string) string {
	if p.saKeyToType[key] != dexpb.KEYWORD {
		panic(NewWorkflowDefinitionErrorFmt("key %v is not registered as a KEYWORD search attribute", key))
	}
	return p.saCurrentStringValue[key]
}

func (p *persistenceImpl) SetSearchAttributeKeyword(key string, value string) {
	if p.saKeyToType[key] != dexpb.KEYWORD {
		panic(NewWorkflowDefinitionErrorFmt("key %v is not registered as a KEYWORD search attribute", key))
	}
	p.saCurrentStringValue[key] = value
	p.saStringToReturn[key] = value
}

func (p *persistenceImpl) GetSearchAttributeBool(key string) bool {
	if p.saKeyToType[key] != dexpb.BOOL {
		panic(NewWorkflowDefinitionErrorFmt("key %v is not registered as a BOOL search attribute", key))
	}
	return p.saCurrentBoolValue[key]
}

func (p *persistenceImpl) SetSearchAttributeBool(key string, value bool) {
	if p.saKeyToType[key] != dexpb.BOOL {
		panic(NewWorkflowDefinitionErrorFmt("key %v is not registered as a BOOL search attribute", key))
	}
	p.saBoolToReturn[key] = value
	p.saCurrentBoolValue[key] = value
}

func (p *persistenceImpl) GetSearchAttributeDouble(key string) float64 {
	if p.saKeyToType[key] != dexpb.DOUBLE {
		panic(NewWorkflowDefinitionErrorFmt("key %v is not registered as a DOUBLE search attribute", key))
	}
	return p.saCurrentDoubleValue[key]
}

func (p *persistenceImpl) SetSearchAttributeDouble(key string, value float64) {
	if p.saKeyToType[key] != dexpb.DOUBLE {
		panic(NewWorkflowDefinitionErrorFmt("key %v is not registered as a DOUBLE search attribute", key))
	}
	p.saCurrentDoubleValue[key] = value
	p.saDoubleToReturn[key] = value
}

func (p *persistenceImpl) GetSearchAttributeText(key string) string {
	if p.saKeyToType[key] != dexpb.TEXT {
		panic(NewWorkflowDefinitionErrorFmt("key %v is not registered as a TEXT search attribute", key))
	}
	return p.saCurrentStringValue[key]
}

func (p *persistenceImpl) SetSearchAttributeText(key string, value string) {
	if p.saKeyToType[key] != dexpb.TEXT {
		panic(NewWorkflowDefinitionErrorFmt("key %v is not registered as a TEXT search attribute", key))
	}
	p.saStringToReturn[key] = value
	p.saCurrentStringValue[key] = value
}

func (p *persistenceImpl) GetSearchAttributeDatetime(key string) time.Time {
	if p.saKeyToType[key] != dexpb.DATETIME {
		panic(NewWorkflowDefinitionErrorFmt("key %v is not registered as a DATETIME search attribute", key))
	}
	d, err := time.Parse(DateTimeFormat, p.saCurrentStringValue[key])
	if err != nil {
		panic(err)
	}
	return d
}

func (p *persistenceImpl) SetSearchAttributeDatetime(key string, value time.Time) {
	if p.saKeyToType[key] != dexpb.DATETIME {
		panic(NewWorkflowDefinitionErrorFmt("key %v is not registered as a DATETIME search attribute", key))
	}

	v := value.Format(DateTimeFormat)
	p.saCurrentStringValue[key] = v
	p.saStringToReturn[key] = v
}

func (p *persistenceImpl) GetSearchAttributeKeywordArray(key string) []string {
	if p.saKeyToType[key] != dexpb.KEYWORD_ARRAY {
		panic(NewWorkflowDefinitionErrorFmt("key %v is not registered as a KEYWORD_ARRAY search attribute", key))
	}
	return p.saCurrentStrArrValue[key]
}

func (p *persistenceImpl) SetSearchAttributeKeywordArray(key string, value []string) {
	if p.saKeyToType[key] != dexpb.KEYWORD_ARRAY {
		panic(NewWorkflowDefinitionErrorFmt("key %v is not registered as a KEYWORD_ARRAY search attribute", key))
	}
	p.saCurrentStrArrValue[key] = value
	p.saStrArrToReturn[key] = value
}

func (p *persistenceImpl) GetStateExecutionLocal(key string, valuePtr interface{}) {
	err := p.encoder.Decode(ptr.Any(p.currentStateExeLocal[key]), valuePtr)
	if err != nil {
		panic(err)
	}
}

func (p *persistenceImpl) SetStateExecutionLocal(key string, value interface{}) {
	v, err := p.encoder.Encode(value)
	if err != nil {
		panic(err)
	}
	p.currentStateExeLocal[key] = *v
	p.stateExeLocalToReturn[key] = *v
}

func (p *persistenceImpl) RecordEvent(key string, value interface{}) {
	v, err := p.encoder.Encode(value)
	if err != nil {
		panic(err)
	}
	p.recordedEvents[key] = *v
}

func (p *persistenceImpl) GetToReturn() (
	dataObjectsToReturn []dexpb.KeyValue,
	stateLocalToReturn []dexpb.KeyValue,
	recordEvents []dexpb.KeyValue,
	searchAttributes []dexpb.SearchAttribute) {
	for k, v := range p.dataAttributesToReturn {
		dataObjectsToReturn = append(dataObjectsToReturn, dexpb.KeyValue{
			Key:   ptr.Any(k),
			Value: ptr.Any(v),
		})
	}

	for k, v := range p.stateExeLocalToReturn {
		stateLocalToReturn = append(stateLocalToReturn, dexpb.KeyValue{
			Key:   ptr.Any(k),
			Value: ptr.Any(v),
		})
	}

	for k, v := range p.recordedEvents {
		recordEvents = append(recordEvents, dexpb.KeyValue{
			Key:   ptr.Any(k),
			Value: ptr.Any(v),
		})
	}
	for k, sa := range p.saIntToReturn {
		searchAttributes = append(searchAttributes, dexpb.SearchAttribute{
			Key:          ptr.Any(k),
			ValueType:    ptr.Any(p.saKeyToType[k]),
			IntegerValue: ptr.Any(sa),
		})
	}
	for k, sa := range p.saStringToReturn {
		searchAttributes = append(searchAttributes, dexpb.SearchAttribute{
			Key:         ptr.Any(k),
			ValueType:   ptr.Any(p.saKeyToType[k]),
			StringValue: ptr.Any(sa),
		})
	}
	for k, sa := range p.saDoubleToReturn {
		searchAttributes = append(searchAttributes, dexpb.SearchAttribute{
			Key:         ptr.Any(k),
			ValueType:   ptr.Any(p.saKeyToType[k]),
			DoubleValue: ptr.Any(sa),
		})
	}
	for k, sa := range p.saBoolToReturn {
		searchAttributes = append(searchAttributes, dexpb.SearchAttribute{
			Key:       ptr.Any(k),
			ValueType: ptr.Any(p.saKeyToType[k]),
			BoolValue: ptr.Any(sa),
		})
	}
	for k, sa := range p.saStrArrToReturn {
		searchAttributes = append(searchAttributes, dexpb.SearchAttribute{
			Key:              ptr.Any(k),
			ValueType:        ptr.Any(p.saKeyToType[k]),
			StringArrayValue: sa,
		})
	}
	return
}
