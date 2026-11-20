// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package payloadcodec

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
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

func errorMessage(result codecHTTPResult) string {
	failure, _ := result.body["error"].(map[string]any)
	message, _ := failure["message"].(string)
	return message
}

func encodedEnvelope(t *testing.T, envelope *dexpb.PayloadEnvelope) string {
	t.Helper()
	envelopeBytes, err := proto.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(envelopeBytes)
}

// TestLocalCodecServerErrorMessagesAreFixed checks that responses carry only the fixed text of each category.
func TestLocalCodecServerErrorMessagesAreFixed(t *testing.T) {
	server := startTestCodecServer(t)
	validEnvelope := &dexpb.PayloadEnvelope{
		Cipher: dexpb.PayloadCipher_PAYLOAD_CIPHER_AES256_GCM, KeyId: "dev-key", Data: make([]byte, 40),
	}
	tooManyMetadata := proto.Clone(validEnvelope).(*dexpb.PayloadEnvelope)
	tooManyMetadata.Metadata = map[string][]byte{}
	for index := range maxMetadataEntries + 1 {
		tooManyMetadata.Metadata[fmt.Sprintf("entry-%d", index)] = []byte("value")
	}
	invalidKeyID := proto.Clone(validEnvelope).(*dexpb.PayloadEnvelope)
	invalidKeyID.KeyId = "has space"
	unknownKey := proto.Clone(validEnvelope).(*dexpb.PayloadEnvelope)
	unknownKey.KeyId = "missing-key"
	oversized := strings.Repeat("A", Limits{}.withDefaults().MaxEncodedTextBytes+4)
	for _, scenario := range []struct {
		name     string
		path     string
		entry    string
		category string
		message  string
	}{
		{"oversized base64", "/decode", fmt.Sprintf("%q", oversized), CategoryLimitExceeded, "envelope exceeds a size limit"},
		{"non-canonical base64", "/decode", `"AB=="`, CategoryMalformed, "envelope is malformed"},
		{"invalid key ID", "/decode", fmt.Sprintf("%q", encodedEnvelope(t, invalidKeyID)), CategoryMalformed, "envelope is malformed"},
		{"metadata over the limit", "/decode", fmt.Sprintf("%q", encodedEnvelope(t, tooManyMetadata)), CategoryLimitExceeded, "envelope exceeds a size limit"},
		{"unknown key", "/decode", fmt.Sprintf("%q", encodedEnvelope(t, unknownKey)), "unknown_key", `key id "missing-key" is unknown`},
		{"reserved encoding", "/encode", `{"objValue":{"encoding":"dex/2","payload":"AA=="}}`, CategoryInvalidPlaintext, "plaintext is not an encryptable Value"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			entries := "envelopes"
			if scenario.path == "/encode" {
				entries = "values"
			}
			body := fmt.Sprintf(`{"context":{"flow_id":"f"},%q:[%s]}`, entries, scenario.entry)
			result := postCodecJSON(t, server, scenario.path, body, bearer(server.token))
			if result.status != http.StatusUnprocessableEntity || errorCategory(result) != scenario.category || errorMessage(result) != scenario.message {
				t.Fatalf("status %d body %v, want %s %q", result.status, result.body, scenario.category, scenario.message)
			}
		})
	}
}

// startTestCodecServerInstance returns the server itself, so a test can hold its decode slots.
func startTestCodecServerInstance(t *testing.T) (*LocalCodecServer, string) {
	t.Helper()
	keyFilePath := filepath.Join(t.TempDir(), "key.json")
	if err := GenerateKeyFile(keyFilePath, "k1", false); err != nil {
		t.Fatal(err)
	}
	codec, err := NewLocalCodecFromKeyFile(keyFilePath)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	server, err := NewLocalCodecServer(codec, "token", nil, listener)
	if err != nil {
		t.Fatal(err)
	}
	return server, listener.Addr().String()
}

type firstReadSignal struct {
	reader io.Reader
	read   chan struct{}
	once   bool
}

func (signal *firstReadSignal) Read(buffer []byte) (int, error) {
	if !signal.once {
		signal.once = true
		close(signal.read)
	}
	return signal.reader.Read(buffer)
}

func TestLocalCodecServerReadsDecodeBodyOnlyWithASlot(t *testing.T) {
	server, address := startTestCodecServerInstance(t)
	for range maxConcurrentDecodeRequests {
		server.decodeSlots <- struct{}{}
	}
	body := &firstReadSignal{reader: strings.NewReader(`{"context":{"flow_id":"f"},"envelopes":[]}`), read: make(chan struct{})}
	request := httptest.NewRequest(http.MethodPost, "/decode", body)
	request.Host = address
	request.Header.Set("Authorization", "Bearer token")
	recorder := httptest.NewRecorder()
	handled := make(chan struct{})
	go func() {
		server.ServeHTTP(recorder, request)
		close(handled)
	}()
	select {
	case <-body.read:
		t.Fatal("a third concurrent request read its body before a slot was free")
	case <-time.After(300 * time.Millisecond):
	}
	<-server.decodeSlots
	select {
	case <-handled:
	case <-time.After(5 * time.Second):
		t.Fatal("the request did not finish after a slot was released")
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d body %s", recorder.Code, recorder.Body.String())
	}
}

func gzipEnvelopesJSON(t *testing.T, keyFilePath string, plaintexts [][]byte) string {
	t.Helper()
	keys, err := dex.NewFilePayloadEncryptionKeyProvider(keyFilePath)
	if err != nil {
		t.Fatal(err)
	}
	gzipCodec, err := dex.NewAESGCMPayloadCodec(keys, dex.AESGCMPayloadCodecOptions{Compression: dexpb.PayloadCompression_PAYLOAD_COMPRESSION_GZIP})
	if err != nil {
		t.Fatal(err)
	}
	envelopes, err := gzipCodec.Encode(context.Background(), dex.PayloadContext{FlowID: "f"}, plaintexts)
	if err != nil {
		t.Fatal(err)
	}
	return encodedEnvelopesJSON(t, envelopes)
}

// TestLocalCodecServerChargesTheDecryptedPlaintext uses plaintexts that repeat string_value: the parsed
// Value keeps only the short last copy, but the budget must count every decrypted byte.
func TestLocalCodecServerChargesTheDecryptedPlaintext(t *testing.T) {
	server := startTestCodecServer(t)
	repeated := append(mustMarshalValue(t, strings.Repeat("a", 20<<20)), mustMarshalValue(t, "b")...)
	value := &dexpb.Value{}
	if err := proto.Unmarshal(repeated, value); err != nil || value.GetStringValue() != "b" {
		t.Fatalf("plaintext parses to %v, %v", value, err)
	}
	body := gzipEnvelopesJSON(t, server.keyFilePath, [][]byte{repeated, repeated, repeated, repeated})
	result := postCodecJSON(t, server, "/decode", body, bearer(server.token))
	if result.status != http.StatusRequestEntityTooLarge || errorCategory(result) != CategoryLimitExceeded {
		t.Fatalf("status %d body %v", result.status, result.body)
	}
}

// TestLocalCodecServerBoundsEscapedJSONBeforeMarshaling decodes a string of control characters, which
// protojson escapes to six bytes each, past the response JSON limit.
func TestLocalCodecServerBoundsEscapedJSONBeforeMarshaling(t *testing.T) {
	server := startTestCodecServer(t)
	controlCharacters := strings.Repeat("\x01", maxDecodeResponseJSONBytes/6+1024)
	body := gzipEnvelopesJSON(t, server.keyFilePath, [][]byte{mustMarshalValue(t, controlCharacters)})
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	result := postCodecJSON(t, server, "/decode", body, bearer(server.token))
	runtime.ReadMemStats(&after)
	if result.status != http.StatusRequestEntityTooLarge || errorCategory(result) != CategoryLimitExceeded {
		t.Fatalf("status %d body %v", result.status, result.body)
	}
	// Decrypting and parsing take a few copies of the 16 MiB plaintext; the escaped JSON alone would be 96 MiB.
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > maxDecodeResponseJSONBytes {
		t.Fatalf("decode allocated %d bytes; the JSON was built before the limit check", allocated)
	}
}

func TestMaxValueJSONBytesBoundsProtojson(t *testing.T) {
	for _, value := range []*dexpb.Value{
		{Kind: &dexpb.Value_StringValue{StringValue: "plain \"quoted\" \\ \n\t\x01\x1f é"}},
		{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{Encoding: "json\x02", Payload: []byte("12345")}}},
		{Kind: &dexpb.Value_IntValue{IntValue: -9223372036854775808}},
		{Kind: &dexpb.Value_DoubleValue{DoubleValue: -1.2345678901234567e-308}},
		{Kind: &dexpb.Value_BoolValue{BoolValue: false}},
	} {
		encoded, err := protojson.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if bound := maxValueJSONBytes(value); len(encoded) > bound {
			t.Fatalf("protojson of %v has %d bytes, more than the bound %d", value, len(encoded), bound)
		}
	}
}
