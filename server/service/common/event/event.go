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

package event

import "github.com/superdurable/dex/gen/dexpb"

// Event is a lightweight server-side observability hook payload (not an IDL type).
type Event struct {
	FlowId             string
	RunId              string
	FlowType           string
	StepType           string
	StepExecutionId    string
	RpcName            string
	EventType          string
	StartTimestampInMs int64
	Attributes         []*dexpb.KV
}

// HandleEventFunc must be lightweight, reliable, and fast (<1s).
type HandleEventFunc func(event Event)

var Handle HandleEventFunc = DefaultHandleEventFunc

func SetHandleEventFunc(handler HandleEventFunc) {
	Handle = handler
}

func DefaultHandleEventFunc(event Event) {
	// Noop by default
}
