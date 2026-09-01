// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package dex

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

// AtMost returns a Condition satisfied when no more than count messages are queued.
func (c Channel[T]) AtMost(count int, options ...ConditionOption) Condition {
	return newChannelCondition(c.name, "", false, nil, &count, options)
}

// AtLeastAtMost returns a Condition with inclusive lower and upper queue-size bounds.
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

func (Channel[T]) channelIsMap() bool {
	return false
}

// ChannelMap defines independently queued Channel instances under one shared name.
// Register the map once in PersistenceSchema and supply an instance string to each operation.
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

// AtMost returns an instance Condition with an inclusive upper queue-size bound.
func (c ChannelMap[T]) AtMost(
	instance string,
	count int,
	options ...ConditionOption,
) Condition {
	return newChannelCondition(c.name, instance, true, nil, &count, options)
}

// AtLeastAtMost returns an instance Condition with inclusive lower and upper bounds.
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
	channelSize(name string) int
	channelMapSize(name string, instance string) int
	channelMapKeys(name string) []string
	getChannelResults(
		name string,
		instance string,
		isMap bool,
		resultsPtr any,
	) error
}
