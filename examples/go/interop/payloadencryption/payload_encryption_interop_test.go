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

// These tests run only from examples/run-payload-encryption-interop-tests.sh.
// It starts a Dex server, one payload interop Worker per SDK, and the codec servers.
package payloadencryptioninterop_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/blob-cache-go/blobcache"
	interop "github.com/superdurable/dex/examples/go/interop/payloadencryption"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const (
	encodedTextPrefix = "dex-encoded:"
	envelopeEncoding  = "dex/1"
)

var flowCounter atomic.Int64

// TestGoClientReadsEveryWorkerPayload checks that the Go Client decrypts what each
// SDK's Worker encrypts, and that each Worker decrypts the Go Client's input.
func TestGoClientReadsEveryWorkerPayload(t *testing.T) {
	environment := loadInteropEnvironment(t)
	encryption, err := interop.NewFilePayloadEncryption(environment.keyFilePath)
	require.NoError(t, err)
	client := environment.newClient(t, encryption)
	clientWithoutCodec := environment.newClient(t, dex.PayloadEncryptionOptions{})
	flowService := environment.newRawFlowServiceClient(t)
	for _, owner := range interop.Owners {
		t.Run(string(owner), func(t *testing.T) {
			input := "Go Client input for " + string(owner) + ": naïve café ✓"
			flowID := requireCompletedFlowPayloads(t, client, owner, input)
			requireStoredAttributeEncrypted(t, flowService, flowID, interop.SecretText.AttributeName())
			requireStoredOutputEncrypted(t, flowService, flowID)
			requireHistoryHidesPlaintext(t, flowService, flowID, input)

			var status string
			_, err := clientWithoutCodec.GetAttributes(
				integrationContext(t), flowID, dex.ReadAttribute(interop.StatusText, &status),
			)
			require.NoError(t, err)
			require.Equal(t, interop.ExpectedStatusText(owner), status)
			var secret string
			_, err = clientWithoutCodec.GetAttributes(
				integrationContext(t), flowID, dex.ReadAttribute(interop.SecretText, &secret),
			)
			require.ErrorIs(t, err, dex.ErrPayloadCodecNotConfigured)

			failedFlowID := requireFailedFlowErrorText(t, client, owner, "Go Client failure input ✓")
			requireStoredErrorTextEncoded(t, flowService, failedFlowID)
		})
	}
}

// TestCodecServersDecodeAndEncodeEveryWorkerPayload runs a Go Client whose codec is a
// codec server. Each Worker decrypts the server's encoding, and the server decodes each
// Worker's output, encrypted Attribute, and error text.
func TestCodecServersDecodeAndEncodeEveryWorkerPayload(t *testing.T) {
	environment := loadInteropEnvironment(t)
	require.NotEmpty(t, environment.codecServers)
	httpClient := &http.Client{Timeout: 30 * time.Second}
	for _, endpoint := range environment.codecServers {
		t.Run(endpoint.Name, func(t *testing.T) {
			client := environment.newClient(t, dex.PayloadEncryptionOptions{
				Codec: codecServerPayloadCodec{endpoint: endpoint, httpClient: httpClient},
			})
			for _, owner := range interop.Owners {
				t.Run(string(owner), func(t *testing.T) {
					input := endpoint.Name + " codec server input for " + string(owner) + " ✓"
					requireCompletedFlowPayloads(t, client, owner, input)
					requireFailedFlowErrorText(t, client, owner, endpoint.Name+" codec server failure ✓")
				})
			}
			t.Run("rejects a request without credentials", func(t *testing.T) {
				request, err := http.NewRequestWithContext(integrationContext(t), http.MethodPost,
					endpoint.URL+"/decode", strings.NewReader(`{"context":{"flow_type":"","flow_id":"f"},"envelopes":[]}`))
				require.NoError(t, err)
				request.Header.Set("Content-Type", "application/json")
				response, err := httpClient.Do(request)
				require.NoError(t, err)
				require.NoError(t, response.Body.Close())
				require.Equal(t, http.StatusUnauthorized, response.StatusCode)
			})
			t.Run("allows CORS only from the configured origin", func(t *testing.T) {
				allowed := sendCORSPreflight(t, httpClient, endpoint.URL, endpoint.AllowedOrigin)
				require.Equal(t, endpoint.AllowedOrigin, allowed.Header.Get("Access-Control-Allow-Origin"))
				denied := sendCORSPreflight(t, httpClient, endpoint.URL, "http://untrusted.example")
				require.Empty(t, denied.Header.Get("Access-Control-Allow-Origin"))
			})
		})
	}
}

// requireCompletedFlowPayloads starts the Owner's Flow and checks its output and
// encrypted Attribute through client. It returns the Flow ID.
func requireCompletedFlowPayloads(t *testing.T, client *dex.Client, owner interop.Owner, input string) string {
	t.Helper()
	flowID := newFlowID("payload-interop-" + string(owner))
	_, err := client.StartFlow(integrationContext(t), interop.NewPayloadInteropFlow(owner), flowID, input,
		dex.StartFlowOptions{})
	require.NoError(t, err)
	result, err := client.WaitForFlow(integrationContext(t), flowID, dex.WaitForFlowOptions{NeedsResults: true})
	require.NoError(t, err)
	require.Equal(t, dex.FlowCompleted, result.Status, result.ErrorMessage)
	var output string
	require.NoError(t, result.DecodeSingleOutput(&output))
	require.Equal(t, interop.ExpectedOutput(owner, input), output)
	var secret string
	_, err = client.GetAttributes(integrationContext(t), flowID, dex.ReadAttribute(interop.SecretText, &secret))
	require.NoError(t, err)
	require.Equal(t, interop.ExpectedSecretText(owner, input), secret)
	return flowID
}

// requireFailedFlowErrorText starts the Owner's Flow with a failing input and checks
// the decoded Step error text through client. It returns the Flow ID.
func requireFailedFlowErrorText(t *testing.T, client *dex.Client, owner interop.Owner, failureInput string) string {
	t.Helper()
	flowID := newFlowID("payload-interop-failure-" + string(owner))
	_, err := client.StartFlow(integrationContext(t), interop.NewPayloadInteropFlow(owner), flowID,
		interop.FailInputPrefix+failureInput, dex.StartFlowOptions{})
	require.NoError(t, err)
	result, err := client.WaitForFlow(integrationContext(t), flowID, dex.WaitForFlowOptions{NeedsResults: true})
	require.NoError(t, err)
	require.Equal(t, dex.FlowFailed, result.Status)
	require.Contains(t, result.ErrorMessage, interop.ExpectedFailureText(owner, failureInput))
	return flowID
}

// requireStoredAttributeEncrypted reads the raw Attribute from the server and checks
// that it is a dex/1 envelope, not plaintext.
func requireStoredAttributeEncrypted(
	t *testing.T, flowService dexpb.FlowServiceClient, flowID string, attributeName string,
) {
	t.Helper()
	response, err := flowService.GetAttributes(integrationContext(t), &dexpb.GetAttributesRequest{
		FlowId: flowID,
		Keys:   []string{attributeName},
	})
	require.NoError(t, err)
	require.Len(t, response.GetAttributes(), 1)
	requireEnvelope(t, loadStoredValues(t, flowService, flowID, response.GetAttributes()[0].GetValue())[0])
}

// requireStoredOutputEncrypted reads the raw Flow result and checks that the Step
// output is a dex/1 envelope.
func requireStoredOutputEncrypted(t *testing.T, flowService dexpb.FlowServiceClient, flowID string) {
	t.Helper()
	result, err := flowService.WaitForFlow(integrationContext(t), &dexpb.WaitForFlowRequest{
		FlowId:       flowID,
		NeedsResults: true,
	})
	require.NoError(t, err)
	require.Len(t, result.GetResults(), 1)
	requireEnvelope(t, loadStoredValues(t, flowService, flowID, result.GetResults()[0].GetCompletedStepOutput())[0])
}

// requireHistoryHidesPlaintext reads every Value in the Flow's raw history, including
// the Client-encrypted start input, and checks that none of them holds plaintext.
func requireHistoryHidesPlaintext(
	t *testing.T, flowService dexpb.FlowServiceClient, flowID string, plaintexts ...string,
) {
	t.Helper()
	var values []*dexpb.Value
	request := &dexpb.GetHistoryEventsRequest{FlowId: flowID}
	for {
		response, err := flowService.GetHistoryEvents(integrationContext(t), request)
		require.NoError(t, err)
		for _, event := range response.GetEvents() {
			values = appendNestedValues(values, event.ProtoReflect())
		}
		if len(response.GetNextPageToken()) == 0 {
			break
		}
		request.NextPageToken = response.GetNextPageToken()
	}
	envelopeCount := 0
	for _, value := range loadStoredValues(t, flowService, flowID, values...) {
		if value.GetObjValue().GetEncoding() == envelopeEncoding {
			envelopeCount++
			continue
		}
		storedText := value.GetStringValue() + string(value.GetObjValue().GetPayload())
		for _, plaintext := range plaintexts {
			require.NotContains(t, storedText, plaintext, "history stores plaintext")
		}
	}
	t.Logf("history holds %d Values, %d of them dex/1 envelopes", len(values), envelopeCount)
	require.Positive(t, envelopeCount, "history holds no dex/1 envelope")
}

// appendNestedValues appends every dex.Value inside message, at any depth.
func appendNestedValues(values []*dexpb.Value, message protoreflect.Message) []*dexpb.Value {
	if value, isValue := message.Interface().(*dexpb.Value); isValue {
		return append(values, value)
	}
	message.Range(func(field protoreflect.FieldDescriptor, fieldValue protoreflect.Value) bool {
		if field.Message() == nil {
			return true
		}
		switch {
		case field.IsList():
			for index := 0; index < fieldValue.List().Len(); index++ {
				values = appendNestedValues(values, fieldValue.List().Get(index).Message())
			}
		case field.IsMap():
			fieldValue.Map().Range(func(_ protoreflect.MapKey, entry protoreflect.Value) bool {
				values = appendNestedValues(values, entry.Message())
				return true
			})
		default:
			values = appendNestedValues(values, fieldValue.Message())
		}
		return true
	})
	return values
}

// loadStoredValues replaces each Blob reference with the Value the server stored for it.
func loadStoredValues(
	t *testing.T, flowService dexpb.FlowServiceClient, flowID string, values ...*dexpb.Value,
) []*dexpb.Value {
	t.Helper()
	var entries []*dexpb.LoadBlobRequestEntry
	for _, value := range values {
		if storedBlobID(value) != "" {
			entries = append(entries, &dexpb.LoadBlobRequestEntry{FlowId: flowID, BlobValue: value})
		}
	}
	if len(entries) == 0 {
		return values
	}
	response, err := flowService.LoadBlobs(integrationContext(t), &dexpb.LoadBlobsRequest{Entries: entries})
	require.NoError(t, err)
	loaded := make([]*dexpb.Value, 0, len(values))
	for _, value := range values {
		if blobID := storedBlobID(value); blobID != "" {
			value = response.GetValues()[blobID]
			require.NotNil(t, value, "blob %s was not loaded", blobID)
		}
		loaded = append(loaded, value)
	}
	return loaded
}

func storedBlobID(value *dexpb.Value) string {
	if blobID := value.GetInternalBlobIdForObjValue(); blobID != "" {
		return blobID
	}
	return value.GetInternalBlobIdForStringValue()
}

func requireEnvelope(t *testing.T, value *dexpb.Value) {
	t.Helper()
	require.Equal(t, envelopeEncoding, value.GetObjValue().GetEncoding(), "stored Value: %v", value)
	require.NotEmpty(t, value.GetObjValue().GetPayload())
}

// requireStoredErrorTextEncoded reads the raw Flow result and checks that the error
// text is in the dex-encoded: prefix format.
func requireStoredErrorTextEncoded(t *testing.T, flowService dexpb.FlowServiceClient, flowID string) {
	t.Helper()
	result, err := flowService.WaitForFlow(integrationContext(t), &dexpb.WaitForFlowRequest{FlowId: flowID})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_FAILED, result.GetFlowStatus())
	require.True(t, strings.HasPrefix(result.GetErrorMessage(), encodedTextPrefix),
		"the stored error text is not encoded: %q", result.GetErrorMessage())
}

func sendCORSPreflight(t *testing.T, httpClient *http.Client, url string, origin string) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(integrationContext(t), http.MethodOptions, url+"/decode", nil)
	require.NoError(t, err)
	request.Header.Set("Origin", origin)
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request.Header.Set("Access-Control-Request-Headers", "authorization, content-type")
	response, err := httpClient.Do(request)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	return response
}

type interopEnvironment struct {
	serverAddress string
	keyFilePath   string
	codecServers  []codecServerEndpoint
}

func loadInteropEnvironment(t *testing.T) interopEnvironment {
	t.Helper()
	environment := interopEnvironment{
		serverAddress: os.Getenv("DEX_FLOW_SERVICE_ADDRESS"),
		keyFilePath:   os.Getenv("DEX_PAYLOAD_INTEROP_KEY_FILE"),
	}
	require.NotEmpty(t, environment.serverAddress,
		"run examples/run-payload-encryption-interop-tests.sh; DEX_FLOW_SERVICE_ADDRESS is not set")
	require.NotEmpty(t, environment.keyFilePath,
		"run examples/run-payload-encryption-interop-tests.sh; DEX_PAYLOAD_INTEROP_KEY_FILE is not set")
	if codecServers := os.Getenv("DEX_PAYLOAD_INTEROP_CODEC_SERVERS"); codecServers != "" {
		require.NoError(t, json.Unmarshal([]byte(codecServers), &environment.codecServers))
	}
	return environment
}

func (environment interopEnvironment) newClient(t *testing.T, encryption dex.PayloadEncryptionOptions) *dex.Client {
	t.Helper()
	registry, err := interop.NewClientRegistry()
	require.NoError(t, err)
	cache, err := blobcache.New(&blobcache.Config{Dir: t.TempDir(), MaxBytes: 64 << 20})
	require.NoError(t, err)
	client, err := dex.NewClient(registry, cache, dex.ClientOptions{
		FlowServiceAddress: environment.serverAddress,
		PayloadEncryption:  encryption,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, client.Close())
		require.NoError(t, cache.Close())
	})
	return client
}

func (environment interopEnvironment) newRawFlowServiceClient(t *testing.T) dexpb.FlowServiceClient {
	t.Helper()
	connection, err := grpc.NewClient(environment.serverAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	return dexpb.NewFlowServiceClient(connection)
}

func integrationContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	return ctx
}

func newFlowID(prefix string) string {
	sequence := flowCounter.Add(1)
	return prefix + "-" + strconv.FormatInt(time.Now().UnixNano(), 10) + "-" + strconv.FormatInt(sequence, 10)
}
