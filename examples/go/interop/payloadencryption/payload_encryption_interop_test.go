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
	"math"
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
	"google.golang.org/protobuf/proto"
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
			requireStoredTypedValueMapping(t, flowService, encryption.Codec, flowID)

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
	localEncryption, err := interop.NewFilePayloadEncryption(environment.keyFilePath)
	require.NoError(t, err)
	httpClient := &http.Client{Timeout: 30 * time.Second}
	for _, endpoint := range environment.codecServers {
		t.Run(endpoint.Name, func(t *testing.T) {
			serverCodec := codecServerPayloadCodec{endpoint: endpoint, httpClient: httpClient}
			client := environment.newClient(t, dex.PayloadEncryptionOptions{Codec: serverCodec})
			for _, owner := range interop.Owners {
				t.Run(string(owner), func(t *testing.T) {
					input := endpoint.Name + " codec server input for " + string(owner) + " ✓"
					requireCompletedFlowPayloads(t, client, owner, input)
					requireFailedFlowErrorText(t, client, owner, endpoint.Name+" codec server failure ✓")
				})
			}
			t.Run("round-trips every Value kind with the SDK codec", func(t *testing.T) {
				requireCodecRoundTrip(t, serverCodec, localEncryption.Codec)
				requireCodecRoundTrip(t, localEncryption.Codec, serverCodec)
			})
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
	_, err := client.StartFlow(integrationContext(t), interop.NewPayloadInteropFlow(owner), flowID,
		interop.NewPayloadInteropInput(input), dex.StartFlowOptions{})
	require.NoError(t, err)
	result, err := client.WaitForFlow(integrationContext(t), flowID, dex.WaitForFlowOptions{NeedsResults: true})
	require.NoError(t, err)
	require.Equal(t, dex.FlowCompleted, result.Status, result.ErrorMessage)
	var output string
	require.NoError(t, result.DecodeSingleOutput(&output))
	require.Equal(t, interop.ExpectedOutput(owner, input), output)
	var secret string
	var document interop.PayloadInteropDocument
	var count int64
	var ratio float64
	_, err = client.GetAttributes(integrationContext(t), flowID,
		dex.ReadAttribute(interop.SecretText, &secret),
		dex.ReadAttribute(interop.DocumentObject, &document),
		dex.ReadAttribute(interop.CountInteger, &count),
		dex.ReadAttribute(interop.RatioDouble, &ratio),
	)
	require.NoError(t, err)
	require.Equal(t, interop.ExpectedSecretText(owner, input), secret)
	require.Equal(t, interop.ExpectedDocument(), document)
	require.Equal(t, interop.ExpectedCount, count)
	require.Equal(t, interop.ExpectedRatio, ratio)
	return flowID
}

// requireStoredTypedValueMapping decrypts the raw typed Attributes with the local codec and
// checks the Value kind each Worker chose: obj_value "json", int_value, and double_value.
func requireStoredTypedValueMapping(
	t *testing.T, flowService dexpb.FlowServiceClient, codec dex.PayloadCodec, flowID string,
) {
	t.Helper()
	names := []string{
		interop.DocumentObject.AttributeName(),
		interop.CountInteger.AttributeName(),
		interop.RatioDouble.AttributeName(),
	}
	response, err := flowService.GetAttributes(integrationContext(t), &dexpb.GetAttributesRequest{
		FlowId: flowID,
		Keys:   names,
	})
	require.NoError(t, err)
	storedByName := make(map[string]*dexpb.Value, len(names))
	for _, attribute := range response.GetAttributes() {
		storedByName[attribute.GetKey()] = loadStoredValues(t, flowService, flowID, attribute.GetValue())[0]
	}
	decrypt := func(name string) *dexpb.Value {
		stored := storedByName[name]
		require.NotNil(t, stored, "Attribute %s is missing", name)
		requireEnvelope(t, stored)
		envelope := &dexpb.PayloadEnvelope{}
		require.NoError(t, proto.Unmarshal(stored.GetObjValue().GetPayload(), envelope))
		plaintexts, err := codec.Decode(integrationContext(t), dex.PayloadContext{FlowID: flowID},
			[]*dexpb.PayloadEnvelope{envelope})
		require.NoError(t, err)
		value := &dexpb.Value{}
		require.NoError(t, proto.Unmarshal(plaintexts[0], value))
		return value
	}

	document := decrypt(interop.DocumentObject.AttributeName())
	require.Equal(t, "json", document.GetObjValue().GetEncoding(), "document Value: %v", document)
	var storedDocument, expectedDocument map[string]any
	require.NoError(t, json.Unmarshal(document.GetObjValue().GetPayload(), &storedDocument))
	expectedJSON, err := json.Marshal(interop.ExpectedDocument())
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(expectedJSON, &expectedDocument))
	require.Equal(t, expectedDocument, storedDocument)

	count := decrypt(interop.CountInteger.AttributeName())
	_, isInt := count.GetKind().(*dexpb.Value_IntValue)
	require.True(t, isInt, "count Value: %v", count)
	require.Equal(t, interop.ExpectedCount, count.GetIntValue())

	ratio := decrypt(interop.RatioDouble.AttributeName())
	_, isDouble := ratio.GetKind().(*dexpb.Value_DoubleValue)
	require.True(t, isDouble, "ratio Value: %v", ratio)
	require.Equal(t, interop.ExpectedRatio, ratio.GetDoubleValue())
}

// requireCodecRoundTrip encodes every encryptable Value kind with encoder, decodes the
// envelopes with decoder, and checks that each Value comes back bit for bit.
func requireCodecRoundTrip(t *testing.T, encoder dex.PayloadCodec, decoder dex.PayloadCodec) {
	t.Helper()
	documentJSON, err := json.Marshal(interop.ExpectedDocument())
	require.NoError(t, err)
	values := []*dexpb.Value{
		{Kind: &dexpb.Value_StringValue{StringValue: "naïve café ✓"}},
		{Kind: &dexpb.Value_StringValue{StringValue: ""}},
		{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{Encoding: "json", Payload: documentJSON}}},
		{Kind: &dexpb.Value_IntValue{IntValue: interop.ExpectedCount}},
		{Kind: &dexpb.Value_IntValue{IntValue: math.MaxInt64}},
		{Kind: &dexpb.Value_IntValue{IntValue: math.MinInt64}},
		{Kind: &dexpb.Value_IntValue{IntValue: 0}},
		{Kind: &dexpb.Value_DoubleValue{DoubleValue: interop.ExpectedRatio}},
		{Kind: &dexpb.Value_DoubleValue{DoubleValue: math.Copysign(0, -1)}},
		{Kind: &dexpb.Value_DoubleValue{DoubleValue: math.Inf(1)}},
		{Kind: &dexpb.Value_DoubleValue{DoubleValue: math.Inf(-1)}},
		{Kind: &dexpb.Value_DoubleValue{DoubleValue: math.NaN()}},
		{Kind: &dexpb.Value_BoolValue{BoolValue: true}},
		{Kind: &dexpb.Value_BoolValue{BoolValue: false}},
	}
	plaintexts := make([][]byte, 0, len(values))
	for _, value := range values {
		plaintext, err := proto.MarshalOptions{Deterministic: true}.Marshal(value)
		require.NoError(t, err)
		plaintexts = append(plaintexts, plaintext)
	}
	payloadContext := dex.PayloadContext{FlowType: "", FlowID: newFlowID("payload-interop-round-trip")}
	envelopes, err := encoder.Encode(integrationContext(t), payloadContext, plaintexts)
	require.NoError(t, err)
	decoded, err := decoder.Decode(integrationContext(t), payloadContext, envelopes)
	require.NoError(t, err)
	require.Len(t, decoded, len(values))
	for index, value := range values {
		roundTripped := &dexpb.Value{}
		require.NoError(t, proto.Unmarshal(decoded[index], roundTripped))
		if _, isDouble := value.GetKind().(*dexpb.Value_DoubleValue); isDouble && math.IsNaN(value.GetDoubleValue()) {
			// NaN payload bits carry no meaning and differ between languages.
			_, isRoundTrippedDouble := roundTripped.GetKind().(*dexpb.Value_DoubleValue)
			require.True(t, isRoundTrippedDouble && math.IsNaN(roundTripped.GetDoubleValue()),
				"Value %d: sent NaN, got %v", index, roundTripped)
			continue
		}
		roundTrippedBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(roundTripped)
		require.NoError(t, err)
		require.Equal(t, plaintexts[index], roundTrippedBytes, "Value %d: sent %v, got %v", index, value, roundTripped)
	}
}

// requireFailedFlowErrorText starts the Owner's Flow with a failing input and checks
// the decoded Step error text through client. It returns the Flow ID.
func requireFailedFlowErrorText(t *testing.T, client *dex.Client, owner interop.Owner, failureInput string) string {
	t.Helper()
	flowID := newFlowID("payload-interop-failure-" + string(owner))
	_, err := client.StartFlow(integrationContext(t), interop.NewPayloadInteropFlow(owner), flowID,
		interop.NewPayloadInteropInput(interop.FailInputPrefix+failureInput), dex.StartFlowOptions{})
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
