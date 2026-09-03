// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package dex

import (
	"fmt"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

// Channel defines one durable FIFO queue of typed messages.
//
// Add the Channel to PersistenceSchema. Clients and handlers may publish messages; Step WaitFor
// methods create Conditions that wait on queue-size bounds and later decode consumed results.
//
// Example:
//
//	var approvals = dex.DefineChannel[Approval]("approvals")
//	return dex.AnyOf(approvals.ForOne(), dex.Timer(5*time.Minute)), nil
type Channel[T any] struct {
	name string
}

// ChannelMapLoad selects one ChannelMap instance for an RPC invocation.
// Create values with ChannelMap.LoadMessages and place them in
// InvokeOptions.LoadChannelMapInstances.
type ChannelMapLoad struct {
	name     string
	instance string
}

// ChannelMessage identifies one pending Channel value returned by Client.GetChannelMessages.
//
// MessageID is assigned by Dex when the value is published. It can be passed to
// Client.DeleteChannelMessage or Channel.Delete from a transactional RPC.
type ChannelMessage[T any] struct {
	// MessageID is the server-assigned UUIDv7 for this pending message.
	MessageID string
	// Value is the decoded Channel value.
	Value T
}

// DefineChannel creates a typed Channel with a stable name without performing I/O.
// "/" is reserved as the ChannelMap separator and is prohibited in Channel names.
func DefineChannel[T any](name string) Channel[T] {
	return Channel[T]{name: name}
}

// ChannelDef is the interface of Channel, without Go's generic
//
// ChannelDef is internal to the SDK. Applications create values with DefineChannel or
// DefineChannelMap, then pass them to PersistenceSchema and Client methods.
type ChannelDef interface {
	channelName() string
	channelIsMap() bool
}

// Publish stages one message from the current Step or RPC invocation.
// It returns an error when ctx is invalid or value cannot be encoded.
func (c Channel[T]) Publish(ctx Context, value T) error {
	invocation, ok := ctx.(channelInvocation)
	if !ok {
		return errInvalidInvocationContext
	}
	return invocation.publishChannel(c.name, value)
}

// Delete stages one pending-message deletion from an RPC handler.
// Use InvokeOptions.IsTransactional when a missing ID must abort all RPC writes.
func (c Channel[T]) Delete(ctx Context, messageID string) error {
	invocation, ok := ctx.(channelInvocation)
	if !ok {
		return errInvalidInvocationContext
	}
	return invocation.deleteChannelMessage(c.name, "", false, messageID)
}

// PendingMessages returns the loaded RPC snapshot in FIFO order.
// It returns StateNotLoadedError when InvokeOptions did not select this Channel.
func (c Channel[T]) PendingMessages(ctx Context) ([]ChannelMessage[T], error) {
	invocation, ok := ctx.(channelInvocation)
	if !ok {
		return nil, errInvalidInvocationContext
	}
	messages, err := invocation.pendingChannelMessages(c.name, "", false)
	if err != nil {
		return nil, err
	}
	return decodePendingMessages[T](messages)
}

// FindPendingMessage returns one loaded message by ID and reports whether it exists.
func (c Channel[T]) FindPendingMessage(
	ctx Context,
	messageID string,
) (ChannelMessage[T], bool, error) {
	messages, err := c.PendingMessages(ctx)
	if err != nil {
		return ChannelMessage[T]{}, false, err
	}
	for _, message := range messages {
		if message.MessageID == messageID {
			return message, true, nil
		}
	}
	return ChannelMessage[T]{}, false, nil
}

// ForOne returns a Condition that consumes exactly one queued message.
func (c Channel[T]) ForOne(options ...ConditionOption) Condition {
	return newChannelCondition(c.name, "", false, nil, nil, options)
}

// ForN returns a Condition that consumes exactly count queued messages.
func (c Channel[T]) ForN(count int, options ...ConditionOption) Condition {
	return newChannelCondition(c.name, "", false, &count, &count, options)
}

// AtLeast returns a Condition satisfied when at least count messages are queued.
func (c Channel[T]) AtLeast(count int, options ...ConditionOption) Condition {
	return newChannelCondition(c.name, "", false, &count, nil, options)
}

// AtMost returns a non-blocking Condition consuming up to count messages when its Wait completes.
func (c Channel[T]) AtMost(count int, options ...ConditionOption) Condition {
	return newChannelCondition(c.name, "", false, nil, &count, options)
}

// AtLeastAtMost waits for atLeast messages, then consumes up to atMost currently queued messages.
func (c Channel[T]) AtLeastAtMost(
	atLeast int,
	atMost int,
	options ...ConditionOption,
) Condition {
	return newChannelCondition(
		c.name,
		"",
		false,
		&atLeast,
		&atMost,
		options,
	)
}

// Size returns the invocation snapshot's queued-message count.
// It panics when ctx did not originate from Dex.
func (c Channel[T]) Size(ctx Context) int {
	invocation, ok := ctx.(channelInvocation)
	if !ok {
		panic(errInvalidInvocationContext)
	}
	return invocation.channelSize(c.name)
}

// GetConditionResults decodes messages consumed by this Channel's satisfied Condition.
// It returns an error for an invalid context or an undecodable message.
func (c Channel[T]) GetConditionResults(ctx Context) ([]T, error) {
	invocation, ok := ctx.(channelInvocation)
	if !ok {
		return nil, errInvalidInvocationContext
	}
	var results []T
	if err := invocation.getChannelResults(c.name, "", false, &results); err != nil {
		return nil, err
	}
	return results, nil
}

// ChannelName returns the stable name sent to Dex and used in PersistenceSchema.
func (c Channel[T]) ChannelName() string {
	return c.name
}

func (c Channel[T]) channelName() string {
	return c.name
}

func (c Channel[T]) channelLoadName() string {
	return c.name
}

func (Channel[T]) channelIsMap() bool {
	return false
}

// ChannelMap defines independently queued Channel instances under one shared name.
// Register the map once in PersistenceSchema and supply an instance string to each operation.
// Slash is prohibited in instance keys because it is a reserved character.
type ChannelMap[T any] struct {
	name string
}

// DefineChannelMap creates a typed Channel map with a stable name.
// "/" is reserved as the ChannelMap separator and is prohibited in ChannelMap names.
func DefineChannelMap[T any](name string) ChannelMap[T] {
	return ChannelMap[T]{name: name}
}

// Publish stages one typed message for instance from the current invocation.
// It returns an error when ctx is invalid or value cannot be encoded.
func (c ChannelMap[T]) Publish(ctx Context, instance string, value T) error {
	invocation, ok := ctx.(channelInvocation)
	if !ok {
		return errInvalidInvocationContext
	}
	return invocation.publishChannelMap(c.name, instance, value)
}

// Delete stages one pending-message deletion from a ChannelMap instance in an RPC handler.
// Use InvokeOptions.IsTransactional when a missing ID must abort all RPC writes.
func (c ChannelMap[T]) Delete(ctx Context, instance string, messageID string) error {
	invocation, ok := ctx.(channelInvocation)
	if !ok {
		return errInvalidInvocationContext
	}
	return invocation.deleteChannelMessage(c.name, instance, true, messageID)
}

// LoadMessages selects pending messages from one slash-free logical instance for an RPC snapshot.
func (c ChannelMap[T]) LoadMessages(instance string) ChannelMapLoad {
	return ChannelMapLoad{name: c.name, instance: instance}
}

// PendingMessages returns one loaded instance snapshot in FIFO order.
// It returns StateNotLoadedError when InvokeOptions did not select this ChannelMap.
func (c ChannelMap[T]) PendingMessages(
	ctx Context,
	instance string,
) ([]ChannelMessage[T], error) {
	invocation, ok := ctx.(channelInvocation)
	if !ok {
		return nil, errInvalidInvocationContext
	}
	messages, err := invocation.pendingChannelMessages(c.name, instance, true)
	if err != nil {
		return nil, err
	}
	return decodePendingMessages[T](messages)
}

// FindPendingMessage returns one loaded instance message by ID and reports whether it exists.
func (c ChannelMap[T]) FindPendingMessage(
	ctx Context,
	instance string,
	messageID string,
) (ChannelMessage[T], bool, error) {
	messages, err := c.PendingMessages(ctx, instance)
	if err != nil {
		return ChannelMessage[T]{}, false, err
	}
	for _, message := range messages {
		if message.MessageID == messageID {
			return message, true, nil
		}
	}
	return ChannelMessage[T]{}, false, nil
}

// ForOne returns an instance Condition that consumes exactly one message.
func (c ChannelMap[T]) ForOne(
	instance string,
	options ...ConditionOption,
) Condition {
	return newChannelCondition(c.name, instance, true, nil, nil, options)
}

// ForN returns an instance Condition that consumes exactly count messages.
func (c ChannelMap[T]) ForN(
	instance string,
	count int,
	options ...ConditionOption,
) Condition {
	return newChannelCondition(
		c.name,
		instance,
		true,
		&count,
		&count,
		options,
	)
}

// AtLeast returns an instance Condition with an inclusive lower queue-size bound.
func (c ChannelMap[T]) AtLeast(
	instance string,
	count int,
	options ...ConditionOption,
) Condition {
	return newChannelCondition(c.name, instance, true, &count, nil, options)
}

// AtMost returns a non-blocking instance Condition consuming up to count messages when its Wait completes.
func (c ChannelMap[T]) AtMost(
	instance string,
	count int,
	options ...ConditionOption,
) Condition {
	return newChannelCondition(c.name, instance, true, nil, &count, options)
}

// AtLeastAtMost waits for atLeast instance messages, then consumes up to atMost currently queued messages.
func (c ChannelMap[T]) AtLeastAtMost(
	instance string,
	atLeast int,
	atMost int,
	options ...ConditionOption,
) Condition {
	return newChannelCondition(
		c.name,
		instance,
		true,
		&atLeast,
		&atMost,
		options,
	)
}

// Size returns the invocation snapshot's queue size for instance.
// It panics when ctx did not originate from Dex.
func (c ChannelMap[T]) Size(ctx Context, instance string) int {
	invocation, ok := ctx.(channelInvocation)
	if !ok {
		panic(errInvalidInvocationContext)
	}
	return invocation.channelMapSize(c.name, instance)
}

// MapSize returns the number of non-empty Channel instances visible to the current RPC.
func (c ChannelMap[T]) MapSize(ctx Context) int {
	return len(c.AllInstanceKeys(ctx))
}

// AllInstanceKeys returns non-empty Channel instance keys in ascending order for the current RPC.
func (c ChannelMap[T]) AllInstanceKeys(ctx Context) []string {
	invocation, ok := ctx.(channelInvocation)
	if !ok {
		panic(errInvalidInvocationContext)
	}
	return invocation.channelMapKeys(c.name)
}

// GetConditionResults decodes messages consumed by instance's satisfied Condition.
// It returns an error for an invalid context or an undecodable message.
func (c ChannelMap[T]) GetConditionResults(
	ctx Context,
	instance string,
) ([]T, error) {
	invocation, ok := ctx.(channelInvocation)
	if !ok {
		return nil, errInvalidInvocationContext
	}
	var results []T
	if err := invocation.getChannelResults(
		c.name,
		instance,
		true,
		&results,
	); err != nil {
		return nil, err
	}
	return results, nil
}

// ChannelName returns the stable shared name of this Channel map.
func (c ChannelMap[T]) ChannelName() string {
	return c.name
}

func (c ChannelMap[T]) channelName() string {
	return c.name
}

func (ChannelMap[T]) channelIsMap() bool {
	return true
}

type channelInvocation interface {
	publishChannel(name string, value any) error
	publishChannelMap(name string, instance string, value any) error
	deleteChannelMessage(name string, instance string, isMap bool, messageID string) error
	channelSize(name string) int
	channelMapSize(name string, instance string) int
	channelMapKeys(name string) []string
	pendingChannelMessages(
		name string,
		instance string,
		isMap bool,
	) ([]*dexpb.ChannelMessage, error)
	getChannelResults(
		name string,
		instance string,
		isMap bool,
		resultsPtr any,
	) error
}

func decodePendingMessages[T any](
	messages []*dexpb.ChannelMessage,
) ([]ChannelMessage[T], error) {
	decoded := make([]ChannelMessage[T], 0, len(messages))
	for _, message := range messages {
		if message == nil || message.MessageId == "" || message.Value == nil {
			return nil, fmt.Errorf("dex: invalid loaded Channel message")
		}
		var value T
		if err := decodeValue(message.Value, &value); err != nil {
			return nil, err
		}
		decoded = append(decoded, ChannelMessage[T]{
			MessageID: message.MessageId,
			Value:     value,
		})
	}
	return decoded, nil
}
