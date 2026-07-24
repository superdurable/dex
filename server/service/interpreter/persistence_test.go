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
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/service/interpreter/interfaces"
	"google.golang.org/protobuf/types/known/structpb"
)

type s2WorkflowProvider struct {
	interfaces.WorkflowProvider

	upserts   []map[string]interface{}
	upsertErr error
}

func (p *s2WorkflowProvider) Await(
	_ interfaces.UnifiedContext,
	condition func() bool,
) error {
	if !condition() {
		return errors.New("condition is not ready")
	}
	return nil
}

func (p *s2WorkflowProvider) UpsertSearchAttributes(
	_ interfaces.UnifiedContext,
	attributes map[string]interface{},
) error {
	if p.upsertErr != nil {
		return p.upsertErr
	}
	p.upserts = append(p.upserts, attributes)
	return nil
}

func TestPersistenceOwnershipOrderingAndQuery(t *testing.T) {
	provider := &s2WorkflowProvider{}
	attributeB := stringKV("b", "two")
	attributeA := stringKV("a", "one")
	manager, err := NewPersistenceManager(provider, []*iwfpb.KV{attributeB, attributeA})
	require.NoError(t, err)

	all := manager.GetAllAttributes()
	require.Equal(t, []string{"a", "b"}, []string{all[0].GetKey(), all[1].GetKey()})
	require.Same(t, attributeA.GetValue(), all[0].GetValue())

	response := manager.GetAttributes(&iwfpb.GetAttributesQueryRequest{
		Keys: []string{"b", "missing", "a", "b"},
	})
	require.Equal(t, []string{"a", "b"}, []string{
		response.GetAttributes()[0].GetKey(),
		response.GetAttributes()[1].GetKey(),
	})
	require.Same(t, attributeA.GetValue(), response.GetAttributes()[0].GetValue())

	response = manager.GetAttributes(&iwfpb.GetAttributesQueryRequest{
		Keys:    []string{"b"},
		AllKeys: true,
	})
	require.Equal(t, 2, len(response.GetAttributes()))
}

func TestPersistenceBatchSerializedEqualityAndValidation(t *testing.T) {
	provider := &s2WorkflowProvider{}
	manager, err := NewPersistenceManager(provider, nil)
	require.NoError(t, err)

	object := &iwfpb.AttributeWrite{
		Key: "object",
		Value: &iwfpb.Value{Kind: &iwfpb.Value_ObjValue{ObjValue: &iwfpb.EncodedObject{
			Encoding: "json",
			Payload:  []byte(`{"value":1}`),
		}}},
	}
	applied, err := manager.ApplyAttributeWrites(nil, []*iwfpb.AttributeWrite{object})
	require.NoError(t, err)
	require.True(t, applied)

	equalSerializedObject := &iwfpb.AttributeWrite{
		Key: "object",
		Value: &iwfpb.Value{Kind: &iwfpb.Value_ObjValue{ObjValue: &iwfpb.EncodedObject{
			Encoding: "json",
			Payload:  []byte(`{"value":1}`),
		}}},
	}
	applied, err = manager.ApplyAttributeWrites(nil, []*iwfpb.AttributeWrite{equalSerializedObject})
	require.NoError(t, err)
	require.True(t, applied)
	stored, exists := manager.GetAttribute("object")
	require.True(t, exists)
	require.Same(t, equalSerializedObject.GetValue(), stored)
	require.Empty(t, provider.upserts)

	applied, err = manager.ApplyAttributeWrites(nil, []*iwfpb.AttributeWrite{
		stringAttribute("other", "value", nil),
		{Key: "invalid"},
	})
	require.Error(t, err)
	require.False(t, applied)
	_, exists = manager.GetAttribute("other")
	require.False(t, exists)
}

func TestPersistenceIndexedMutationIsAtomic(t *testing.T) {
	provider := &s2WorkflowProvider{}
	indexConfig := &iwfpb.IndexConfig{
		Enable:   true,
		Type:     iwfpb.IndexType_INDEX_TYPE_KEYWORD,
		IndexKey: "CustomKeywordField",
	}
	initial := stringKV("indexed", "old")
	manager, err := NewPersistenceManager(provider, []*iwfpb.KV{initial})
	require.NoError(t, err)

	provider.upsertErr = errors.New("backend unavailable")
	replacement := stringAttribute("indexed", "new", indexConfig)
	applied, err := manager.ApplyAttributeWrites(nil, []*iwfpb.AttributeWrite{replacement})
	require.ErrorContains(t, err, "backend unavailable")
	require.False(t, applied)
	stored, exists := manager.GetAttribute("indexed")
	require.True(t, exists)
	require.Same(t, initial.GetValue(), stored)

	provider.upsertErr = nil
	applied, err = manager.ApplyAttributeWrites(nil, []*iwfpb.AttributeWrite{replacement})
	require.NoError(t, err)
	require.True(t, applied)
	require.Equal(t, "new", provider.upserts[0]["CustomKeywordField"])
	require.Same(t, replacement.GetValue(), manager.GetAllAttributes()[0].GetValue())
}

func TestPersistenceNullDeletesUsingCurrentIndexConfig(t *testing.T) {
	provider := &s2WorkflowProvider{}
	manager, err := NewPersistenceManager(provider, []*iwfpb.KV{stringKV("indexed", "old")})
	require.NoError(t, err)

	deletion := &iwfpb.AttributeWrite{
		Key: "indexed",
		Value: &iwfpb.Value{Kind: &iwfpb.Value_NullValue{
			NullValue: structpb.NullValue_NULL_VALUE,
		}},
		IndexConfig: &iwfpb.IndexConfig{Enable: true, IndexKey: "CurrentIndexKey"},
	}
	applied, err := manager.ApplyAttributeWrites(nil, []*iwfpb.AttributeWrite{deletion})
	require.NoError(t, err)
	require.True(t, applied)
	require.Contains(t, provider.upserts[0], "CurrentIndexKey")
	require.Nil(t, provider.upserts[0]["CurrentIndexKey"])
	_, exists := manager.GetAttribute("indexed")
	require.False(t, exists)

	applied, err = manager.ApplyAttributeWrites(nil, []*iwfpb.AttributeWrite{deletion})
	require.NoError(t, err)
	require.True(t, applied)
	require.Len(t, provider.upserts, 2)
}

func TestPersistenceUsesOnlyCurrentIndexConfig(t *testing.T) {
	provider := &s2WorkflowProvider{}
	manager, err := NewPersistenceManager(provider, []*iwfpb.KV{stringKV("indexed", "old")})
	require.NoError(t, err)

	moved := stringAttribute("indexed", "new", &iwfpb.IndexConfig{
		Enable:   true,
		Type:     iwfpb.IndexType_INDEX_TYPE_KEYWORD,
		IndexKey: "NewIndexKey",
	})
	applied, err := manager.ApplyAttributeWrites(nil, []*iwfpb.AttributeWrite{moved})
	require.NoError(t, err)
	require.True(t, applied)
	require.NotContains(t, provider.upserts[0], "OldIndexKey")
	require.Equal(t, "new", provider.upserts[0]["NewIndexKey"])

	sameValueWithNewIndex := stringAttribute("indexed", "new", &iwfpb.IndexConfig{
		Enable:   true,
		Type:     iwfpb.IndexType_INDEX_TYPE_KEYWORD,
		IndexKey: "AnotherIndexKey",
	})
	applied, err = manager.ApplyAttributeWrites(nil, []*iwfpb.AttributeWrite{sameValueWithNewIndex})
	require.NoError(t, err)
	require.True(t, applied)
	require.Equal(t, "new", provider.upserts[1]["AnotherIndexKey"])
	stored, exists := manager.GetAttribute("indexed")
	require.True(t, exists)
	require.Same(t, sameValueWithNewIndex.GetValue(), stored)

	disabled := stringAttribute("indexed", "stored-only", nil)
	applied, err = manager.ApplyAttributeWrites(nil, []*iwfpb.AttributeWrite{disabled})
	require.NoError(t, err)
	require.True(t, applied)
	require.Len(t, provider.upserts, 2)
}

func TestPersistenceDoesNotEnforceIndexOwnership(t *testing.T) {
	provider := &s2WorkflowProvider{}
	manager, err := NewPersistenceManager(provider, nil)
	require.NoError(t, err)

	applied, err := manager.ApplyAttributeWrites(nil, []*iwfpb.AttributeWrite{
		stringAttribute("first", "new-first", &iwfpb.IndexConfig{
			Enable:   true,
			Type:     iwfpb.IndexType_INDEX_TYPE_KEYWORD,
			IndexKey: "SharedIndexKey",
		}),
		stringAttribute("second", "new-second", &iwfpb.IndexConfig{
			Enable:   true,
			Type:     iwfpb.IndexType_INDEX_TYPE_KEYWORD,
			IndexKey: "SharedIndexKey",
		}),
	})
	require.NoError(t, err)
	require.True(t, applied)
	require.Equal(t, "new-second", provider.upserts[0]["SharedIndexKey"])
	require.Len(t, manager.GetAllAttributes(), 2)
}

func TestPersistenceLocksRejectWholeBatch(t *testing.T) {
	provider := &s2WorkflowProvider{}
	manager, err := NewPersistenceManager(provider, []*iwfpb.KV{
		stringKV("locked", "old"),
		stringKV("free", "old"),
	})
	require.NoError(t, err)

	attributes, err := manager.LoadAttributes(nil, []string{"locked"})
	require.NoError(t, err)
	require.Len(t, attributes, 2)
	require.True(t, manager.HasAnyLock())
	require.False(t, manager.CanLockKeys([]string{"locked"}))
	require.False(t, manager.CanLockKeys([]string{"free", "locked"}))
	_, err = manager.LoadAttributes(nil, []string{"free"})
	require.NoError(t, err)
	manager.UnlockKeys([]string{"free"})

	applied, err := manager.ApplyAttributeWrites(nil, []*iwfpb.AttributeWrite{
		stringAttribute("free", "new", nil),
		stringAttribute("locked", "new", nil),
	})
	require.NoError(t, err)
	require.False(t, applied)
	require.Equal(t, "old", manager.GetAttributes(&iwfpb.GetAttributesQueryRequest{
		Keys: []string{"free"},
	}).GetAttributes()[0].GetValue().GetStringValue())

	manager.UnlockKeys([]string{"locked"})
	require.False(t, manager.HasAnyLock())

	applied, err = manager.ApplyAttributeWrites(nil, []*iwfpb.AttributeWrite{
		stringAttribute("free", "new", nil),
		stringAttribute("locked", "new", nil),
	})
	require.NoError(t, err)
	require.True(t, applied)

	manager.UnlockKeys([]string{"locked"})
}

func TestPersistenceRejectsDuplicateAndInvalidInitialAttributes(t *testing.T) {
	provider := &s2WorkflowProvider{}
	_, err := NewPersistenceManager(provider, []*iwfpb.KV{
		stringKV("duplicate", "one"),
		stringKV("duplicate", "two"),
	})
	require.ErrorContains(t, err, "duplicate")

	_, err = NewPersistenceManager(provider, []*iwfpb.KV{{Key: "missing-value"}})
	require.ErrorContains(t, err, "value is missing")
}

func stringAttribute(key, value string, indexConfig *iwfpb.IndexConfig) *iwfpb.AttributeWrite {
	return &iwfpb.AttributeWrite{
		Key:         key,
		Value:       &iwfpb.Value{Kind: &iwfpb.Value_StringValue{StringValue: value}},
		IndexConfig: indexConfig,
	}
}

func stringKV(key, value string) *iwfpb.KV {
	return &iwfpb.KV{
		Key:   key,
		Value: &iwfpb.Value{Kind: &iwfpb.Value_StringValue{StringValue: value}},
	}
}
