// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package dex

import (
	"fmt"

	"github.com/google/uuid"
)

func newRequestID() (string, error) {
	requestID, err := uuid.NewRandom()
	if err != nil {
		return "", fmt.Errorf("dex: generate request ID: %w", err)
	}
	return requestID.String(), nil
}
