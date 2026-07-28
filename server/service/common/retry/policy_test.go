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

package retry

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
)

func TestConvertCadenceActivityRetryPolicyNilMatchesTemporalDefaults(t *testing.T) {
	policy := ConvertCadenceActivityRetryPolicy(nil)
	require.NotNil(t, policy)
	require.Equal(t, time.Second, policy.InitialInterval)
	require.Equal(t, time.Second*100, policy.MaximumInterval)
	require.Equal(t, int32(0), policy.MaximumAttempts)
	require.Equal(t, 2.0, policy.BackoffCoefficient)
	require.Equal(t, time.Hour*24*365, policy.ExpirationInterval)
}

func TestConvertCadenceActivityRetryPolicyHonorsExplicitMaximumAttempts(t *testing.T) {
	policy := ConvertCadenceActivityRetryPolicy(&dexpb.RetryPolicy{MaximumAttempts: 1})
	require.NotNil(t, policy)
	require.Equal(t, int32(1), policy.MaximumAttempts)
}

func TestConvertTemporalActivityRetryPolicyNilStaysNil(t *testing.T) {
	require.Nil(t, ConvertTemporalActivityRetryPolicy(nil))
}
