// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package common

import (
	"log"

	commonlog "github.com/superdurable/dex/service/common/log"
)

func LogRequest(message string, request any) {
	log.Println(message, FormatForLogging(request))
}

func FormatForLogging(value any) string {
	return commonlog.ToJsonAndTruncateForLogging(value)
}
