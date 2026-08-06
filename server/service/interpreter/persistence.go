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
	"sort"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/common/index"
	"github.com/superdurable/dex/service/common/utils"
	"github.com/superdurable/dex/service/interpreter/interfaces"
)

type PersistenceManager struct {
	provider          interfaces.WorkflowProvider
	indexSynchronizer *IndexSynchronizer

	attributes map[string]*dexpb.Value

	lockedKeys map[string]bool
}

func NewPersistenceManager(
	provider interfaces.WorkflowProvider,
	initialAttributes []*dexpb.KV,
	indexSynchronizer *IndexSynchronizer,
) *PersistenceManager {
	if provider == nil {
		panic("PersistenceManager requires a WorkflowProvider")
	}

	attributes := make(map[string]*dexpb.Value, len(initialAttributes))
	for _, attribute := range initialAttributes {
		if utils.IsNullValue(attribute.GetValue()) {
			continue
		}
		attributes[attribute.GetKey()] = attribute.GetValue()
	}

	return &PersistenceManager{
		provider:          provider,
		indexSynchronizer: indexSynchronizer,
		attributes:        attributes,
		// locks will not be carried over during continueAsNew
		lockedKeys: map[string]bool{},
	}
}

func (am *PersistenceManager) GetAttributes(
	request *dexpb.GetAttributesQueryRequest,
) *dexpb.GetAttributesQueryResponse {
	if request == nil {
		panic("GetAttributes requires a request")
	}

	keys := request.GetKeys()
	if request.GetAllKeys() {
		keys = sortedAttributeKeys(am.attributes)
	} else {
		keys = sortedUniqueStrings(keys)
	}

	attributes := make([]*dexpb.KV, 0, len(keys))
	for _, key := range keys {
		value, ok := am.attributes[key]
		if !ok {
			continue
		}
		attributes = append(attributes, &dexpb.KV{Key: key, Value: value})
	}
	return &dexpb.GetAttributesQueryResponse{Attributes: attributes}
}

func (am *PersistenceManager) LoadAttributes(
	ctx interfaces.UnifiedContext,
	keysToLock []string,
) ([]*dexpb.KV, error) {
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

func (am *PersistenceManager) GetAllAttributes() []*dexpb.KV {
	attributes := make([]*dexpb.KV, 0, len(am.attributes))

	// NOTE: using sortedAttributeKeys so that the protobuf snapshot for continueAsNew is stable for pagination
	for _, key := range sortedAttributeKeys(am.attributes) {
		attributes = append(attributes, &dexpb.KV{Key: key, Value: am.attributes[key]})
	}
	return attributes
}

func (am *PersistenceManager) ApplyAttributeWrites(
	ctx interfaces.UnifiedContext,
	writes []*dexpb.AttributeWrite,
) error {
	if len(writes) == 0 {
		return nil
	}
	if am.indexSynchronizer != nil {
		for _, write := range writes {
			if utils.IsNullValue(write.GetValue()) {
				delete(am.attributes, write.GetKey())
				continue
			}
			am.attributes[write.GetKey()] = write.GetValue()
		}
		am.indexSynchronizer.ApplyAttributeWrites(writes)
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

func (am *PersistenceManager) GetAttribute(key string) (*dexpb.Value, bool) {
	value, ok := am.attributes[key]
	return value, ok
}

func sortedAttributeKeys(attributes map[string]*dexpb.Value) []string {
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
