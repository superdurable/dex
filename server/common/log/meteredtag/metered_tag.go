package meteredtag

import (
	"github.com/superdurable/dex/server/common/log/tag"
	"github.com/superdurable/dex/server/internal/metrics"
)

// The package is used to log tags that are used to monitor the system.
// Putting in a separate package to avoid circular dependency.

// CriticalErrorCodeBug is used to log errors that are more critical than normal errors
// which we should monitor on.
func CriticalErrorCodeBug() tag.Tag {
	metrics.CounterCriticalError.Inc()
	return tag.CriticalCodeBug()
}
