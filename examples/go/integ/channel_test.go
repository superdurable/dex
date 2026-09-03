// Copyright (c) 2026 Super Durable, Inc.
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

package integ

import (
	"testing"

	"github.com/stretchr/testify/require"
	channelprimitive "github.com/superdurable/dex/examples/go/primitives/channel"
	"github.com/superdurable/dex/examples/go/registry"
	"github.com/superdurable/dex/sdk-go/dex"
)

func TestChannelMessageCanBeMovedByID(t *testing.T) {
	ctx := integrationContext(t)
	flowID := newFlowID(t, "channel-message")
	_, err := integClient.StartFlow(
		ctx,
		registry.Channel,
		flowID,
		30,
		dex.StartFlowOptions{},
	)
	require.NoError(t, err)

	require.NoError(t, integClient.PublishToChannel(ctx, flowID, channelprimitive.Queued, "delete me"))
	require.NoError(t, integClient.PublishToChannel(ctx, flowID, channelprimitive.Queued, "move me"))

	var pending []dex.ChannelMessage[string]
	require.NoError(t, integClient.GetChannelMessages(ctx, flowID, channelprimitive.Queued, &pending))
	require.Len(t, pending, 2)
	require.Equal(t, []string{"delete me", "move me"}, []string{pending[0].Value, pending[1].Value})
	require.NoError(t, integClient.DeleteChannelMessage(ctx, flowID, channelprimitive.Queued, pending[0].MessageID))

	move := channelprimitive.MoveMessage{MessageID: pending[1].MessageID}
	var none dex.None
	require.NoError(t, integClient.InvokeRPC(
		ctx,
		flowID,
		registry.Channel.Move,
		move,
		&none,
		dex.InvokeOptions{IsTransactional: true, LoadChannels: []dex.ChannelDef{channelprimitive.Queued}},
	))

	var moved []dex.ChannelMessage[string]
	require.NoError(t, integClient.GetChannelMessages(ctx, flowID, channelprimitive.Moved, &moved))
	require.Len(t, moved, 1)
	require.Equal(t, []string{"move me"}, []string{moved[0].Value})

	err = integClient.InvokeRPC(
		ctx,
		flowID,
		registry.Channel.Move,
		move,
		&none,
		dex.InvokeOptions{IsTransactional: true, LoadChannels: []dex.ChannelDef{channelprimitive.Queued}},
	)
	var notFound *dex.ChannelMessageNotFoundError
	require.ErrorAs(t, err, &notFound)
	require.NoError(t, integClient.GetChannelMessages(ctx, flowID, channelprimitive.Moved, &moved))
	require.Equal(t, []string{"move me"}, []string{moved[0].Value})

	require.NoError(t, integClient.InvokeRPC(
		ctx,
		flowID,
		registry.Channel.Approve,
		nil,
		&none,
		dex.InvokeOptions{},
	))
}
