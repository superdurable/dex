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

package quickstart

import (
	"context"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	dexagents "github.com/superdurable/dex/agents/go/core"
	dexeino "github.com/superdurable/dex/agents/go/eino"

	"github.com/superdurable/dex/examples/go/agent-harnesses/orderdesk"
)

// EinoQuickStartFlowType is the Flow type of the quick start's durable Eino agent.
const EinoQuickStartFlowType = "EinoQuickStart"

type lookUpOrderInput struct {
	OrderID string `json:"order_id" jsonschema:"description=The order ID, such as 1001"`
}

type cancelOrderInput struct {
	OrderID string `json:"order_id" jsonschema:"description=The order ID, such as 1002"`
	Reason  string `json:"reason" jsonschema:"description=Why the customer cancels the order"`
}

// NewPlainEinoAgent returns the agent as Eino alone writes it. An adk.Runner runs it in this process.
func NewPlainEinoAgent(
	ctx context.Context, orderDesk *orderdesk.OrderDesk, chatModel model.ToolCallingChatModel,
) (*adk.ChatModelAgent, error) {
	config, err := newEinoAgentConfig(orderDesk, chatModel)
	if err != nil {
		return nil, err
	}
	return adk.NewChatModelAgent(ctx, config)
}

// NewDurableEinoAgent returns the same agent as a durable Dex agent session: the same config, plus tool effects.
func NewDurableEinoAgent(
	orderDesk *orderdesk.OrderDesk, chatModel model.ToolCallingChatModel,
) (*dexagents.AgentSessionFlow, error) {
	config, err := newEinoAgentConfig(orderDesk, chatModel)
	if err != nil {
		return nil, err
	}
	return dexeino.NewAgentSessionFlow(config, EinoQuickStartFlowType,
		dexeino.WithToolEffects(map[string]dexagents.ToolEffect{
			"look_up_order": dexagents.ToolEffectRead,
			"cancel_order":  dexagents.ToolEffectNonIdempotentWrite,
		}))
}

func newEinoAgentConfig(
	orderDesk *orderdesk.OrderDesk, chatModel model.ToolCallingChatModel,
) (*adk.ChatModelAgentConfig, error) {
	lookUpOrder, err := utils.InferTool("look_up_order",
		"Look up one order by its order ID and return its items and status.",
		func(ctx context.Context, input lookUpOrderInput) (string, error) {
			return orderDesk.DescribeOrder(ctx, input.OrderID)
		})
	if err != nil {
		return nil, err
	}
	cancelOrder, err := utils.InferTool("cancel_order", "Cancel one order that has not shipped yet.",
		func(ctx context.Context, input cancelOrderInput) (string, error) {
			return orderDesk.CancelOrder(ctx, input.OrderID, input.Reason)
		})
	if err != nil {
		return nil, err
	}
	return &adk.ChatModelAgentConfig{
		Name:        "order_support",
		Description: "Answers questions about orders and cancels them.",
		Instruction: orderdesk.OrderSupportInstructions,
		Model:       chatModel,
		ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{
			Tools: []tool.BaseTool{lookUpOrder, cancelOrder},
		}},
	}, nil
}
