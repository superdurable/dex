// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Sustainable Use License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package common

import (
	"encoding/json"
	"fmt"
	"log"
)

func LogRequest(message string, request any) { log.Println(message, FormatForLogging(request)) }
func FormatForLogging(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("cannot encode request: %v", err)
	}
	const maxLogBytes = 4096
	if len(encoded) > maxLogBytes {
		return string(encoded[:maxLogBytes]) + "..."
	}
	return string(encoded)
}
