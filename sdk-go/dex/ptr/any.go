// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package ptr

// Any returns a pointer to a copy of obj.
//
// It is useful for optional SDK fields whose literal values cannot otherwise be
// addressed directly. Mutating the returned value does not mutate the original.
//
//	options := dex.StartFlowOptions{RequestID: ptr.Any("request-42")}
func Any[T any](obj T) *T {
	return &obj
}
