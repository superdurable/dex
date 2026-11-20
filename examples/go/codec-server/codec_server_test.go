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

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	testAllowedOrigin = "https://dex-web.example.com"
	testBearerToken   = "test-bearer-token-0123456789abcdef"
	vectorsPath       = "../../../protos/conformance/payload-encryption/vectors.json"
)

var testPayloadContext = map[string]string{"flow_type": "OrderFlow", "flow_id": "order-1001"}

type testCodecServer struct {
	url         string
	keyFilePath string
	logs        *bytes.Buffer
}

func writeRandomKeyFile(t *testing.T) (path string, encodedKey string) {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	encodedKey = base64.StdEncoding.EncodeToString(key)
	content := `{"current_key_id":"k1","keys":{"k1":"` + encodedKey + `"}}`
	path = filepath.Join(t.TempDir(), "payload-keys.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, encodedKey
}

func defaultTestConfig(keyFilePath string) codecServerConfig {
	return codecServerConfig{
		KeyFilePath:               keyFilePath,
		AllowedOrigins:            []string{testAllowedOrigin},
		AuthMode:                  authModeBearerToken,
		BearerToken:               testBearerToken,
		MaxRequestBodyBytes:       1 << 20,
		MaxItemsPerRequest:        100,
		PayloadSizeLimits:         newPayloadSizeLimits(defaultMaxEnvelopeBytes, defaultMaxPlaintextBytes),
		MaxDecodedBytesPerRequest: defaultMaxDecodedBytesPerRequest,
		ShutdownTimeout:           time.Second,
	}
}

func startTestCodecServer(t *testing.T, config codecServerConfig) testCodecServer {
	t.Helper()
	if err := config.validate(); err != nil {
		t.Fatalf("invalid test config: %v", err)
	}
	logs := &bytes.Buffer{}
	server, err := newPayloadCodecHTTPServer(config, slog.New(slog.NewJSONHandler(logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.routes())
	t.Cleanup(httpServer.Close)
	return testCodecServer{url: httpServer.URL, keyFilePath: config.KeyFilePath, logs: logs}
}

func (server testCodecServer) post(t *testing.T, path string, body any, headers map[string]string) (int, http.Header, []byte) {
	t.Helper()
	encoded, ok := body.([]byte)
	if !ok {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request, err := http.NewRequest(http.MethodPost, server.url+path, bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+testBearerToken)
	for name, value := range headers {
		if value == "" {
			request.Header.Del(name)
		} else {
			request.Header.Set(name, value)
		}
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	content, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, response.Header, content
}

type errorResponse struct {
	Error struct {
		Category string `json:"category"`
		Message  string `json:"message"`
		Index    *int   `json:"index"`
	} `json:"error"`
}

func requireErrorResponse(t *testing.T, status int, content []byte, wantStatus int, wantCategory string, wantIndex *int) {
	t.Helper()
	if status != wantStatus {
		t.Fatalf("status = %d, want %d; body %s", status, wantStatus, content)
	}
	var body errorResponse
	if err := json.Unmarshal(content, &body); err != nil {
		t.Fatalf("error body is not JSON: %s", content)
	}
	if body.Error.Category != wantCategory || body.Error.Message == "" {
		t.Fatalf("error = %+v, want category %q with a message", body.Error, wantCategory)
	}
	switch {
	case wantIndex == nil && body.Error.Index != nil:
		t.Fatalf("index = %d, want none", *body.Error.Index)
	case wantIndex != nil && (body.Error.Index == nil || *body.Error.Index != *wantIndex):
		t.Fatalf("index = %v, want %d; body %s", body.Error.Index, *wantIndex, content)
	}
}

func indexOf(index int) *int { return &index }

func sdkCodecFromKeyFile(t *testing.T, keyFilePath string) *dex.AESGCMPayloadCodec {
	t.Helper()
	keys, err := dex.NewFilePayloadEncryptionKeyProvider(keyFilePath)
	if err != nil {
		t.Fatal(err)
	}
	codec, err := dex.NewAESGCMPayloadCodec(keys, dex.AESGCMPayloadCodecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return codec
}

func sampleValues() []*dexpb.Value {
	return []*dexpb.Value{
		{Kind: &dexpb.Value_StringValue{StringValue: "card ending 4242"}},
		{Kind: &dexpb.Value_StringValue{StringValue: ""}},
		{Kind: &dexpb.Value_IntValue{IntValue: -7}},
		{Kind: &dexpb.Value_DoubleValue{DoubleValue: math.Copysign(0, -1)}},
		{Kind: &dexpb.Value_BoolValue{BoolValue: false}},
		{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{Encoding: "json", Payload: []byte(`{"orderId":"A-1"}`)}}},
	}
}

func requireSameValue(t *testing.T, index int, got *dexpb.Value, want *dexpb.Value) {
	t.Helper()
	if !proto.Equal(got, want) {
		t.Fatalf("value %d = %v, want %v", index, got, want)
	}
	if want.GetKind() != nil {
		if _, isDouble := want.GetKind().(*dexpb.Value_DoubleValue); isDouble &&
			math.Signbit(got.GetDoubleValue()) != math.Signbit(want.GetDoubleValue()) {
			t.Fatalf("value %d lost the sign of zero", index)
		}
	}
}

func TestDecodeReturnsValuesEncryptedByGoSDKCodec(t *testing.T) {
	keyFilePath, _ := writeRandomKeyFile(t)
	server := startTestCodecServer(t, defaultTestConfig(keyFilePath))
	codec := sdkCodecFromKeyFile(t, keyFilePath)

	values := sampleValues()
	plaintexts := make([][]byte, len(values))
	for index, value := range values {
		plaintext, err := proto.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		plaintexts[index] = plaintext
	}
	envelopes, err := codec.Encode(context.Background(), dex.PayloadContext{FlowType: "OrderFlow", FlowID: "order-1001"}, plaintexts)
	if err != nil {
		t.Fatal(err)
	}
	envelopeTexts := make([]string, len(envelopes))
	for index, envelope := range envelopes {
		envelopeBytes, err := proto.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		envelopeTexts[index] = base64.StdEncoding.EncodeToString(envelopeBytes)
	}

	status, _, content := server.post(t, "/decode", map[string]any{"context": testPayloadContext, "envelopes": envelopeTexts}, nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d; body %s", status, content)
	}
	var response struct {
		Values []json.RawMessage `json:"values"`
	}
	if err := json.Unmarshal(content, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Values) != len(values) {
		t.Fatalf("got %d values, want %d", len(response.Values), len(values))
	}
	for index, valueJSON := range response.Values {
		got := &dexpb.Value{}
		if err := protojson.Unmarshal(valueJSON, got); err != nil {
			t.Fatalf("value %d is not Value JSON: %s", index, valueJSON)
		}
		requireSameValue(t, index, got, values[index])
	}
	if !strings.Contains(string(response.Values[2]), `"intValue":"-7"`) {
		t.Fatalf("intValue must be a decimal string: %s", response.Values[2])
	}
}

func TestGoSDKCodecDecodesEnvelopesFromEncode(t *testing.T) {
	keyFilePath, _ := writeRandomKeyFile(t)
	server := startTestCodecServer(t, defaultTestConfig(keyFilePath))
	codec := sdkCodecFromKeyFile(t, keyFilePath)

	values := sampleValues()
	valuesJSON := make([]json.RawMessage, len(values))
	for index, value := range values {
		valueJSON, err := protojson.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		valuesJSON[index] = valueJSON
	}
	status, _, content := server.post(t, "/encode", map[string]any{"context": testPayloadContext, "values": valuesJSON}, nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d; body %s", status, content)
	}
	var response struct {
		Envelopes []string `json:"envelopes"`
	}
	if err := json.Unmarshal(content, &response); err != nil {
		t.Fatal(err)
	}
	envelopes := make([]*dexpb.PayloadEnvelope, len(response.Envelopes))
	for index, envelopeText := range response.Envelopes {
		envelopeBytes, err := base64.StdEncoding.Strict().DecodeString(envelopeText)
		if err != nil {
			t.Fatalf("envelope %d is not standard base64: %v", index, err)
		}
		envelopes[index] = &dexpb.PayloadEnvelope{}
		if err := proto.Unmarshal(envelopeBytes, envelopes[index]); err != nil {
			t.Fatal(err)
		}
		if envelopes[index].GetCompression() != dexpb.PayloadCompression_PAYLOAD_COMPRESSION_NONE {
			t.Fatalf("envelope %d is compressed", index)
		}
	}
	plaintexts, err := codec.Decode(context.Background(), dex.PayloadContext{FlowID: "order-1001"}, envelopes)
	if err != nil {
		t.Fatal(err)
	}
	for index, plaintext := range plaintexts {
		got := &dexpb.Value{}
		if err := proto.Unmarshal(plaintext, got); err != nil {
			t.Fatal(err)
		}
		requireSameValue(t, index, got, values[index])
	}
}

func TestEmptyFlowTypeIsAccepted(t *testing.T) {
	keyFilePath, _ := writeRandomKeyFile(t)
	server := startTestCodecServer(t, defaultTestConfig(keyFilePath))
	for name, flowIDOnlyContext := range map[string]map[string]string{
		"empty flow_type":   {"flow_type": "", "flow_id": "order-1001"},
		"missing flow_type": {"flow_id": "order-1001"},
	} {
		status, _, content := server.post(t, "/encode", map[string]any{
			"context": flowIDOnlyContext, "values": []any{map[string]string{"stringValue": "hello"}},
		}, nil)
		if status != http.StatusOK {
			t.Fatalf("%s: encode status %d body %s", name, status, content)
		}
		var encoded struct {
			Envelopes []string `json:"envelopes"`
		}
		if err := json.Unmarshal(content, &encoded); err != nil {
			t.Fatal(err)
		}
		status, _, content = server.post(t, "/decode", map[string]any{
			"context": flowIDOnlyContext, "envelopes": encoded.Envelopes,
		}, nil)
		if status != http.StatusOK || !strings.Contains(string(content), `"stringValue":"hello"`) {
			t.Fatalf("%s: decode status %d body %s", name, status, content)
		}
	}
}

func TestEncodeAndDecodeEmptyListsReturnEmptyLists(t *testing.T) {
	keyFilePath, _ := writeRandomKeyFile(t)
	server := startTestCodecServer(t, defaultTestConfig(keyFilePath))
	for path, body := range map[string]map[string]any{
		"/encode": {"context": testPayloadContext, "values": []any{}},
		"/decode": {"context": testPayloadContext, "envelopes": []any{}},
	} {
		status, _, content := server.post(t, path, body, nil)
		if status != http.StatusOK || (!strings.Contains(string(content), `"values":[]`) && !strings.Contains(string(content), `"envelopes":[]`)) {
			t.Fatalf("%s: status %d body %s", path, status, content)
		}
	}
}

type goldenVectorFile struct {
	KeyFile  json.RawMessage    `json:"keyFile"`
	Limits   goldenLimits       `json:"limits"`
	Positive []goldenVectorCase `json:"positive"`
	Negative []goldenVectorCase `json:"negative"`
}

type goldenLimits struct {
	MaxEnvelopeBytes    int `json:"maxEnvelopeBytes"`
	MaxEncodedTextBytes int `json:"maxEncodedTextBytes"`
	MaxDataBytes        int `json:"maxDataBytes"`
	MaxPlaintextBytes   int `json:"maxPlaintextBytes"`
}

type goldenVectorCase struct {
	Name  string `json:"name"`
	Input struct {
		Value *struct {
			ObjValue *struct {
				Encoding string `json:"encoding"`
				Payload  string `json:"payload"`
			} `json:"objValue"`
		} `json:"value"`
		Text *string `json:"text"`
	} `json:"input"`
	Limits        *goldenLimits   `json:"limits"`
	ExpectedValue json.RawMessage `json:"expectedValue"`
	ExpectedError string          `json:"expectedError"`
}

// envelopeText returns what a caller sends to /decode: the dex/1 payload or the text after the prefix.
func (vectorCase goldenVectorCase) envelopeText() (string, bool) {
	if vectorCase.Input.Text != nil {
		return strings.CutPrefix(*vectorCase.Input.Text, "dex-encoded:")
	}
	if vectorCase.Input.Value == nil || vectorCase.Input.Value.ObjValue == nil ||
		vectorCase.Input.Value.ObjValue.Encoding != "dex/1" {
		return "", false
	}
	return vectorCase.Input.Value.ObjValue.Payload, true
}

func TestDecodeGoldenVectors(t *testing.T) {
	content, err := os.ReadFile(vectorsPath)
	if err != nil {
		t.Fatalf("read golden vectors from the Dex repository: %v", err)
	}
	var vectors goldenVectorFile
	if err := json.Unmarshal(content, &vectors); err != nil {
		t.Fatal(err)
	}
	keyFilePath := filepath.Join(t.TempDir(), "vector-keys.json")
	if err := os.WriteFile(keyFilePath, vectors.KeyFile, 0o600); err != nil {
		t.Fatal(err)
	}
	serverForLimits := func(t *testing.T, limits *goldenLimits) testCodecServer {
		config := defaultTestConfig(keyFilePath)
		if limits == nil {
			limits = &vectors.Limits
		}
		config.PayloadSizeLimits = payloadSizeLimits(*limits)
		return startTestCodecServer(t, config)
	}
	defaultServer := serverForLimits(t, nil)

	decodedCases := 0
	for _, vectorCase := range vectors.Positive {
		envelopeText, isEnvelope := vectorCase.envelopeText()
		if !isEnvelope {
			continue
		}
		t.Run("positive/"+vectorCase.Name, func(t *testing.T) {
			server := defaultServer
			if vectorCase.Limits != nil {
				server = serverForLimits(t, vectorCase.Limits)
			}
			status, _, content := server.post(t, "/decode",
				map[string]any{"context": testPayloadContext, "envelopes": []string{envelopeText}}, nil)
			if status != http.StatusOK {
				t.Fatalf("status = %d; body %s", status, content)
			}
			var response struct {
				Values []json.RawMessage `json:"values"`
			}
			if err := json.Unmarshal(content, &response); err != nil || len(response.Values) != 1 {
				t.Fatalf("body %s", content)
			}
			got, want := &dexpb.Value{}, &dexpb.Value{}
			if err := protojson.Unmarshal(response.Values[0], got); err != nil {
				t.Fatal(err)
			}
			if err := protojson.Unmarshal(vectorCase.ExpectedValue, want); err != nil {
				t.Fatal(err)
			}
			requireSameValue(t, 0, got, want)
		})
		decodedCases++
	}
	for _, vectorCase := range vectors.Negative {
		envelopeText, isEnvelope := vectorCase.envelopeText()
		// The caller checks the marker and that prefix text holds a string, so the server cannot.
		if !isEnvelope || vectorCase.ExpectedError == "unsupported_marker" || vectorCase.Name == "text-plaintext-not-string" {
			continue
		}
		t.Run("negative/"+vectorCase.Name, func(t *testing.T) {
			server := defaultServer
			if vectorCase.Limits != nil {
				server = serverForLimits(t, vectorCase.Limits)
			}
			status, _, content := server.post(t, "/decode",
				map[string]any{"context": testPayloadContext, "envelopes": []string{envelopeText}}, nil)
			requireErrorResponse(t, status, content, http.StatusUnprocessableEntity, vectorCase.ExpectedError, indexOf(0))
		})
		decodedCases++
	}
	if decodedCases < 40 {
		t.Fatalf("only %d golden vector cases ran; the vector file layout may have changed", decodedCases)
	}
}

func TestDecodeErrorNamesFirstFailingIndex(t *testing.T) {
	content, err := os.ReadFile(vectorsPath)
	if err != nil {
		t.Fatal(err)
	}
	var vectors goldenVectorFile
	if err := json.Unmarshal(content, &vectors); err != nil {
		t.Fatal(err)
	}
	keyFilePath := filepath.Join(t.TempDir(), "vector-keys.json")
	if err := os.WriteFile(keyFilePath, vectors.KeyFile, 0o600); err != nil {
		t.Fatal(err)
	}
	server := startTestCodecServer(t, defaultTestConfig(keyFilePath))
	envelopeByName := map[string]string{}
	for _, vectorCase := range append(vectors.Positive, vectors.Negative...) {
		if envelopeText, ok := vectorCase.envelopeText(); ok {
			envelopeByName[vectorCase.Name] = envelopeText
		}
	}
	cases := []struct {
		names        []string
		wantCategory string
		wantIndex    int
	}{
		{[]string{"string-ascii", "int-positive", "wrong-tag"}, "authentication_failed", 2},
		{[]string{"wrong-tag", "non-canonical-base64"}, "authentication_failed", 0},
		{[]string{"plaintext-null-value", "wrong-tag"}, "invalid_plaintext", 0},
		{[]string{"string-ascii", "non-canonical-base64", "unknown-key-id"}, "malformed", 1},
	}
	envelopeByName["non-canonical-base64"] = "AAE"
	for _, testCase := range cases {
		envelopes := make([]string, len(testCase.names))
		for index, name := range testCase.names {
			envelopes[index] = envelopeByName[name]
		}
		status, _, body := server.post(t, "/decode", map[string]any{"context": testPayloadContext, "envelopes": envelopes}, nil)
		requireErrorResponse(t, status, body, http.StatusUnprocessableEntity, testCase.wantCategory, indexOf(testCase.wantIndex))
	}
}

func TestCORSRejectsUnlistedOrigin(t *testing.T) {
	keyFilePath, _ := writeRandomKeyFile(t)
	server := startTestCodecServer(t, defaultTestConfig(keyFilePath))

	preflight := func(origin string) *http.Response {
		request, err := http.NewRequest(http.MethodOptions, server.url+"/decode", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Origin", origin)
		request.Header.Set("Access-Control-Request-Method", "POST")
		request.Header.Set("Access-Control-Request-Headers", "authorization, content-type")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { response.Body.Close() })
		return response
	}

	allowed := preflight(testAllowedOrigin)
	if allowed.StatusCode != http.StatusNoContent ||
		allowed.Header.Get("Access-Control-Allow-Origin") != testAllowedOrigin ||
		!strings.Contains(allowed.Header.Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Fatalf("allowed preflight: status %d headers %v", allowed.StatusCode, allowed.Header)
	}
	rejected := preflight("https://attacker.example.com")
	if rejected.StatusCode != http.StatusForbidden || rejected.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("unlisted preflight: status %d headers %v", rejected.StatusCode, rejected.Header)
	}

	body := map[string]any{"context": testPayloadContext, "envelopes": []string{}}
	status, headers, content := server.post(t, "/decode", body, map[string]string{"Origin": "https://attacker.example.com"})
	requireErrorResponse(t, status, content, http.StatusForbidden, "forbidden", nil)
	if headers.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("rejected origin received Access-Control-Allow-Origin")
	}
	status, headers, content = server.post(t, "/decode", body, map[string]string{"Origin": testAllowedOrigin})
	if status != http.StatusOK || headers.Get("Access-Control-Allow-Origin") != testAllowedOrigin {
		t.Fatalf("allowed origin: status %d body %s", status, content)
	}
}

func TestBearerTokenAuthenticationFailuresReturn401(t *testing.T) {
	keyFilePath, _ := writeRandomKeyFile(t)
	server := startTestCodecServer(t, defaultTestConfig(keyFilePath))
	body := map[string]any{"context": testPayloadContext, "values": []any{}}
	for name, authorization := range map[string]string{
		"missing":      "",
		"wrong token":  "Bearer not-the-configured-token-0123456789",
		"wrong scheme": "Basic " + testBearerToken,
	} {
		status, _, content := server.post(t, "/encode", body, map[string]string{"Authorization": authorization})
		if status != http.StatusUnauthorized {
			t.Fatalf("%s: status %d", name, status)
		}
		requireErrorResponse(t, status, content, http.StatusUnauthorized, "unauthenticated", nil)
	}
}

func TestTrustedProxyHeaderAuthentication(t *testing.T) {
	keyFilePath, _ := writeRandomKeyFile(t)
	config := defaultTestConfig(keyFilePath)
	config.AuthMode = authModeTrustedProxyHeader
	config.BearerToken = ""
	config.TrustedIdentityHeader = "X-Forwarded-Email"
	server := startTestCodecServer(t, config)
	body := map[string]any{"context": testPayloadContext, "values": []any{}}

	status, _, content := server.post(t, "/encode", body, map[string]string{"Authorization": ""})
	requireErrorResponse(t, status, content, http.StatusUnauthorized, "unauthenticated", nil)
	status, _, content = server.post(t, "/encode", body, map[string]string{"Authorization": "", "X-Forwarded-Email": "ada@example.com"})
	if status != http.StatusOK {
		t.Fatalf("status %d body %s", status, content)
	}
	if !strings.Contains(server.logs.String(), `"caller":"ada@example.com"`) {
		t.Fatalf("access log does not name the caller: %s", server.logs.String())
	}
}

type forbiddingPayloadCodecSelector struct{}

func (forbiddingPayloadCodecSelector) selectPayloadCodec(context.Context, callerIdentity) (dex.PayloadCodec, error) {
	return nil, errCallerForbidden
}

const providerSentinelSecret = "SENTINEL-provider-secret-arn:kms:tenant-7"

type leakingPayloadEncryptionKeyProvider struct{}

func (leakingPayloadEncryptionKeyProvider) CurrentKeyID(context.Context, dex.PayloadContext) (string, error) {
	return "", errors.New("current key lookup failed: " + providerSentinelSecret)
}

func (leakingPayloadEncryptionKeyProvider) Key(context.Context, dex.PayloadContext, string) ([]byte, error) {
	return nil, errors.New("key lookup failed: " + providerSentinelSecret)
}

type leakingPayloadCodec struct{}

func (leakingPayloadCodec) Encode(context.Context, dex.PayloadContext, [][]byte) ([]*dexpb.PayloadEnvelope, error) {
	return nil, errors.New("codec failed: " + providerSentinelSecret)
}

func (leakingPayloadCodec) Decode(context.Context, dex.PayloadContext, []*dexpb.PayloadEnvelope) ([][]byte, error) {
	return nil, errors.New("codec failed: " + providerSentinelSecret)
}

type staticPayloadCodecSelector struct {
	codec dex.PayloadCodec
	err   error
}

func (selector staticPayloadCodecSelector) selectPayloadCodec(context.Context, callerIdentity) (dex.PayloadCodec, error) {
	return selector.codec, selector.err
}

func TestProviderAndCodecErrorTextNeverReachesResponsesOrLogs(t *testing.T) {
	keyFilePath, _ := writeRandomKeyFile(t)
	sdkCodec := sdkCodecFromKeyFile(t, keyFilePath)
	plaintext, err := proto.Marshal(&dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	envelopes, err := sdkCodec.Encode(context.Background(), dex.PayloadContext{FlowID: "order-1001"}, [][]byte{plaintext})
	if err != nil {
		t.Fatal(err)
	}
	envelopeBytes, err := proto.Marshal(envelopes[0])
	if err != nil {
		t.Fatal(err)
	}
	envelopeText := base64.StdEncoding.EncodeToString(envelopeBytes)
	leakingAESCodec, err := dex.NewAESGCMPayloadCodec(leakingPayloadEncryptionKeyProvider{}, dex.AESGCMPayloadCodecOptions{})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name         string
		selector     staticPayloadCodecSelector
		path         string
		body         map[string]any
		wantStatus   int
		wantCategory string
		wantIndex    *int
	}{
		{"provider refuses key on decode", staticPayloadCodecSelector{codec: leakingAESCodec}, "/decode",
			map[string]any{"context": testPayloadContext, "envelopes": []string{envelopeText}},
			http.StatusUnprocessableEntity, "unknown_key", indexOf(0)},
		{"provider fails on encode", staticPayloadCodecSelector{codec: leakingAESCodec}, "/encode",
			map[string]any{"context": testPayloadContext, "values": []any{map[string]string{"stringValue": "x"}}},
			http.StatusInternalServerError, "internal", nil},
		{"custom codec fails on decode", staticPayloadCodecSelector{codec: leakingPayloadCodec{}}, "/decode",
			map[string]any{"context": testPayloadContext, "envelopes": []string{envelopeText}},
			http.StatusInternalServerError, "internal", nil},
		{"custom codec fails on encode", staticPayloadCodecSelector{codec: leakingPayloadCodec{}}, "/encode",
			map[string]any{"context": testPayloadContext, "values": []any{map[string]string{"stringValue": "x"}}},
			http.StatusInternalServerError, "internal", nil},
		{"selector fails", staticPayloadCodecSelector{err: errors.New("tenant lookup failed: " + providerSentinelSecret)}, "/decode",
			map[string]any{"context": testPayloadContext, "envelopes": []string{envelopeText}},
			http.StatusInternalServerError, "internal", nil},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			logs := &bytes.Buffer{}
			server, err := newPayloadCodecHTTPServer(defaultTestConfig(keyFilePath), slog.New(slog.NewJSONHandler(logs, nil)))
			if err != nil {
				t.Fatal(err)
			}
			server.codecSelector = testCase.selector
			httpServer := httptest.NewServer(server.routes())
			t.Cleanup(httpServer.Close)
			status, _, content := testCodecServer{url: httpServer.URL}.post(t, testCase.path, testCase.body, nil)
			requireErrorResponse(t, status, content, testCase.wantStatus, testCase.wantCategory, testCase.wantIndex)
			if strings.Contains(string(content), "SENTINEL") || strings.Contains(logs.String(), "SENTINEL") {
				t.Fatalf("provider or codec error text leaked; body %s logs %s", content, logs.String())
			}
		})
	}
}

func TestDecodedBytesBudgetBoundsCompressibleEnvelopes(t *testing.T) {
	keyFilePath, _ := writeRandomKeyFile(t)
	config := defaultTestConfig(keyFilePath)
	config.MaxDecodedBytesPerRequest = 4 << 20
	server := startTestCodecServer(t, config)

	keys, err := dex.NewFilePayloadEncryptionKeyProvider(keyFilePath)
	if err != nil {
		t.Fatal(err)
	}
	gzipCodec, err := dex.NewAESGCMPayloadCodec(keys, dex.AESGCMPayloadCodecOptions{
		Compression: dexpb.PayloadCompression_PAYLOAD_COMPRESSION_GZIP,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Each 1 MiB string compresses to about 1 KiB, so five fit easily in the 1 MiB body limit.
	plaintext, err := proto.Marshal(&dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: strings.Repeat("a", 1<<20)}})
	if err != nil {
		t.Fatal(err)
	}
	envelopes, err := gzipCodec.Encode(context.Background(), dex.PayloadContext{FlowID: "order-1001"},
		[][]byte{plaintext, plaintext, plaintext, plaintext, plaintext})
	if err != nil {
		t.Fatal(err)
	}
	envelopeTexts := make([]string, len(envelopes))
	for index, envelope := range envelopes {
		envelopeBytes, err := proto.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		envelopeTexts[index] = base64.StdEncoding.EncodeToString(envelopeBytes)
	}

	status, _, content := server.post(t, "/decode",
		map[string]any{"context": testPayloadContext, "envelopes": envelopeTexts[:3]}, nil)
	if status != http.StatusOK {
		t.Fatalf("three envelopes under the budget: status %d body %.200s", status, content)
	}
	status, _, content = server.post(t, "/decode",
		map[string]any{"context": testPayloadContext, "envelopes": envelopeTexts}, nil)
	requireErrorResponse(t, status, content, http.StatusRequestEntityTooLarge, "limit_exceeded", nil)

	// A failing entry before the budget runs out still reports its own index.
	withBadEntry := []string{envelopeTexts[0], "AAE", envelopeTexts[1], envelopeTexts[2], envelopeTexts[3]}
	status, _, content = server.post(t, "/decode",
		map[string]any{"context": testPayloadContext, "envelopes": withBadEntry}, nil)
	requireErrorResponse(t, status, content, http.StatusUnprocessableEntity, "malformed", indexOf(1))
}

func gzipEnvelopeTexts(t *testing.T, keyFilePath string, values ...*dexpb.Value) []string {
	t.Helper()
	keys, err := dex.NewFilePayloadEncryptionKeyProvider(keyFilePath)
	if err != nil {
		t.Fatal(err)
	}
	gzipCodec, err := dex.NewAESGCMPayloadCodec(keys, dex.AESGCMPayloadCodecOptions{
		Compression: dexpb.PayloadCompression_PAYLOAD_COMPRESSION_GZIP,
	})
	if err != nil {
		t.Fatal(err)
	}
	plaintexts := make([][]byte, len(values))
	for index, value := range values {
		if plaintexts[index], err = proto.Marshal(value); err != nil {
			t.Fatal(err)
		}
	}
	envelopes, err := gzipCodec.Encode(context.Background(), dex.PayloadContext{FlowID: "order-1001"}, plaintexts)
	if err != nil {
		t.Fatal(err)
	}
	envelopeTexts := make([]string, len(envelopes))
	for index, envelope := range envelopes {
		envelopeBytes, err := proto.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		envelopeTexts[index] = base64.StdEncoding.EncodeToString(envelopeBytes)
	}
	return envelopeTexts
}

func TestDecodedBudgetCountsJSONEscapesBeforeBuildingJSON(t *testing.T) {
	keyFilePath, _ := writeRandomKeyFile(t)
	config := defaultTestConfig(keyFilePath)
	config.MaxDecodedBytesPerRequest = 4 << 20
	server := startTestCodecServer(t, config)

	// Each control character becomes six JSON bytes (\u0001), so 1 MiB of them needs 6 MiB.
	controlCharacters := func(count int) *dexpb.Value {
		return &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: strings.Repeat("\x01", count)}}
	}
	oneMiB := gzipEnvelopeTexts(t, keyFilePath, controlCharacters(1<<20))
	if len(oneMiB[0]) > 16<<10 {
		t.Fatalf("envelope has %d bytes; expected high compression", len(oneMiB[0]))
	}
	status, _, content := server.post(t, "/decode",
		map[string]any{"context": testPayloadContext, "envelopes": oneMiB}, nil)
	requireErrorResponse(t, status, content, http.StatusRequestEntityTooLarge, "limit_exceeded", nil)

	halfMiB := gzipEnvelopeTexts(t, keyFilePath, controlCharacters(512<<10), controlCharacters(512<<10))
	status, _, content = server.post(t, "/decode",
		map[string]any{"context": testPayloadContext, "envelopes": halfMiB[:1]}, nil)
	if status != http.StatusOK {
		t.Fatalf("3 MiB of JSON under a 4 MiB budget: status %d body %.200s", status, content)
	}
	status, _, content = server.post(t, "/decode",
		map[string]any{"context": testPayloadContext, "envelopes": halfMiB}, nil)
	requireErrorResponse(t, status, content, http.StatusRequestEntityTooLarge, "limit_exceeded", nil)

	// Rejecting 4 MiB of control characters must not allocate their 24 MiB of JSON first.
	fourMiB := gzipEnvelopeTexts(t, keyFilePath, controlCharacters(4<<20))[0]
	codecServer, err := newPayloadCodecHTTPServer(config, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	codec := sdkCodecFromKeyFile(t, keyFilePath)
	decodeAndMeasure := func(budgetBytes int) (*codecRequestError, uint64) {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		_, requestErr := codecServer.decodeEnvelope(
			context.Background(), codec, dex.PayloadContext{FlowID: "order-1001"}, fourMiB, budgetBytes)
		runtime.ReadMemStats(&after)
		return requestErr, after.TotalAlloc - before.TotalAlloc
	}
	acceptedErr, acceptedBytes := decodeAndMeasure(32 << 20)
	rejectedErr, rejectedBytes := decodeAndMeasure(1 << 20)
	if acceptedErr != nil || rejectedErr == nil || rejectedErr.status != http.StatusRequestEntityTooLarge {
		t.Fatalf("accepted error %+v, rejected error %+v", acceptedErr, rejectedErr)
	}
	if rejectedBytes+(20<<20) > acceptedBytes {
		t.Fatalf("rejecting allocated %d bytes and accepting %d; JSON was built before the budget check",
			rejectedBytes, acceptedBytes)
	}
}

func TestValueJSONLengthMatchesWrittenJSON(t *testing.T) {
	values := append(sampleValues(),
		&dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: "quote \" slash \\ tab \t nl \n cr \r bs \b ff \f nul \x00 esc \x1b del \x7f"}},
		&dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: "unicode é 漢字   😀 <html>&"}},
		&dexpb.Value{Kind: &dexpb.Value_IntValue{IntValue: math.MinInt64}},
		&dexpb.Value{Kind: &dexpb.Value_IntValue{IntValue: math.MaxInt64}},
		&dexpb.Value{Kind: &dexpb.Value_DoubleValue{DoubleValue: math.NaN()}},
		&dexpb.Value{Kind: &dexpb.Value_DoubleValue{DoubleValue: math.Inf(1)}},
		&dexpb.Value{Kind: &dexpb.Value_DoubleValue{DoubleValue: math.Inf(-1)}},
		&dexpb.Value{Kind: &dexpb.Value_DoubleValue{DoubleValue: 1e300}},
		&dexpb.Value{Kind: &dexpb.Value_DoubleValue{DoubleValue: 5e-324}},
		&dexpb.Value{Kind: &dexpb.Value_DoubleValue{DoubleValue: 0.1}},
		&dexpb.Value{Kind: &dexpb.Value_BoolValue{BoolValue: true}},
		&dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{}}},
		&dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{Encoding: "raw"}}},
		&dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{Payload: []byte{0, 1, 2, 3, 255}}}},
		&dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{Encoding: "x\"\x01", Payload: []byte("a")}}},
	)
	for index, value := range values {
		written := appendValueJSON(nil, value)
		if len(written) != valueJSONLength(value) {
			t.Fatalf("value %d: wrote %d bytes, length says %d: %s", index, len(written), valueJSONLength(value), written)
		}
		parsed := &dexpb.Value{}
		if err := protojson.Unmarshal(written, parsed); err != nil {
			t.Fatalf("value %d: %s is not proto3 JSON: %v", index, written, err)
		}
		if _, isDouble := value.GetKind().(*dexpb.Value_DoubleValue); isDouble && math.IsNaN(value.GetDoubleValue()) {
			if !math.IsNaN(parsed.GetDoubleValue()) {
				t.Fatalf("value %d: NaN did not round-trip", index)
			}
			continue
		}
		requireSameValue(t, index, parsed, value)
	}
}

func TestCodecSelectorForbiddenReturns403(t *testing.T) {
	keyFilePath, _ := writeRandomKeyFile(t)
	server, err := newPayloadCodecHTTPServer(defaultTestConfig(keyFilePath), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	server.codecSelector = forbiddingPayloadCodecSelector{}
	httpServer := httptest.NewServer(server.routes())
	t.Cleanup(httpServer.Close)
	status, _, content := testCodecServer{url: httpServer.URL}.post(t, "/decode",
		map[string]any{"context": testPayloadContext, "envelopes": []string{}}, nil)
	requireErrorResponse(t, status, content, http.StatusForbidden, "forbidden", nil)
}

func TestRequestLimitsReturn413(t *testing.T) {
	keyFilePath, _ := writeRandomKeyFile(t)
	config := defaultTestConfig(keyFilePath)
	config.MaxRequestBodyBytes = 1024
	config.MaxItemsPerRequest = 3
	server := startTestCodecServer(t, config)

	oversized := map[string]any{"context": testPayloadContext, "values": []any{map[string]string{"stringValue": strings.Repeat("x", 2048)}}}
	status, _, content := server.post(t, "/encode", oversized, nil)
	requireErrorResponse(t, status, content, http.StatusRequestEntityTooLarge, "limit_exceeded", nil)

	tooMany := map[string]any{"context": testPayloadContext, "envelopes": []string{"", "", "", ""}}
	status, _, content = server.post(t, "/decode", tooMany, nil)
	requireErrorResponse(t, status, content, http.StatusRequestEntityTooLarge, "limit_exceeded", nil)
}

func TestProtocolErrorBodies(t *testing.T) {
	keyFilePath, _ := writeRandomKeyFile(t)
	server := startTestCodecServer(t, defaultTestConfig(keyFilePath))
	cases := []struct {
		name         string
		path         string
		body         any
		headers      map[string]string
		wantStatus   int
		wantCategory string
		wantIndex    *int
	}{
		{"invalid JSON", "/decode", []byte(`{"context":`), nil, http.StatusBadRequest, "malformed", nil},
		{"missing context", "/decode", map[string]any{"envelopes": []string{}}, nil, http.StatusBadRequest, "malformed", nil},
		{"empty flow id", "/encode", map[string]any{"context": map[string]string{"flow_type": "OrderFlow", "flow_id": ""}, "values": []any{}}, nil, http.StatusBadRequest, "malformed", nil},
		{"missing list", "/encode", map[string]any{"context": testPayloadContext}, nil, http.StatusBadRequest, "malformed", nil},
		{"wrong content type", "/encode", map[string]any{"context": testPayloadContext, "values": []any{}}, map[string]string{"Content-Type": "text/plain"}, http.StatusBadRequest, "malformed", nil},
		{"null value", "/encode", map[string]any{"context": testPayloadContext, "values": []any{map[string]any{"nullValue": nil}}}, nil, http.StatusUnprocessableEntity, "invalid_plaintext", indexOf(0)},
		{"reserved encoding", "/encode", map[string]any{"context": testPayloadContext, "values": []any{
			map[string]string{"stringValue": "ok"},
			map[string]any{"objValue": map[string]string{"encoding": "dex/1", "payload": "AA=="}},
		}}, nil, http.StatusUnprocessableEntity, "invalid_plaintext", indexOf(1)},
		{"blob reference", "/encode", map[string]any{"context": testPayloadContext, "values": []any{map[string]string{"internalBlobIdForStringValue": "blob-1"}}}, nil, http.StatusUnprocessableEntity, "invalid_plaintext", indexOf(0)},
		{"not a Value", "/encode", map[string]any{"context": testPayloadContext, "values": []any{map[string]string{"color": "blue"}}}, nil, http.StatusUnprocessableEntity, "malformed", indexOf(0)},
		{"non-canonical base64", "/decode", map[string]any{"context": testPayloadContext, "envelopes": []string{"AAE"}}, nil, http.StatusUnprocessableEntity, "malformed", indexOf(0)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			status, headers, content := server.post(t, testCase.path, testCase.body, testCase.headers)
			requireErrorResponse(t, status, content, testCase.wantStatus, testCase.wantCategory, testCase.wantIndex)
			if headers.Get("Content-Type") != "application/json" {
				t.Fatalf("Content-Type = %q", headers.Get("Content-Type"))
			}
		})
	}
	request, err := http.NewRequest(http.MethodGet, server.url+"/decode", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+testBearerToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	content, _ := io.ReadAll(response.Body)
	requireErrorResponse(t, response.StatusCode, content, http.StatusBadRequest, "malformed", nil)
}

func TestHealthCheckNeedsNoCredentials(t *testing.T) {
	keyFilePath, _ := writeRandomKeyFile(t)
	server := startTestCodecServer(t, defaultTestConfig(keyFilePath))
	response, err := http.Get(server.url + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status %d", response.StatusCode)
	}
}

func TestLogsContainNoPayloadsKeysOrTokens(t *testing.T) {
	keyFilePath, encodedKey := writeRandomKeyFile(t)
	server := startTestCodecServer(t, defaultTestConfig(keyFilePath))
	secret := "top-secret-card-4242"
	status, _, content := server.post(t, "/encode",
		map[string]any{"context": testPayloadContext, "values": []any{map[string]string{"stringValue": secret}}}, nil)
	if status != http.StatusOK {
		t.Fatalf("status %d body %s", status, content)
	}
	var response struct {
		Envelopes []string `json:"envelopes"`
	}
	if err := json.Unmarshal(content, &response); err != nil {
		t.Fatal(err)
	}
	server.post(t, "/decode", map[string]any{"context": testPayloadContext, "envelopes": response.Envelopes}, nil)
	server.post(t, "/decode", map[string]any{"context": testPayloadContext, "envelopes": []string{"AAE"}}, nil)

	logs := server.logs.String()
	if !strings.Contains(logs, `"msg":"codec request"`) {
		t.Fatalf("no access log lines: %s", logs)
	}
	for _, forbidden := range []string{secret, encodedKey, testBearerToken, response.Envelopes[0]} {
		if strings.Contains(logs, forbidden) {
			t.Fatalf("logs contain %q", forbidden)
		}
	}
}

func TestLoadConfigRejectsUnsafeSettings(t *testing.T) {
	keyFilePath, _ := writeRandomKeyFile(t)
	base := map[string]string{
		"DEX_CODEC_SERVER_KEY_FILE":        keyFilePath,
		"DEX_CODEC_SERVER_ALLOWED_ORIGINS": testAllowedOrigin,
		"DEX_CODEC_SERVER_AUTH_MODE":       authModeBearerToken,
		"DEX_CODEC_SERVER_BEARER_TOKEN":    testBearerToken,
	}
	load := func(overrides map[string]string, arguments ...string) (codecServerConfig, error) {
		environment := map[string]string{}
		for name, value := range base {
			environment[name] = value
		}
		for name, value := range overrides {
			environment[name] = value
		}
		return loadCodecServerConfig(arguments, func(name string) (string, bool) {
			value, found := environment[name]
			return value, found
		})
	}

	config, err := load(nil, "-allowed-origins", "https://Dex-Web.example.com, http://localhost:8802")
	if err != nil {
		t.Fatal(err)
	}
	if len(config.AllowedOrigins) != 2 || config.AllowedOrigins[0] != testAllowedOrigin ||
		config.PayloadSizeLimits.MaxEncodedTextBytes != 22_457_004 {
		t.Fatalf("config = %+v", config)
	}
	for name, overrides := range map[string]map[string]string{
		"wildcard origin":   {"DEX_CODEC_SERVER_ALLOWED_ORIGINS": "*"},
		"origin with path":  {"DEX_CODEC_SERVER_ALLOWED_ORIGINS": "https://dex-web.example.com/app"},
		"no origins":        {"DEX_CODEC_SERVER_ALLOWED_ORIGINS": ""},
		"no auth":           {"DEX_CODEC_SERVER_AUTH_MODE": ""},
		"short token":       {"DEX_CODEC_SERVER_BEARER_TOKEN": "short"},
		"two key sources":   {"DEX_CODEC_SERVER_KEYS_JSON": `{"current_key_id":"k1","keys":{}}`},
		"proxy no header":   {"DEX_CODEC_SERVER_AUTH_MODE": authModeTrustedProxyHeader},
		"bad item limit":    {"DEX_CODEC_SERVER_MAX_ITEMS_PER_REQUEST": "0"},
		"no decoded limit":  {"DEX_CODEC_SERVER_MAX_DECODED_BYTES_PER_REQUEST": "0"},
		"zero shutdown":     {"DEX_CODEC_SERVER_SHUTDOWN_TIMEOUT": "0s"},
		"negative shutdown": {"DEX_CODEC_SERVER_SHUTDOWN_TIMEOUT": "-1s"},
		"no key source":     {"DEX_CODEC_SERVER_KEY_FILE": ""},
	} {
		if _, err := load(overrides); err == nil {
			t.Fatalf("%s: config was accepted", name)
		}
	}
}

func TestKeysJSONFromEnvironment(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	provider, err := newKeyFileJSONPayloadEncryptionKeyProvider([]byte(`{"current_key_id":"k2","keys":{"k1":"` + key + `","k2":"` + key + `"}}`))
	if err != nil {
		t.Fatal(err)
	}
	currentKeyID, _ := provider.CurrentKeyID(context.Background(), dex.PayloadContext{})
	if currentKeyID != "k2" {
		t.Fatalf("current key id = %q", currentKeyID)
	}
	if _, err := provider.Key(context.Background(), dex.PayloadContext{}, "k9"); err == nil {
		t.Fatal("unknown key id was accepted")
	}
	shortKey := base64.StdEncoding.EncodeToString([]byte("sixteen-byte-key"))
	for name, keyFileJSON := range map[string]string{
		"short key":       `{"current_key_id":"k1","keys":{"k1":"` + shortKey + `"}}`,
		"missing current": `{"current_key_id":"k3","keys":{"k1":"` + key + `"}}`,
		"key id space":    `{"current_key_id":"k 1","keys":{"k 1":"` + key + `"}}`,
	} {
		_, err := newKeyFileJSONPayloadEncryptionKeyProvider([]byte(keyFileJSON))
		if err == nil {
			t.Fatalf("%s: accepted", name)
		}
		if strings.Contains(err.Error(), shortKey) || strings.Contains(err.Error(), key) {
			t.Fatalf("%s: error contains key bytes: %v", name, err)
		}
	}
}
