// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package dex

import "github.com/superdurable/dex/sdk-go/gen/dexpb"

// Attribute defines a typed persisted value.
// String values require valid UTF-8; use []byte for arbitrary bytes.
type Attribute[T any] struct {
	name  string
	index *AttributeIndex
}

func DefineAttribute[T any](key string, options ...AttributeOption) Attribute[T] {
	config := applyAttributeOptions(options)
	return Attribute[T]{name: key, index: config.index}
}

// AttributeDef is the interface of Attribute, without Go's generic
// So that internal sdk can use it to workaround Go's generic limitations
type AttributeDef interface {
	attributeName() string
	attributeIndex() *AttributeIndex
	attributeIsMap() bool
}

// Get returns the current value. Missing values return AttributeNotFoundError.
func (a Attribute[T]) Get(ctx Context) (value T, err error) {
	invocation, ok := ctx.(attributeInvocation)
	if !ok {
		return value, errInvalidInvocationContext
	}
	found, err := invocation.getAttribute(a.name, &value)
	if err != nil {
		return value, err
	}
	if !found {
		return value, &AttributeNotFoundError{AttributeName: a.name}
	}
	return value, nil
}

func (a Attribute[T]) Set(ctx Context, value T) error {
	invocation, ok := ctx.(attributeInvocation)
	if !ok {
		return errInvalidInvocationContext
	}
	return invocation.setAttribute(a.name, value, a.index)
}

func (a Attribute[T]) Delete(ctx Context) error {
	invocation, ok := ctx.(attributeInvocation)
	if !ok {
		return errInvalidInvocationContext
	}
	return invocation.deleteAttribute(a.name, a.index)
}

func (a Attribute[T]) AttributeName() string {
	return a.name
}

func (a Attribute[T]) attributeName() string {
	return a.name
}

func (a Attribute[T]) attributeIndex() *AttributeIndex {
	return a.index
}

func (Attribute[T]) attributeIsMap() bool {
	return false
}

// AttributeMap defines keyed typed persisted values.
// String values require valid UTF-8; use []byte for arbitrary bytes.
type AttributeMap[T any] struct {
	name  string
	index *AttributeIndex
}

func DefineAttributeMap[T any](name string, options ...AttributeOption) AttributeMap[T] {
	config := applyAttributeOptions(options)
	return AttributeMap[T]{name: name, index: config.index}
}

// Get returns the current map value. Missing instances return AttributeNotFoundError.
func (a AttributeMap[T]) Get(
	ctx Context,
	instance string,
) (value T, err error) {
	invocation, ok := ctx.(attributeInvocation)
	if !ok {
		return value, errInvalidInvocationContext
	}
	found, err := invocation.getAttributeMap(a.name, instance, &value)
	if err != nil {
		return value, err
	}
	if !found {
		return value, &AttributeNotFoundError{
			AttributeName: a.name,
			Instance:      instance,
		}
	}
	return value, nil
}

func (a AttributeMap[T]) Set(ctx Context, instance string, value T) error {
	invocation, ok := ctx.(attributeInvocation)
	if !ok {
		return errInvalidInvocationContext
	}
	return invocation.setAttributeMap(a.name, instance, value, a.index)
}

func (a AttributeMap[T]) Delete(ctx Context, instance string) error {
	invocation, ok := ctx.(attributeInvocation)
	if !ok {
		return errInvalidInvocationContext
	}
	return invocation.deleteAttributeMap(a.name, instance, a.index)
}

func (a AttributeMap[T]) AttributeName() string {
	return a.name
}

func (a AttributeMap[T]) attributeName() string {
	return a.name
}

func (a AttributeMap[T]) attributeIndex() *AttributeIndex {
	return a.index
}

func (AttributeMap[T]) attributeIsMap() bool {
	return true
}

type AttributeOption interface {
	applyAttribute(*attributeConfig)
}

type IndexType uint8

const (
	IndexKeyword IndexType = iota + 1
	IndexFullText
	IndexKeywordArray
	IndexInt
	IndexDouble
	IndexBool
	IndexDatetime
)

// AttributeIndex configures visibility indexing; datetime values use time.Time or RFC3339Nano strings.
type AttributeIndex struct {
	Type     IndexType
	IndexKey string
}

func Indexed(index AttributeIndex) AttributeOption {
	return indexedAttributeOption{index: index}
}

type AttributeLock interface {
	attributeLock()
}

func LockAttribute[T any](attribute Attribute[T]) AttributeLock {
	return attributeLock{name: attribute.name}
}

func LockAttributeMap[T any](
	attribute AttributeMap[T],
	instance string,
) AttributeLock {
	return attributeLock{name: attribute.name, instance: instance, isMap: true}
}

func InitialAttribute[T any](
	attribute Attribute[T],
	value T,
) (InitialAttributeDef, error) {
	encoded, indexConfig, err := encodeAttributeValue(value, attribute.index)
	if err != nil {
		return nil, err
	}
	return initialAttribute{
		name:        attribute.name,
		value:       value,
		index:       attribute.index,
		encoded:     encoded,
		indexConfig: indexConfig,
	}, nil
}

type InitialAttributeDef interface {
	initialAttribute()
}

func InitialAttributeMapValue[T any](
	attribute AttributeMap[T],
	instance string,
	value T,
) (InitialAttributeDef, error) {
	encoded, indexConfig, err := encodeAttributeValue(value, attribute.index)
	if err != nil {
		return nil, err
	}
	return initialAttribute{
		name:        attribute.name,
		instance:    instance,
		value:       value,
		index:       attribute.index,
		isMap:       true,
		encoded:     encoded,
		indexConfig: indexConfig,
	}, nil
}

type attributeInvocation interface {
	getAttribute(name string, valuePtr any) (bool, error)
	setAttribute(name string, value any, index *AttributeIndex) error
	deleteAttribute(name string, index *AttributeIndex) error
	getAttributeMap(name string, instance string, valuePtr any) (bool, error)
	setAttributeMap(name string, instance string, value any, index *AttributeIndex) error
	deleteAttributeMap(name string, instance string, index *AttributeIndex) error
}

type attributeConfig struct {
	index *AttributeIndex
}

func applyAttributeOptions(options []AttributeOption) attributeConfig {
	var config attributeConfig
	for _, option := range options {
		option.applyAttribute(&config)
	}
	return config
}

type indexedAttributeOption struct {
	index AttributeIndex
}

func (option indexedAttributeOption) applyAttribute(config *attributeConfig) {
	index := option.index
	config.index = &index
}

type attributeLock struct {
	name     string
	instance string
	isMap    bool
}

func (attributeLock) attributeLock() {}

type initialAttribute struct {
	name        string
	instance    string
	value       any
	index       *AttributeIndex
	isMap       bool
	encoded     *dexpb.Value
	indexConfig *dexpb.IndexConfig
}

func (initialAttribute) initialAttribute() {}
