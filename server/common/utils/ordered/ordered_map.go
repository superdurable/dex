package ordered

import "iter"

type OrderedMap[K comparable, V any] struct {
	keys   []K
	values map[K]V
}

func NewOrderedMap[K comparable, V any]() *OrderedMap[K, V] {
	return &OrderedMap[K, V]{
		keys:   make([]K, 0),
		values: make(map[K]V),
	}
}

func (om *OrderedMap[K, V]) Set(key K, value V) {
	if _, exists := om.values[key]; !exists {
		om.keys = append(om.keys, key)
	}
	om.values[key] = value
}

func (om *OrderedMap[K, V]) Get(key K) (V, bool) {
	value, exists := om.values[key]
	return value, exists
}

func (om *OrderedMap[K, V]) Delete(key K) {
	if _, exists := om.values[key]; exists {
		delete(om.values, key)
		for i, k := range om.keys {
			if k == key {
				om.keys = append(om.keys[:i], om.keys[i+1:]...)
				break
			}
		}
	}
}

func (om *OrderedMap[K, V]) Keys() []K {
	return om.keys
}

func (om *OrderedMap[K, V]) Len() int {
	return len(om.keys)
}

func (om *OrderedMap[K, V]) Range(fn func(key K, value V) bool) {
	for _, key := range om.keys {
		if value, exists := om.values[key]; exists {
			if !fn(key, value) {
				break
			}
		}
	}
}

func (om *OrderedMap[K, V]) Iterate() iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		for _, key := range om.keys {
			if value, exists := om.values[key]; exists {
				if !yield(key, value) {
					return
				}
			}
		}
	}
}

func (om *OrderedMap[K, V]) Clone() *OrderedMap[K, V] {
	new := &OrderedMap[K, V]{
		keys:   make([]K, len(om.keys)),
		values: make(map[K]V, len(om.values)),
	}

	copy(new.keys, om.keys)

	for k, v := range om.values {
		new.values[k] = v
	}

	return new
}
