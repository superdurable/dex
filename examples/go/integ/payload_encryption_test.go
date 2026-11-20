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

package integ

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/blob-cache-go/blobcache"
	payloadencryption "github.com/superdurable/dex/examples/go/production/payload-encryption"
	"github.com/superdurable/dex/sdk-go/dex"
)

func TestPayloadEncryptionStoresTicketMessageEncrypted(t *testing.T) {
	cache, err := blobcache.New(&blobcache.Config{Dir: t.TempDir(), MaxBytes: 64 << 20})
	require.NoError(t, err)
	keyFilePath := writePayloadKeyFile(t)
	flow, worker, client, workerResult, err := payloadencryption.StartEncryptedSupportTicket(
		cache, flowServiceAddress(), keyFilePath,
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		stopContext, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		require.NoError(t, payloadencryption.StopEncryptedSupportTicket(stopContext, worker, client, workerResult))
		require.NoError(t, cache.Close())
	})

	flowID := newFlowID(t, "support-ticket")
	_, err = client.StartFlow(integrationContext(t), flow, flowID, "My card was charged twice.", dex.StartFlowOptions{})
	require.NoError(t, err)
	result, err := client.WaitForFlow(integrationContext(t), flowID, dex.WaitForFlowOptions{NeedsResults: true})
	require.NoError(t, err)
	require.Equal(t, dex.FlowCompleted, result.Status)
	var message string
	require.NoError(t, client.InvokeRPC(integrationContext(t), flowID, flow.GetTicketMessage, nil, &message))
	require.Equal(t, "My card was charged twice.", message)

	registry, err := dex.NewRegistry([]dex.Flow{&payloadencryption.SupportTicketFlow{}})
	require.NoError(t, err)
	clientWithoutCodec, err := dex.NewClient(registry, cache, dex.ClientOptions{FlowServiceAddress: flowServiceAddress()})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, clientWithoutCodec.Close()) })
	var status string
	_, err = clientWithoutCodec.GetAttributes(
		integrationContext(t), flowID, dex.ReadAttribute(payloadencryption.TicketStatus, &status),
	)
	require.NoError(t, err)
	require.Equal(t, "received", status)
	var storedMessage string
	_, err = clientWithoutCodec.GetAttributes(
		integrationContext(t), flowID, dex.ReadAttribute(payloadencryption.TicketMessage, &storedMessage),
	)
	require.ErrorIs(t, err, dex.ErrPayloadCodecNotConfigured)
}

// writePayloadKeyFile writes a key file in the dexcli codec-server keygen format.
func writePayloadKeyFile(t *testing.T) string {
	t.Helper()
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	content, err := json.Marshal(map[string]any{
		"current_key_id": "k1",
		"keys":           map[string]string{"k1": base64.StdEncoding.EncodeToString(key)},
	})
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "payload-keys.json")
	require.NoError(t, os.WriteFile(path, content, 0o600))
	return path
}
