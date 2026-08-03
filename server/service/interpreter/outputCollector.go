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

import "github.com/superdurable/dex/gen/dexpb"

type OutputCollector struct {
	outputs []*dexpb.StepCompletionOutput
}

func NewOutputCollector(initOutputs []*dexpb.StepCompletionOutput) *OutputCollector {
	collector := &OutputCollector{}
	for _, output := range initOutputs {
		collector.Add(output)
	}
	return collector
}

func (o *OutputCollector) Add(
	output *dexpb.StepCompletionOutput,
) {
	if output == nil || output.GetCompletedStepOutput().GetKind() == nil {
		return
	}
	o.outputs = append(o.outputs, output)
}

func (o *OutputCollector) GetAll() []*dexpb.StepCompletionOutput {
	return o.outputs
}
