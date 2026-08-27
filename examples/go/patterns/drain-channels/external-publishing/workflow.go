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

package externalpublishing

import (
	"fmt"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

const QueueChannelName = "queueChannel"

var QueueChannel = dex.DefineChannel[string](QueueChannelName)

type DrainingChannelFlow struct {
	dex.FlowDefaults
}

func NewDrainingChannelFlow() *DrainingChannelFlow {
	return &DrainingChannelFlow{}
}

func (*DrainingChannelFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(processMessageStep{}),
	}
}

func (*DrainingChannelFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Channels: []dex.ChannelDef{QueueChannel},
	}
}

type processMessageStep struct {
	dex.StepDefaults
}

func (processMessageStep) WaitFor(
	_ dex.Context,
	input string,
) (*dex.Wait, error) {
	if input == "" {
		return dex.Until(QueueChannel.ForOne()), nil
	}
	return dex.SkipWaitImmediately(), nil
}

func (processMessageStep) Execute(
	ctx dex.Context,
	input string,
) (*dex.StepDecision, error) {
	if input != "" {
		fmt.Printf("DrainingChannelFlow process message: %s\n", input)
	} else {
		values, err := QueueChannel.GetConditionResults(ctx)
		if err != nil {
			return nil, err
		}
		if len(values) == 0 {
			return nil, fmt.Errorf("no channel message found")
		}
		fmt.Printf("DrainingChannelFlow process message: %s\n", values[0])
	}
	time.Sleep(20 * time.Second)
	return dex.ForceCompleteIfChannelsEmpty(
		nil,
		[]dex.ChannelDef{QueueChannel},
		dex.MovementOf(processMessageStep{}, ""),
	), nil
}

var _ dex.Flow = (*DrainingChannelFlow)(nil)
