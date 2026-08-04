// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package timeparser

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseTimeRFC3339Nano(t *testing.T) {
	dateTime := time.Date(2026, 7, 30, 10, 0, 0, 123456789, time.UTC)

	parsed, err := ParseTime(dateTime.Format(time.RFC3339Nano))
	require.NoError(t, err)
	require.Equal(t, dateTime.UnixNano(), parsed)
}

func TestParseTimeRawUnixNano(t *testing.T) {
	parsed, err := ParseTime("20240101")
	require.NoError(t, err)
	require.Equal(t, int64(20240101), parsed)

	_, err = ParseRFC3339Nano("20240101")
	require.Error(t, err)
}
