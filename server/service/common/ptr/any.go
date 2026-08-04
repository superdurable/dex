// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

package ptr

func Any[T any](obj T) *T {
	return &obj
}
