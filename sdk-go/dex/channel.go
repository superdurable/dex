package dex

// Channel is a typed, named communication channel.
type Channel[T any] struct {
	Name string
}

// NewChannel creates a typed channel with a fixed name.
func NewChannel[T any](name string) Channel[T] {
	return Channel[T]{Name: name}
}

// Condition creates a SingleCondition with default min=1, max=0.
func (ch Channel[T]) Condition() SingleCondition {
	return channelCondition{ChannelName: ch.Name, Min: 1}
}

// ConditionWithMinMax creates a SingleCondition with explicit min/max.
func (ch Channel[T]) ConditionWithMinMax(min, max int) SingleCondition {
	return channelCondition{ChannelName: ch.Name, Min: min, Max: max}
}

// GetConsumedMessages returns typed messages consumed by the wait condition.
func (ch Channel[T]) GetConsumedMessages(ctx Context) ([]T, error) {
	stepCtx, err := asStepContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := stepCtx.persistence.validateChannel(ch.Name); err != nil {
		return nil, err
	}
	return decodeChannelMessages[T](stepCtx.persistence.objectCodec, stepCtx.results.GetChannelMessages(ch.Name))
}

// Publish buffers messages flushed when the step method completes.
func (ch Channel[T]) Publish(ctx Context, values ...T) error {
	stepCtx, err := asStepContext(ctx)
	if err != nil {
		return err
	}
	return stepCtx.persistence.PublishToChannel(NewChannelMessage(ch, values...))
}

// DynamicChannel represents a family of typed channels sharing a prefix.
type DynamicChannel[T any] struct {
	Prefix string
}

// NewDynamicChannel creates a dynamic channel family.
func NewDynamicChannel[T any](prefix string) DynamicChannel[T] {
	return DynamicChannel[T]{Prefix: prefix}
}

// Condition creates a wait condition for a dynamic channel instance.
func (dc DynamicChannel[T]) Condition(key string) SingleCondition {
	return channelCondition{ChannelName: dynamicChannelName(dc.Prefix, key), Min: 1}
}

// ConditionWithMinMax creates a wait condition with explicit min/max.
func (dc DynamicChannel[T]) ConditionWithMinMax(key string, min, max int) SingleCondition {
	return channelCondition{ChannelName: dynamicChannelName(dc.Prefix, key), Min: min, Max: max}
}

// GetConsumedMessages returns typed messages for a dynamic channel instance.
func (dc DynamicChannel[T]) GetConsumedMessages(ctx Context, key string) ([]T, error) {
	stepCtx, err := asStepContext(ctx)
	if err != nil {
		return nil, err
	}
	channelName := dynamicChannelName(dc.Prefix, key)
	if err := stepCtx.persistence.validateChannel(channelName); err != nil {
		return nil, err
	}
	return decodeChannelMessages[T](stepCtx.persistence.objectCodec, stepCtx.results.GetChannelMessages(channelName))
}

// Publish buffers messages for a dynamic channel instance.
func (dc DynamicChannel[T]) Publish(ctx Context, key string, values ...T) error {
	stepCtx, err := asStepContext(ctx)
	if err != nil {
		return err
	}
	return stepCtx.persistence.PublishToChannel(NewDynamicChannelMessage(dc, key, values...))
}

func dynamicChannelName(prefix, key string) string {
	return prefix + key
}
