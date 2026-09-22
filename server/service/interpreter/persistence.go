// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Sustainable Use License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package interpreter

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service"
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

func (am *PersistenceManager) LoadSelectedAttributes(
	ctx interfaces.UnifiedContext,
	keysToLock []string,
	attributeMapInstances []string,
) ([]*dexpb.KV, error) {
	if err := am.provider.Await(ctx, func() bool {
		return am.CanLockKeys(keysToLock)
	}); err != nil {
		return nil, err
	}
	am.lockKeys(keysToLock)
	return am.GetSelectedAttributes(attributeMapInstances), nil
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
	return am.GetSelectedAttributes(attributeMapInstances), true
}

func (am *PersistenceManager) GetAllAttributes() []*dexpb.KV {
	attributes := make([]*dexpb.KV, 0, len(am.attributes))

	// NOTE: using sortedAttributeKeys so that the protobuf snapshot for continueAsNew is stable for pagination
	for _, key := range sortedAttributeKeys(am.attributes) {
		attributes = append(attributes, &dexpb.KV{Key: key, Value: am.attributes[key]})
	}
	return attributes
}

// GetSelectedAttributes returns ordinary Attributes and selected AttributeMap entries.
func (am *PersistenceManager) GetSelectedAttributes(attributeMapInstances []string) []*dexpb.KV {
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
	return am.ApplyAttributeWritesWithActionPermissionMappings(ctx, writes, nil)
}

func (am *PersistenceManager) ApplyAttributeWritesWithActionPermissionMappings(
	ctx interfaces.UnifiedContext,
	writes []*dexpb.AttributeWrite,
	mappings *dexpb.ActionPermissionMappings,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if mappings != nil {
		projectionWrite, err := am.actionPermissionProjectionWrite(writes, mappings)
		if err != nil {
			return err
		}
		if projectionWrite != nil {
			writes = append(writes, projectionWrite)
		}
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

func (am *PersistenceManager) actionPermissionProjectionWrite(
	writes []*dexpb.AttributeWrite,
	mappings *dexpb.ActionPermissionMappings,
) (*dexpb.AttributeWrite, error) {
	permissionSet := make(map[string]struct{}, len(mappings.GetMappings()))
	for _, mapping := range mappings.GetMappings() {
		value := am.attributeValueAfterWrites(mapping.GetAttributeKey(), writes)
		for _, equalValue := range mapping.GetEqualValues() {
			if equalActionPermissionValues(value, equalValue) {
				permissionSet[mapping.GetRequiredPermission()] = struct{}{}
				break
			}
		}
	}
	permissions := make([]string, 0, len(permissionSet))
	for permission := range permissionSet {
		permissions = append(permissions, permission)
	}
	sort.Strings(permissions)
	currentPermissions, isCurrentValueValid := decodeWorkQueuePermissions(
		am.attributes[service.SearchAttributeDexWorkQueuePermissions],
	)
	if isCurrentValueValid && reflect.DeepEqual(currentPermissions, permissions) {
		return nil, nil
	}
	value := &dexpb.Value{Kind: &dexpb.Value_NullValue{}}
	if len(permissions) > 0 {
		payload, err := json.Marshal(permissions)
		if err != nil {
			return nil, fmt.Errorf("encode Action permission projection: %w", err)
		}
		value = &dexpb.Value{Kind: &dexpb.Value_ObjValue{
			ObjValue: &dexpb.EncodedObject{Encoding: "json", Payload: payload},
		}}
	}
	return &dexpb.AttributeWrite{
		Key:   service.SearchAttributeDexWorkQueuePermissions,
		Value: value,
		IndexConfig: &dexpb.IndexConfig{
			Enable:   true,
			Type:     dexpb.IndexType_INDEX_TYPE_KEYWORD_ARRAY,
			IndexKey: service.SearchAttributeDexWorkQueuePermissions,
		},
	}, nil
}

func (am *PersistenceManager) attributeValueAfterWrites(
	key string,
	writes []*dexpb.AttributeWrite,
) *dexpb.Value {
	for writeIndex := len(writes) - 1; writeIndex >= 0; writeIndex-- {
		write := writes[writeIndex]
		if write == nil || write.GetKey() != key {
			continue
		}
		if utils.IsNullValue(write.GetValue()) {
			return nil
		}
		return write.GetValue()
	}
	return am.attributes[key]
}

func equalActionPermissionValues(left *dexpb.Value, right *dexpb.Value) bool {
	if left == nil || right == nil {
		return false
	}
	switch leftValue := left.GetKind().(type) {
	case *dexpb.Value_StringValue:
		rightValue, ok := right.GetKind().(*dexpb.Value_StringValue)
		return ok && leftValue.StringValue == rightValue.StringValue
	case *dexpb.Value_BoolValue:
		rightValue, ok := right.GetKind().(*dexpb.Value_BoolValue)
		return ok && leftValue.BoolValue == rightValue.BoolValue
	case *dexpb.Value_IntValue:
		rightValue, ok := right.GetKind().(*dexpb.Value_IntValue)
		return ok && leftValue.IntValue == rightValue.IntValue
	case *dexpb.Value_DoubleValue:
		rightValue, ok := right.GetKind().(*dexpb.Value_DoubleValue)
		return ok && leftValue.DoubleValue == rightValue.DoubleValue
	default:
		return false
	}
}

func decodeWorkQueuePermissions(value *dexpb.Value) ([]string, bool) {
	if value == nil || utils.IsNullValue(value) {
		return []string{}, true
	}
	object := value.GetObjValue()
	if object == nil || object.GetEncoding() != "json" {
		return nil, false
	}
	var permissions []string
	if err := json.Unmarshal(object.GetPayload(), &permissions); err != nil {
		return nil, false
	}
	permissionSet := make(map[string]struct{}, len(permissions))
	for _, permission := range permissions {
		permissionSet[permission] = struct{}{}
	}
	permissions = permissions[:0]
	for permission := range permissionSet {
		permissions = append(permissions, permission)
	}
	sort.Strings(permissions)
	return permissions, true
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
