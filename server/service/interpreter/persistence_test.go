// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package interpreter

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	interpreterconfig "github.com/superdurable/dex/service/interpreter/config"
	"github.com/superdurable/dex/service/interpreter/interfaces"
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
	manager := newTestPersistenceManager(provider, []*dexpb.KV{attributeB, attributeA})

	all := manager.GetAllAttributes()
	require.Equal(t, []string{"a", "b"}, []string{all[0].GetKey(), all[1].GetKey()})
	require.Same(t, attributeA.GetValue(), all[0].GetValue())

	response := manager.GetAttributes(&dexpb.GetAttributesQueryRequest{
		Keys: []string{"b", "missing", "a", "b"},
	})
	require.Equal(t, []string{"a", "b"}, []string{
		response.GetAttributes()[0].GetKey(),
		response.GetAttributes()[1].GetKey(),
	})
	require.Same(t, attributeA.GetValue(), response.GetAttributes()[0].GetValue())

	response = manager.GetAttributes(&dexpb.GetAttributesQueryRequest{
		Keys:    []string{"b"},
		AllKeys: true,
	})
	require.Equal(t, 2, len(response.GetAttributes()))
}

func TestPersistenceBatchSerializedEquality(t *testing.T) {
	provider := &s2WorkflowProvider{}
	manager := newTestPersistenceManager(provider, nil)

	object := &dexpb.AttributeWrite{
		Key: "object",
		Value: &dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{
			Encoding: "json",
			Payload:  []byte(`{"value":1}`),
		}}},
	}
	err := manager.ApplyAttributeWrites(persistenceTestContext(), []*dexpb.AttributeWrite{object})
	require.NoError(t, err)

	equalSerializedObject := &dexpb.AttributeWrite{
		Key: "object",
		Value: &dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{
			Encoding: "json",
			Payload:  []byte(`{"value":1}`),
		}}},
	}
	err = manager.ApplyAttributeWrites(persistenceTestContext(), []*dexpb.AttributeWrite{equalSerializedObject})
	require.NoError(t, err)
	stored, exists := manager.GetAttribute("object")
	require.True(t, exists)
	require.Same(t, equalSerializedObject.GetValue(), stored)
	require.Empty(t, provider.upserts)
}

func TestPersistenceIndexedMutationIsAtomic(t *testing.T) {
	provider := &s2WorkflowProvider{}
	indexConfig := &dexpb.IndexConfig{
		Enable:   true,
		Type:     dexpb.IndexType_INDEX_TYPE_KEYWORD,
		IndexKey: "CustomKeywordField",
	}
	initial := stringKV("indexed", "old")
	manager := newTestPersistenceManager(provider, []*dexpb.KV{initial})

	provider.upsertErr = errors.New("backend unavailable")
	replacement := stringAttribute("indexed", "new", indexConfig)
	err := manager.ApplyAttributeWrites(persistenceTestContext(), []*dexpb.AttributeWrite{replacement})
	require.ErrorContains(t, err, "backend unavailable")
	stored, exists := manager.GetAttribute("indexed")
	require.True(t, exists)
	require.Same(t, initial.GetValue(), stored)

	provider.upsertErr = nil
	err = manager.ApplyAttributeWrites(persistenceTestContext(), []*dexpb.AttributeWrite{replacement})
	require.NoError(t, err)
	require.Equal(t, "new", provider.upserts[0]["CustomKeywordField"])
	require.Same(t, replacement.GetValue(), manager.GetAllAttributes()[0].GetValue())
}

func TestPersistenceNullDeletesUsingCurrentIndexConfig(t *testing.T) {
	provider := &s2WorkflowProvider{}
	manager := newTestPersistenceManager(provider, []*dexpb.KV{stringKV("indexed", "old")})

	deletion := &dexpb.AttributeWrite{
		Key: "indexed",
		Value: &dexpb.Value{Kind: &dexpb.Value_NullValue{
			NullValue: structpb.NullValue_NULL_VALUE,
		}},
		IndexConfig: &dexpb.IndexConfig{Enable: true, IndexKey: "CurrentIndexKey"},
	}
	err := manager.ApplyAttributeWrites(persistenceTestContext(), []*dexpb.AttributeWrite{deletion})
	require.NoError(t, err)
	require.Contains(t, provider.upserts[0], "CurrentIndexKey")
	require.Nil(t, provider.upserts[0]["CurrentIndexKey"])
	_, exists := manager.GetAttribute("indexed")
	require.False(t, exists)

	err = manager.ApplyAttributeWrites(persistenceTestContext(), []*dexpb.AttributeWrite{deletion})
	require.NoError(t, err)
	require.Len(t, provider.upserts, 2)
}

func TestPersistenceUsesOnlyCurrentIndexConfig(t *testing.T) {
	provider := &s2WorkflowProvider{}
	manager := newTestPersistenceManager(provider, []*dexpb.KV{stringKV("indexed", "old")})

	moved := stringAttribute("indexed", "new", &dexpb.IndexConfig{
		Enable:   true,
		Type:     dexpb.IndexType_INDEX_TYPE_KEYWORD,
		IndexKey: "NewIndexKey",
	})
	err := manager.ApplyAttributeWrites(persistenceTestContext(), []*dexpb.AttributeWrite{moved})
	require.NoError(t, err)
	require.NotContains(t, provider.upserts[0], "OldIndexKey")
	require.Equal(t, "new", provider.upserts[0]["NewIndexKey"])

	sameValueWithNewIndex := stringAttribute("indexed", "new", &dexpb.IndexConfig{
		Enable:   true,
		Type:     dexpb.IndexType_INDEX_TYPE_KEYWORD,
		IndexKey: "AnotherIndexKey",
	})
	err = manager.ApplyAttributeWrites(persistenceTestContext(), []*dexpb.AttributeWrite{sameValueWithNewIndex})
	require.NoError(t, err)
	require.Equal(t, "new", provider.upserts[1]["AnotherIndexKey"])
	stored, exists := manager.GetAttribute("indexed")
	require.True(t, exists)
	require.Same(t, sameValueWithNewIndex.GetValue(), stored)

	disabled := stringAttribute("indexed", "stored-only", nil)
	err = manager.ApplyAttributeWrites(persistenceTestContext(), []*dexpb.AttributeWrite{disabled})
	require.NoError(t, err)
	require.Len(t, provider.upserts, 2)
}

func TestPersistenceDoesNotEnforceIndexOwnership(t *testing.T) {
	provider := &s2WorkflowProvider{}
	manager := newTestPersistenceManager(provider, nil)

	err := manager.ApplyAttributeWrites(persistenceTestContext(), []*dexpb.AttributeWrite{
		stringAttribute("first", "new-first", &dexpb.IndexConfig{
			Enable:   true,
			Type:     dexpb.IndexType_INDEX_TYPE_KEYWORD,
			IndexKey: "SharedIndexKey",
		}),
		stringAttribute("second", "new-second", &dexpb.IndexConfig{
			Enable:   true,
			Type:     dexpb.IndexType_INDEX_TYPE_KEYWORD,
			IndexKey: "SharedIndexKey",
		}),
	})
	require.NoError(t, err)
	require.Equal(t, "new-second", provider.upserts[0]["SharedIndexKey"])
	require.Len(t, manager.GetAllAttributes(), 2)
}

func stringAttribute(key, value string, indexConfig *dexpb.IndexConfig) *dexpb.AttributeWrite {
	return &dexpb.AttributeWrite{
		Key:         key,
		Value:       &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: value}},
		IndexConfig: indexConfig,
	}
}

func newTestPersistenceManager(
	provider interfaces.WorkflowProvider,
	attributes []*dexpb.KV,
) *PersistenceManager {
	return NewPersistenceManager(
		provider,
		attributes,
		&AttributeSynchronizer{},
		interpreterconfig.NewFlowConfiger(&dexpb.FlowConfig{}),
	)
}

func persistenceTestContext() interfaces.UnifiedContext {
	return interfaces.NewUnifiedContext(context.Background())
}

func stringKV(key, value string) *dexpb.KV {
	return &dexpb.KV{
		Key:   key,
		Value: &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: value}},
	}
}
