// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dex

type Channel[T any] struct {
	name string
}

func DefineChannel[T any](name string) Channel[T] {
	return Channel[T]{name: name}
}

func (c Channel[T]) Publish(ctx Context, value T) error {
	invocation, ok := ctx.(channelInvocation)
	if !ok {
		return errInvalidInvocationContext
	}
	return invocation.publishChannel(c.name, value)
}

func (c Channel[T]) ForOne(options ...Condition) Condition {
	return newChannelCondition(c.name, "", false, nil, nil, options)
}

func (c Channel[T]) ForN(count int, options ...Condition) Condition {
	return newChannelCondition(c.name, "", false, &count, &count, options)
}

func (c Channel[T]) AtLeast(count int, options ...Condition) Condition {
	return newChannelCondition(c.name, "", false, &count, nil, options)
}

func (c Channel[T]) AtMost(count int, options ...Condition) Condition {
	return newChannelCondition(c.name, "", false, nil, &count, options)
}

func (c Channel[T]) AtLeastAtMost(
	atLeast int,
	atMost int,
	options ...Condition,
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

func (c Channel[T]) Size(ctx Context) int {
	invocation, ok := ctx.(channelInvocation)
	if !ok {
		panic(errInvalidInvocationContext)
	}
	return invocation.channelSize(c.name)
}

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

func (c Channel[T]) ChannelName() string {
	return c.name
}

func (Channel[T]) channelDefinition() {}

type ChannelMap[T any] struct {
	name string
}

func DefineChannelMap[T any](name string) ChannelMap[T] {
	return ChannelMap[T]{name: name}
}

func (c ChannelMap[T]) Publish(ctx Context, instance string, value T) error {
	invocation, ok := ctx.(channelInvocation)
	if !ok {
		return errInvalidInvocationContext
	}
	return invocation.publishChannelMap(c.name, instance, value)
}

func (c ChannelMap[T]) ForOne(
	instance string,
	options ...Condition,
) Condition {
	return newChannelCondition(c.name, instance, true, nil, nil, options)
}

func (c ChannelMap[T]) ForN(
	instance string,
	count int,
	options ...Condition,
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

func (c ChannelMap[T]) AtLeast(
	instance string,
	count int,
	options ...Condition,
) Condition {
	return newChannelCondition(c.name, instance, true, &count, nil, options)
}

func (c ChannelMap[T]) AtMost(
	instance string,
	count int,
	options ...Condition,
) Condition {
	return newChannelCondition(c.name, instance, true, nil, &count, options)
}

func (c ChannelMap[T]) AtLeastAtMost(
	instance string,
	atLeast int,
	atMost int,
	options ...Condition,
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

func (c ChannelMap[T]) Size(ctx Context, instance string) int {
	invocation, ok := ctx.(channelInvocation)
	if !ok {
		panic(errInvalidInvocationContext)
	}
	return invocation.channelMapSize(c.name, instance)
}

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

func (c ChannelMap[T]) ChannelName() string {
	return c.name
}

func (ChannelMap[T]) channelDefinition() {}

type channelInvocation interface {
	publishChannel(name string, value any) error
	publishChannelMap(name string, instance string, value any) error
	channelSize(name string) int
	channelMapSize(name string, instance string) int
	getChannelResults(
		name string,
		instance string,
		isMap bool,
		resultsPtr any,
	) error
}
