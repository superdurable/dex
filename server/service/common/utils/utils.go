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

package utils

import (
	"context"
	"net/http"
	"time"

	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	defaultMaxApiTimeoutSeconds = 60
)

func IsNullValue(value *dexpb.Value) bool {
	if value == nil {
		return true
	}
	_, ok := value.GetKind().(*dexpb.Value_NullValue)
	return ok
}

func MergeStringSlice(first, second []string) []string {
	exists := map[string]bool{}
	var out []string
	for _, k := range first {
		if !exists[k] {
			exists[k] = true
			out = append(out, k)
		}
	}
	for _, k := range second {
		if !exists[k] {
			exists[k] = true
			out = append(out, k)
		}
	}
	return out
}

func MergeMap(first map[string]interface{}, second map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(first))
	for k, v := range first {
		out[k] = v
	}

	for k, v := range second {
		out[k] = v
	}
	return out
}

func TrimRpcTimeoutSeconds(ctx context.Context, req *dexpb.InvokeRPCRequest) int32 {
	secondsRemaining := int32(defaultMaxApiTimeoutSeconds)
	ddl, ok := ctx.Deadline()
	if ok {
		timeRemaining := ddl.Sub(time.Now())
		if int32(timeRemaining.Seconds()) < secondsRemaining {
			secondsRemaining = int32(timeRemaining.Seconds())
		}
	}
	if req != nil && req.GetTimeoutSeconds() > 0 && req.GetTimeoutSeconds() < secondsRemaining {
		secondsRemaining = req.GetTimeoutSeconds()
	}
	return secondsRemaining
}

func TrimContextByTimeoutWithCappedDDL(parent context.Context, reqWaitSeconds *int32, configuredMaxSeconds int64) (context.Context, context.CancelFunc) {
	maxWaitSeconds := configuredMaxSeconds
	if maxWaitSeconds == 0 {
		maxWaitSeconds = defaultMaxApiTimeoutSeconds
	}

	if reqWaitSeconds != nil && *reqWaitSeconds > 0 && int64(*reqWaitSeconds) < maxWaitSeconds {
		maxWaitSeconds = int64(*reqWaitSeconds)
	}

	maxWaitDeadline := time.Now().Add(time.Duration(maxWaitSeconds) * time.Second)

	// then capped by context
	ddl, ok := parent.Deadline()
	if ok && ddl.Before(maxWaitDeadline) {
		maxWaitDeadline = ddl
	}

	return context.WithDeadline(parent, maxWaitDeadline)
}

func CheckHttpError(err error, httpResp *http.Response) bool {
	if err != nil || (httpResp != nil && httpResp.StatusCode != http.StatusOK) {
		return true
	}
	return false
}

func ToNanoSeconds(e *timestamppb.Timestamp) int64 {
	return e.GetSeconds()*1000*1000*1000 + int64(e.GetNanos())
}
