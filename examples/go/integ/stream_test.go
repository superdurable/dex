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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	streamprimitive "github.com/superdurable/dex/examples/go/primitives/stream"
	"github.com/superdurable/dex/examples/go/registry"
	"github.com/superdurable/dex/sdk-go/dex"
)

func TestStreamResumesAfterStepAndClientWrites(t *testing.T) {
	ctx := integrationContext(t)
	flowID := newFlowID(t, "stream")
	_, err := integClient.StartFlow(
		ctx,
		registry.Stream,
		flowID,
		"invoice",
		dex.StartFlowOptions{},
	)
	require.NoError(t, err)

	var stepValue string
	stepMessage, err := integClient.ReadStream(
		ctx,
		flowID,
		streamprimitive.Progress,
		"",
		&stepValue,
	)
	require.NoError(t, err)
	require.Equal(t, "Rendering preview for invoicePreview ready for invoice", stepValue)
	require.NotEmpty(t, stepMessage.ResumeToken)
	require.True(t, strings.HasPrefix(stepMessage.Source, "#"))

	err = integClient.WriteStream(
		ctx,
		flowID,
		streamprimitive.Progress,
		"browser/complete",
		"Preview displayed",
	)
	require.NoError(t, err)

	var clientValue string
	clientMessage, err := integClient.ReadStream(
		ctx,
		flowID,
		streamprimitive.Progress,
		stepMessage.ResumeToken,
		&clientValue,
	)
	require.NoError(t, err)
	require.Equal(t, "Preview displayed", clientValue)
	require.Equal(t, "browser/complete", clientMessage.Source)
}
