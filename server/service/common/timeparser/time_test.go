// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

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
