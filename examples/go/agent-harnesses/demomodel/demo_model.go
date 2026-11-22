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

// Tests run against the Dex server at DEX_FLOW_SERVICE_ADDRESS.

// Package demomodel is a local demo model for the order support agents, so they run without a provider key.
//
// The demo model reads the turn's user message and the tool results the turn
// already has, so a retried model call gets the same reply:
//
//   - "reply in Slack thread <channel ID> <thread timestamp>: <text>" calls
//     slack_post_thread_reply, the Slack Connector tool.
//   - "cancel order <id>" calls cancel_order.
//   - Any other text with "order <id>" calls look_up_order.
//   - After the tool results arrive, it answers with them.
package demomodel

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

const (
	// DemoModelName selects the demo model; any name with DemoModelPrefix does.
	DemoModelName = "mock/order-support"
	// DemoModelPrefix starts the name of every local demo model.
	DemoModelPrefix = "mock/"
	// DemoReplyPrefix starts every answer of the demo model.
	DemoReplyPrefix = "Local demo response: "
	// DemoCancelReason is the reason the demo model gives cancel_order.
	DemoCancelReason = "requested by the customer"
	// SlackPostThreadReplyToolName is the tool name of the Slack Connector's postThreadReply operation.
	SlackPostThreadReplyToolName = "slack_post_thread_reply"
)

var (
	slackThreadReplyPattern = regexp.MustCompile(`(?is)\breply in slack thread (\S+) (\S+?): (.+)`)
	cancelOrderPattern      = regexp.MustCompile(`(?i)\bcancel order (\w+)`)
	orderPattern            = regexp.MustCompile(`(?i)\border (\w+)`)
)

// IsDemoModel reports whether modelName selects a local demo model.
func IsDemoModel(modelName string) bool {
	return strings.HasPrefix(modelName, DemoModelPrefix)
}

// OrderSupportDemoModel is an Eino ToolCallingChatModel whose reply depends only on its input.
type OrderSupportDemoModel struct{}

// NewOrderSupportDemoModel returns the demo model.
func NewOrderSupportDemoModel() *OrderSupportDemoModel {
	return &OrderSupportDemoModel{}
}

// Generate returns the demo reply to input: one tool call, or the answer once the turn has tool results.
//
// It returns an error only when the tool arguments cannot be encoded.
func (demo *OrderSupportDemoModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	return planDemoReply(input)
}

// Stream returns the reply of Generate as a stream of one message.
func (demo *OrderSupportDemoModel) Stream(
	ctx context.Context, input []*schema.Message, options ...model.Option,
) (*schema.StreamReader[*schema.Message], error) {
	message, err := demo.Generate(ctx, input, options...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{message}), nil
}

// WithTools returns the demo model itself: its replies name the order support tools directly.
func (demo *OrderSupportDemoModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return demo, nil
}

func planDemoReply(input []*schema.Message) (*schema.Message, error) {
	lastUserIndex := -1
	userCount := 0
	for index, message := range input {
		if message.Role == schema.User {
			lastUserIndex = index
			userCount++
		}
	}
	if lastUserIndex < 0 {
		return schema.AssistantMessage(DemoReplyPrefix+"send a message first.", nil), nil
	}
	var toolOutputs []string
	for _, message := range input[lastUserIndex+1:] {
		if message.Role == schema.Tool {
			toolOutputs = append(toolOutputs, message.Content)
		}
	}
	if len(toolOutputs) > 0 {
		return schema.AssistantMessage(DemoReplyPrefix+strings.Join(toolOutputs, " "), nil), nil
	}
	userText := input[lastUserIndex].Content
	if match := slackThreadReplyPattern.FindStringSubmatch(userText); match != nil {
		return demoToolCall(userCount, SlackPostThreadReplyToolName,
			map[string]string{"channelId": match[1], "threadTimestamp": match[2], "text": match[3]})
	}
	if match := cancelOrderPattern.FindStringSubmatch(userText); match != nil {
		return demoToolCall(userCount, "cancel_order", map[string]string{"order_id": match[1], "reason": DemoCancelReason})
	}
	if match := orderPattern.FindStringSubmatch(userText); match != nil {
		return demoToolCall(userCount, "look_up_order", map[string]string{"order_id": match[1]})
	}
	usage := DemoReplyPrefix + "ask me to look up or cancel an order, such as 'Look up order 1001'."
	return schema.AssistantMessage(usage, nil), nil
}

func demoToolCall(userCount int, toolName string, arguments map[string]string) (*schema.Message, error) {
	encodedArguments, err := json.Marshal(arguments)
	if err != nil {
		return nil, fmt.Errorf("encode the %s arguments: %w", toolName, err)
	}
	call := schema.ToolCall{
		ID:       fmt.Sprintf("call-%d-%s", userCount, toolName),
		Type:     "function",
		Function: schema.FunctionCall{Name: toolName, Arguments: string(encodedArguments)},
	}
	return schema.AssistantMessage("", []schema.ToolCall{call}), nil
}
