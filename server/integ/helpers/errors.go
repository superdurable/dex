// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

package helpers

import "testing"

func FailTestWithError(error error, t *testing.T) {
	t.Fatalf("%s - Test failed with error: %v", t.Name(), error)
}

func FailTestWithErrorMessage(errorMessage string, t *testing.T) {
	t.Fatalf("%s - Test failed with error: %s", t.Name(), errorMessage)
}
