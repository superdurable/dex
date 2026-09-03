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
	"strings"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/common/index"
	"github.com/superdurable/dex/service/common/utils"
	interpreterconfig "github.com/superdurable/dex/service/interpreter/config"
	"github.com/superdurable/dex/service/interpreter/interfaces"
)

type PersistenceManager struct {
	provider     interfaces.WorkflowProvider
	synchronizer *AttributeSynchronizer
	flowConfiger *interpreterconfig.FlowConfiger

	attributes map[string]*dexpb.Value

	lockedKeys map[string]bool
}

func NewPersistenceManager(
	provider interfaces.WorkflowProvider,
	initialAttributes []*dexpb.KV,
	synchronizer *AttributeSynchronizer,
	flowConfiger *interpreterconfig.FlowConfiger,
) *PersistenceManager {
	if provider == nil || synchronizer == nil || flowConfiger == nil {
		panic("PersistenceManager requires non-nil dependencies")
	}

	attributes := make(map[string]*dexpb.Value, len(initialAttributes))
	for _, attribute := range initialAttributes {
		if utils.IsNullValue(attribute.GetValue()) {
			continue
		}
		attributes[attribute.GetKey()] = attribute.GetValue()
	}

	return &PersistenceManager{
		provider:     provider,
		synchronizer: synchronizer,
		flowConfiger: flowConfiger,
		attributes:   attributes,
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
	am.lockKeys(keysToLock)
	return am.GetAllAttributes(), nil
}

func (am *PersistenceManager) TryLoadAttributes(
	keysToLock []string,
) ([]*dexpb.KV, bool) {
	if !am.CanLockKeys(keysToLock) {
		return nil, false
	}
	am.lockKeys(keysToLock)
	return am.GetAllAttributes(), true
}

// TryLoadRPCAttributes locks keys and loads the requested RPC Attribute state.
func (am *PersistenceManager) TryLoadRPCAttributes(
	keysToLock []string,
	attributeMapInstances []string,
) ([]*dexpb.KV, bool) {
	if !am.CanLockKeys(keysToLock) {
		return nil, false
	}
	am.lockKeys(keysToLock)
	return am.GetRPCAttributes(attributeMapInstances), true
}

func (am *PersistenceManager) GetAllAttributes() []*dexpb.KV {
	attributes := make([]*dexpb.KV, 0, len(am.attributes))

	// NOTE: using sortedAttributeKeys so that the protobuf snapshot for continueAsNew is stable for pagination
	for _, key := range sortedAttributeKeys(am.attributes) {
		attributes = append(attributes, &dexpb.KV{Key: key, Value: am.attributes[key]})
	}
	return attributes
}

// GetRPCAttributes returns ordinary Attributes and requested AttributeMap entries.
func (am *PersistenceManager) GetRPCAttributes(attributeMapInstances []string) []*dexpb.KV {
	allInstancePrefixes := make([]string, 0, len(attributeMapInstances))
	exactInstances := make(map[string]struct{}, len(attributeMapInstances))
	for _, instance := range attributeMapInstances {
		if strings.HasSuffix(instance, "/") {
			allInstancePrefixes = append(allInstancePrefixes, instance)
		} else {
			exactInstances[instance] = struct{}{}
		}
	}
	attributes := make([]*dexpb.KV, 0, len(am.attributes))
	for _, key := range sortedAttributeKeys(am.attributes) {
		separatorIndex := strings.IndexByte(key, '/')
		if separatorIndex >= 0 {
			_, loaded := exactInstances[key]
			for _, prefix := range allInstancePrefixes {
				loaded = loaded || strings.HasPrefix(key, prefix)
			}
			if !loaded {
				continue
			}
		}
		attributes = append(attributes, &dexpb.KV{Key: key, Value: am.attributes[key]})
	}
	return attributes
}

func (am *PersistenceManager) ApplyAttributeWrites(
	ctx interfaces.UnifiedContext,
	writes []*dexpb.AttributeWrite,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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
	am.synchronizer.AppendingToPendings(
		ctx,
		writes,
		am.flowConfiger.Get().GetAttributeStoreNames().GetNames(),
	)

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

func (am *PersistenceManager) lockKeys(keys []string) {
	for _, key := range keys {
		am.lockedKeys[key] = true
	}
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

func attributeWritesToKVs(writes []*dexpb.AttributeWrite) []*dexpb.KV {
	attributes := make([]*dexpb.KV, 0, len(writes))
	for _, write := range writes {
		if write == nil || utils.IsNullValue(write.GetValue()) {
			continue
		}
		attributes = append(attributes, &dexpb.KV{Key: write.GetKey(), Value: write.GetValue()})
	}
	return attributes
}
