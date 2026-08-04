// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

package dex

import "github.com/superdurable/dex/sdk-go/logging"

// Logger receives structured SDK logs.
type Logger = logging.Logger

func resolveLogger(override Logger, fallback Logger) Logger {
	if override != nil {
		return override
	}
	return logging.OrDefault(fallback)
}
