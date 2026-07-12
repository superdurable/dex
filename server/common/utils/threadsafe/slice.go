package threadsafe

import (
	"slices"
	"sync"
)

type Slice[T any] struct {
	slice []T
	mu    sync.RWMutex
}

func NewThreadSafeSlice[T any]() *Slice[T] {
	return &Slice[T]{slice: make([]T, 0)}
}

func (s *Slice[T]) Append(item T) {
	s.mu.Lock()
	s.slice = append(s.slice, item)
	s.mu.Unlock()
}

func (s *Slice[T]) GetOne(index int) (T, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if index < 0 || index >= len(s.slice) {
		var zero T
		return zero, false
	}
	return s.slice[index], true
}

func (s *Slice[T]) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.slice)
}

func (s *Slice[T]) GetAllUnsafe() []T {
	return s.slice
}

func (s *Slice[T]) Snapshot() []T {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := slices.Clone(s.slice)
	return cp
}
