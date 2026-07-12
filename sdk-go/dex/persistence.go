package dex

import (
	"fmt"
	"strings"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
)

// PersistenceSchema declares state keys and channels for a flow.
type PersistenceSchema struct {
	StateKeys        []StateKeyDef
	DynamicStateKeys []StateKeyDef
	Channels         []ChannelDef
	DynamicChannels  []ChannelDef
}

// persistenceImpl holds the current state & channel for a stepExecution
// and buffered/pending writes/publishes
type persistenceImpl struct {
	raw                    map[string]*pb.Value
	schema                 PersistenceSchema
	allowedKeys            map[string]struct{}
	dynamicPrefixes        []string
	allowedChannels        map[string]struct{}
	dynamicChannelPrefixes []string
	pendingWrites          map[string]*pb.Value
	pendingPublishes       []ChannelMessage
	objectCodec            ObjectCodec
}

func newPersistence(stateMap map[string]*pb.Value, schema PersistenceSchema, codec ObjectCodec) *persistenceImpl {
	allowed := make(map[string]struct{}, len(schema.StateKeys))
	var dynamicPrefixes []string
	for _, def := range schema.StateKeys {
		allowed[def.Name] = struct{}{}
	}
	for _, def := range schema.DynamicStateKeys {
		dynamicPrefixes = append(dynamicPrefixes, def.Name)
	}
	allowedChannels := make(map[string]struct{}, len(schema.Channels))
	var dynamicChannelPrefixes []string
	for _, def := range schema.Channels {
		allowedChannels[def.Name] = struct{}{}
	}
	for _, def := range schema.DynamicChannels {
		dynamicChannelPrefixes = append(dynamicChannelPrefixes, def.Name)
	}
	return &persistenceImpl{
		raw:                    stateMap,
		schema:                 schema,
		allowedKeys:            allowed,
		dynamicPrefixes:        dynamicPrefixes,
		allowedChannels:        allowedChannels,
		dynamicChannelPrefixes: dynamicChannelPrefixes,
		pendingWrites:          make(map[string]*pb.Value),
		objectCodec:            codec,
	}
}

func getStateKeyTyped[T any](persistence *persistenceImpl, key string) (T, error) {
	var zero T
	if err := persistence.validateKey(key); err != nil {
		return zero, err
	}
	if val, ok := persistence.pendingWrites[key]; ok {
		return decodeStateKeyTyped[T](persistence.objectCodec, val)
	}
	if persistence.raw == nil {
		return zero, nil
	}
	return decodeStateKeyTyped[T](persistence.objectCodec, persistence.raw[key])
}

func (persistence *persistenceImpl) setStateKeyWire(key string, value any) error {
	if err := persistence.validateKey(key); err != nil {
		return err
	}
	encoded, err := persistence.objectCodec.EncodeValue(value)
	if err != nil {
		return fmt.Errorf("dex: set state key %q: %w", key, err)
	}
	persistence.pendingWrites[key] = encoded
	return nil
}

func (persistence *persistenceImpl) PublishToChannel(msgs ...ChannelMessage) error {
	for _, message := range msgs {
		if err := persistence.validateChannel(message.ChannelName); err != nil {
			return err
		}
	}
	persistence.pendingPublishes = append(persistence.pendingPublishes, msgs...)
	return nil
}

func (persistence *persistenceImpl) flushState() map[string]*pb.Value {
	if persistence == nil || len(persistence.pendingWrites) == 0 {
		return nil
	}
	out := make(map[string]*pb.Value, len(persistence.pendingWrites))
	for key, val := range persistence.pendingWrites {
		out[key] = val
	}
	return out
}

func (persistence *persistenceImpl) flushPublishes() []ChannelMessage {
	if persistence == nil || len(persistence.pendingPublishes) == 0 {
		return nil
	}
	return append([]ChannelMessage(nil), persistence.pendingPublishes...)
}

func (persistence *persistenceImpl) validateKey(key string) error {
	if _, ok := persistence.allowedKeys[key]; ok {
		return nil
	}
	for _, prefix := range persistence.dynamicPrefixes {
		if strings.HasPrefix(key, prefix) {
			return nil
		}
	}
	if len(persistence.allowedKeys) == 0 && len(persistence.dynamicPrefixes) == 0 {
		return nil
	}
	return newUndeclaredStateKeyError(key)
}

func (persistence *persistenceImpl) validateChannel(channelName string) error {
	if _, ok := persistence.allowedChannels[channelName]; ok {
		return nil
	}
	for _, prefix := range persistence.dynamicChannelPrefixes {
		if strings.HasPrefix(channelName, prefix) {
			return nil
		}
	}
	if len(persistence.allowedChannels) == 0 && len(persistence.dynamicChannelPrefixes) == 0 {
		return nil
	}
	return newUndeclaredChannelError(channelName)
}

func (persistence *persistenceImpl) validateWaitCondition(condition WaitForCondition) error {
	for _, singleCondition := range waitConditionList(condition) {
		channel, ok := singleCondition.(channelCondition)
		if !ok {
			continue
		}
		if err := persistence.validateChannel(channel.ChannelName); err != nil {
			return err
		}
	}
	return nil
}
