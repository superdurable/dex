// Copyright (c) 2025 superdurable
// SPDX-License-Identifier: MIT

package ptr

// Any returns a pointer to the given value
func Any[T any](obj T) *T {
	return &obj
}
