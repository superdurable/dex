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

package interpreter

import (
	"sort"

	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/service/common/index"
	"github.com/superdurable/iwf/service/common/utils"
	"github.com/superdurable/iwf/service/interpreter/interfaces"
)

type PersistenceManager struct {
	provider interfaces.WorkflowProvider

	attributes map[string]*iwfpb.Value

	lockedKeys map[string]bool
}

func NewPersistenceManager(
	provider interfaces.WorkflowProvider,
	initialAttributes []*iwfpb.KV,
) *PersistenceManager {
	if provider == nil {
		panic("PersistenceManager requires a WorkflowProvider")
	}

	attributes := make(map[string]*iwfpb.Value, len(initialAttributes))
	for _, attribute := range initialAttributes {
		if utils.IsNullValue(attribute.GetValue()) {
			continue
		}
		attributes[attribute.GetKey()] = attribute.GetValue()
	}

	return &PersistenceManager{
		provider:   provider,
		attributes: attributes,
		// locks will not be carried over during continueAsNew
		lockedKeys: map[string]bool{},
	}
}

func (am *PersistenceManager) GetAttributes(
	request *iwfpb.GetAttributesQueryRequest,
) *iwfpb.GetAttributesQueryResponse {
	if request == nil {
		panic("GetAttributes requires a request")
	}

	keys := request.GetKeys()
	if request.GetAllKeys() {
		keys = sortedAttributeKeys(am.attributes)
	} else {
		keys = sortedUniqueStrings(keys)
	}

	attributes := make([]*iwfpb.KV, 0, len(keys))
	for _, key := range keys {
		value, ok := am.attributes[key]
		if !ok {
			continue
		}
		attributes = append(attributes, &iwfpb.KV{Key: key, Value: value})
	}
	return &iwfpb.GetAttributesQueryResponse{Attributes: attributes}
}

func (am *PersistenceManager) LoadAttributes(
	ctx interfaces.UnifiedContext,
	keysToLock []string,
) ([]*iwfpb.KV, error) {
	if err := am.provider.Await(ctx, func() bool {
		return am.CanLockKeys(keysToLock)
	}); err != nil {
		return nil, err
	}
	for _, key := range keysToLock {
		am.lockedKeys[key] = true
	}
	return am.GetAllAttributes(), nil
}

func (am *PersistenceManager) GetAllAttributes() []*iwfpb.KV {
	attributes := make([]*iwfpb.KV, 0, len(am.attributes))

	// NOTE: using sortedAttributeKeys so that the protobuf snapshot for continueAsNew is stable for pagination
	for _, key := range sortedAttributeKeys(am.attributes) {
		attributes = append(attributes, &iwfpb.KV{Key: key, Value: am.attributes[key]})
	}
	return attributes
}

func (am *PersistenceManager) ApplyAttributeWrites(
	ctx interfaces.UnifiedContext,
	writes []*iwfpb.AttributeWrite,
) error {
	if len(writes) == 0 {
		return nil
	}

	searchAttrUpdates := index.ConvertAttributeWritesToSearchAttributeUpsertMap(writes)

	if len(searchAttrUpdates) > 0 {
		if err := am.provider.UpsertSearchAttributes(ctx, searchAttrUpdates); err != nil {
			return err
		}
	}

	for _, write := range writes {
		if utils.IsNullValue(write.GetValue()) {
			delete(am.attributes, write.GetKey())
			continue
		}
		am.attributes[write.GetKey()] = write.GetValue()
	}

	return nil
}

func (am *PersistenceManager) CanLockKeys(keys []string) bool {
	for _, key := range keys {
		if am.lockedKeys[key] {
			return false
		}
	}
	return true
}

func (am *PersistenceManager) UnlockKeys(keys []string) {
	for _, key := range keys {
		delete(am.lockedKeys, key)
	}
}

func (am *PersistenceManager) GetAttribute(key string) (*iwfpb.Value, bool) {
	value, ok := am.attributes[key]
	return value, ok
}

func sortedAttributeKeys(attributes map[string]*iwfpb.Value) []string {
	keys := make([]string, 0, len(attributes))
	for key := range attributes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedUniqueStrings(values []string) []string {
	unique := make(map[string]struct{}, len(values))
	for _, value := range values {
		unique[value] = struct{}{}
	}
	values = make([]string, 0, len(unique))
	for value := range unique {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}
