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

package interpreter

import (
	"github.com/superdurable/dex/service/interpreter/interfaces"
)

const globalChangeId = "global"

// StartingVersionV1 is the reset baseline for the rewritten interpreter. All
// historical version branches were removed; running flows always execute the
// latest behavior. Future determinism-affecting changes add a new constant here
// and gate on it via GlobalVersioner.
const StartingVersionV1 = 1

const MaxOfAllVersions = StartingVersionV1

// GlobalVersioner is the forward hook for determinism-safe interpreter changes.
// See https://stackoverflow.com/questions/73941723 for the pattern.
type GlobalVersioner struct {
	workflowProvider interfaces.WorkflowProvider
	ctx              interfaces.UnifiedContext
	version          int
}

func NewGlobalVersioner(
	workflowProvider interfaces.WorkflowProvider, ctx interfaces.UnifiedContext,
) *GlobalVersioner {
	version := workflowProvider.GetVersion(ctx, globalChangeId, 0, MaxOfAllVersions)
	return &GlobalVersioner{
		workflowProvider: workflowProvider,
		ctx:              ctx,
		version:          version,
	}
}
