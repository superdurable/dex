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
	"fmt"
	"sort"

	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/service/common/mapper"
	"github.com/superdurable/iwf/service/interpreter/interfaces"
	"google.golang.org/protobuf/proto"
)

type PersistenceManager struct {
	provider interfaces.WorkflowProvider

	attributes map[string]*iwfpb.Value

	lockedKeys map[string]bool
}

type attributeMutation struct {
	key   string
	value *iwfpb.Value
}

func NewPersistenceManager(
	provider interfaces.WorkflowProvider,
	initialAttributes []*iwfpb.KV,
) (*PersistenceManager, error) {
	if provider == nil {
		panic("PersistenceManager requires a WorkflowProvider")
	}

	attributes := make(map[string]*iwfpb.Value, len(initialAttributes))
	seenKeys := make(map[string]struct{}, len(initialAttributes))
	for index, attribute := range initialAttributes {
		if err := validateKV(attribute); err != nil {
			return nil, fmt.Errorf("initial attribute %d: %w", index, err)
		}
		if _, duplicated := seenKeys[attribute.GetKey()]; duplicated {
			return nil, fmt.Errorf("duplicate attribute key %q", attribute.GetKey())
		}
		seenKeys[attribute.GetKey()] = struct{}{}
		if isNullValue(attribute.GetValue()) {
			continue
		}
		attributes[attribute.GetKey()] = attribute.GetValue()
	}

	return &PersistenceManager{
		provider:   provider,
		attributes: attributes,
		// locks will not be carried over during continueAsNew
		lockedKeys: map[string]bool{},
	}, nil
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
	keys []string,
) ([]*iwfpb.KV, error) {
	if err := am.provider.Await(ctx, func() bool {
		return am.CanLockKeys(keys)
	}); err != nil {
		return nil, err
	}
	for _, key := range keys {
		am.lockedKeys[key] = true
	}
	return am.GetAllAttributes(), nil
}

func (am *PersistenceManager) GetAllAttributes() []*iwfpb.KV {
	attributes := make([]*iwfpb.KV, 0, len(am.attributes))

	// NOTE: using sortedAttributeKeys so that the protobuf snapshot for continueAsNew is stable for pagination
	// TODO: we should use deterministic map iteration in interpreter for safety
	// https://github.com/superdurable/iwf/issues/510
	for _, key := range sortedAttributeKeys(am.attributes) {
		attributes = append(attributes, &iwfpb.KV{Key: key, Value: am.attributes[key]})
	}
	return attributes
}

func (am *PersistenceManager) ApplyAttributeWrites(
	ctx interfaces.UnifiedContext,
	writes []*iwfpb.AttributeWrite,
) (bool, error) {
	if len(writes) == 0 {
		return true, nil
	}

	mutations, indexedUpdates, applicable, err := am.planAttributeWrites(writes)
	if err != nil {
		return false, err
	}
	if !applicable {
		return false, nil
	}
	if len(mutations) == 0 && len(indexedUpdates) == 0 {
		return true, nil
	}
	if len(indexedUpdates) > 0 {
		if err := am.provider.UpsertSearchAttributes(ctx, indexedUpdates); err != nil {
			return false, fmt.Errorf("upsert indexed attributes: %w", err)
		}
	}

	for _, mutation := range mutations {
		if mutation.value == nil {
			delete(am.attributes, mutation.key)
		} else {
			am.attributes[mutation.key] = mutation.value
		}
	}
	return true, nil
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

func (am *PersistenceManager) HasAnyLock() bool {
	return len(am.lockedKeys) > 0
}

func (am *PersistenceManager) planAttributeWrites(
	writes []*iwfpb.AttributeWrite,
) ([]attributeMutation, map[string]interface{}, bool, error) {
	seenKeys := make(map[string]struct{}, len(writes))
	for index, write := range writes {
		if err := validateAttributeWrite(write); err != nil {
			return nil, nil, false, fmt.Errorf("attribute %d: %w", index, err)
		}
		if _, duplicated := seenKeys[write.GetKey()]; duplicated {
			return nil, nil, false, fmt.Errorf("duplicate attribute key %q", write.GetKey())
		}
		seenKeys[write.GetKey()] = struct{}{}
	}
	for _, write := range writes {
		if am.lockedKeys[write.GetKey()] {
			return nil, nil, false, nil
		}
	}

	mutations := make([]attributeMutation, 0, len(writes))
	indexedUpdates := make(map[string]interface{})
	for _, write := range writes {
		if err := addIndexedUpdate(indexedUpdates, write); err != nil {
			return nil, nil, false, fmt.Errorf("attribute %q: %w", write.GetKey(), err)
		}

		_, exists := am.attributes[write.GetKey()]
		if isNullValue(write.GetValue()) {
			if exists {
				mutations = append(mutations, attributeMutation{key: write.GetKey()})
			}
			continue
		}
		mutations = append(mutations, attributeMutation{key: write.GetKey(), value: write.GetValue()})
	}
	return mutations, indexedUpdates, true, nil
}

func validateAttributeWrite(write *iwfpb.AttributeWrite) error {
	if write == nil {
		return fmt.Errorf("write is nil")
	}
	if write.GetKey() == "" {
		return fmt.Errorf("key is empty")
	}
	if write.GetValue() == nil || write.GetValue().GetKind() == nil {
		return fmt.Errorf("value is missing")
	}
	return nil
}

func validateKV(attribute *iwfpb.KV) error {
	if attribute == nil {
		return fmt.Errorf("attribute is nil")
	}
	if attribute.GetKey() == "" {
		return fmt.Errorf("key is empty")
	}
	if attribute.GetValue() == nil || attribute.GetValue().GetKind() == nil {
		return fmt.Errorf("value is missing")
	}
	return nil
}

func addIndexedUpdate(updates map[string]interface{}, write *iwfpb.AttributeWrite) error {
	if indexedKey(write) == "" {
		return nil
	}
	if isNullValue(write.GetValue()) {
		updates[indexedKey(write)] = nil
		return nil
	}
	mapped, err := mapper.MapAttributeWritesToSearchAttributes([]*iwfpb.AttributeWrite{write})
	if err != nil {
		return err
	}
	for key, value := range mapped {
		updates[key] = value
	}
	return nil
}

func indexedKey(write *iwfpb.AttributeWrite) string {
	if write.GetIndexConfig() == nil || !write.GetIndexConfig().GetEnable() {
		return ""
	}
	return mapper.IndexKey(write)
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

func attributeValuesEqual(left, right *iwfpb.Value) bool {
	return proto.Equal(left, right)
}

func isNullValue(value *iwfpb.Value) bool {
	if value == nil {
		return false
	}
	_, ok := value.GetKind().(*iwfpb.Value_NullValue)
	return ok
}
