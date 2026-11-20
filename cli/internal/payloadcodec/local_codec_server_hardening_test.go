// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package payloadcodec

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/protobuf/proto"
)

const providerSentinel = "SENTINEL-provider-secret-7f3a"

// leakingKeyProvider fails with a secret in its error text, which must never reach a codec server response.
type leakingKeyProvider struct{}

func (provider leakingKeyProvider) CurrentKeyID(context.Context, dex.PayloadContext) (string, error) {
	return "", errors.New(providerSentinel)
}

func (provider leakingKeyProvider) Key(context.Context, dex.PayloadContext, string) ([]byte, error) {
	return nil, errors.New(providerSentinel)
}

func startCodecServerWithCodec(t *testing.T, codec *LocalCodec) runningCodecServer {
	t.Helper()
	token, err := NewLocalCodecServerToken()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewLocalCodecServer(codec, token, []string{testWebOrigin}, listener)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return runningCodecServer{endpoint: "http://" + listener.Addr().String(), token: token}
}

func encodedEnvelopesJSON(t *testing.T, envelopes []*dexpb.PayloadEnvelope) string {
	t.Helper()
	encoded := make([]string, 0, len(envelopes))
	for _, envelope := range envelopes {
		envelopeBytes, err := proto.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		encoded = append(encoded, base64.StdEncoding.EncodeToString(envelopeBytes))
	}
	body, err := json.Marshal(map[string]any{"context": map[string]string{"flow_id": "f"}, "envelopes": encoded})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestLocalCodecServerNeverReturnsKeyProviderText(t *testing.T) {
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	keyFilePath := filepath.Join(t.TempDir(), "key.json")
	if err := GenerateKeyFile(keyFilePath, "k1", false); err != nil {
		t.Fatal(err)
	}
	envelopes, err := sdkCodec(t, keyFilePath).Encode(context.Background(), dex.PayloadContext{FlowID: "f"}, [][]byte{mustMarshalValue(t, "secret")})
	if err != nil {
		t.Fatal(err)
	}
	codec, err := newLocalCodec(leakingKeyProvider{}, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	server := startCodecServerWithCodec(t, codec)
	decode := postCodecJSON(t, server, "/decode", encodedEnvelopesJSON(t, envelopes), bearer(server.token))
	encode := postCodecJSON(t, server, "/encode", `{"context":{"flow_id":"f"},"values":[{"stringValue":"x"}]}`, bearer(server.token))
	if decode.status != http.StatusUnprocessableEntity || errorCategory(decode) != "unknown_key" {
		t.Fatalf("decode = %d %v", decode.status, decode.body)
	}
	if encode.status != http.StatusInternalServerError || errorCategory(encode) != CategoryInternal {
		t.Fatalf("encode = %d %v", encode.status, encode.body)
	}
	for _, text := range []string{responseText(decode.body), responseText(encode.body), logs.String()} {
		if strings.Contains(text, providerSentinel) {
			t.Fatalf("key provider text leaked: %s", text)
		}
	}
}

func responseText(body map[string]any) string {
	encoded, _ := json.Marshal(body)
	return string(encoded)
}

func mustMarshalValue(t *testing.T, text string) []byte {
	t.Helper()
	plaintext, err := proto.Marshal(&dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: text}})
	if err != nil {
		t.Fatal(err)
	}
	return plaintext
}

// TestLocalCodecServerLimitsCumulativeDecodeOutput sends small gzip envelopes that expand to more plaintext
// than one response may hold.
func TestLocalCodecServerLimitsCumulativeDecodeOutput(t *testing.T) {
	server := startTestCodecServer(t)
	keys, err := dex.NewFilePayloadEncryptionKeyProvider(server.keyFilePath)
	if err != nil {
		t.Fatal(err)
	}
	gzipCodec, err := dex.NewAESGCMPayloadCodec(keys, dex.AESGCMPayloadCodecOptions{Compression: dexpb.PayloadCompression_PAYLOAD_COMPRESSION_GZIP})
	if err != nil {
		t.Fatal(err)
	}
	plaintext := mustMarshalValue(t, strings.Repeat("a", 1<<20))
	envelopeCount := maxDecodeResponsePlaintextBytes/len(plaintext) + 2
	plaintexts := make([][]byte, envelopeCount)
	for index := range plaintexts {
		plaintexts[index] = plaintext
	}
	envelopes, err := gzipCodec.Encode(context.Background(), dex.PayloadContext{FlowID: "f"}, plaintexts)
	if err != nil {
		t.Fatal(err)
	}
	body := encodedEnvelopesJSON(t, envelopes)
	if len(body) > 1<<20 {
		t.Fatalf("request body has %d bytes; the envelopes should compress well", len(body))
	}
	tooLarge := postCodecJSON(t, server, "/decode", body, bearer(server.token))
	if tooLarge.status != http.StatusRequestEntityTooLarge || errorCategory(tooLarge) != CategoryLimitExceeded {
		t.Fatalf("cumulative decode = %d %v", tooLarge.status, tooLarge.body)
	}
	fitting := postCodecJSON(t, server, "/decode", encodedEnvelopesJSON(t, envelopes[:4]), bearer(server.token))
	if fitting.status != http.StatusOK || len(fitting.body["values"].([]any)) != 4 {
		t.Fatalf("decode within the limit = %d", fitting.status)
	}
}

func TestLocalCodecServerRequiresTokenForEveryRoute(t *testing.T) {
	server := startTestCodecServer(t)
	request := func(method string, path string, headers map[string]string) codecHTTPResult {
		httpRequest, err := http.NewRequest(method, server.endpoint+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		for name, value := range headers {
			httpRequest.Header.Set(name, value)
		}
		return doCodecRequest(t, httpRequest)
	}
	for _, scenario := range []struct {
		name    string
		method  string
		path    string
		headers map[string]string
		status  int
	}{
		{"health without token", http.MethodGet, "/healthz", nil, http.StatusUnauthorized},
		{"unknown path without token", http.MethodGet, "/keys", nil, http.StatusUnauthorized},
		{"wrong method without token", http.MethodGet, "/decode", nil, http.StatusUnauthorized},
		{"OPTIONS that is not a preflight", http.MethodOptions, "/decode", map[string]string{"Origin": testWebOrigin}, http.StatusUnauthorized},
		{"health with token", http.MethodGet, "/healthz", bearer(server.token), http.StatusOK},
		{"unknown path with token", http.MethodGet, "/keys", bearer(server.token), http.StatusNotFound},
		{"wrong method with token", http.MethodGet, "/decode", bearer(server.token), http.StatusMethodNotAllowed},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if result := request(scenario.method, scenario.path, scenario.headers); result.status != scenario.status {
				t.Fatalf("status %d body %v, want %d", result.status, result.body, scenario.status)
			}
		})
	}
}

func TestDevCodecServerDiscoveryRejectsUnsafeLocations(t *testing.T) {
	discovery := DevCodecServerDiscovery{Endpoint: "http://127.0.0.1:41234", Token: "token"}

	privateDirectory := filepath.Join(t.TempDir(), "state")
	path := DevCodecServerDiscoveryPath(privateDirectory, 8801)
	content, err := WriteDevCodecServerDiscovery(path, discovery)
	if err != nil {
		t.Fatal(err)
	}
	if read, found, err := ReadDevCodecServerDiscovery(path); err != nil || !found || read != discovery {
		t.Fatalf("read = %v %v %v", read, found, err)
	}

	if err := os.Chmod(privateDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadDevCodecServerDiscovery(path); err == nil || !strings.Contains(err.Error(), "accessible by other users") {
		t.Fatalf("read a 0644 discovery file: %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, found, err := ReadDevCodecServerDiscovery(path); err != nil || !found {
		t.Fatalf("read a 0600 file in a 0755 directory: %v %v", found, err)
	}
	if err := os.Chmod(privateDirectory, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadDevCodecServerDiscovery(path); err == nil || !strings.Contains(err.Error(), "writable by other users") {
		t.Fatalf("read in a permissive directory: %v", err)
	}
	if _, err := WriteDevCodecServerDiscovery(path, discovery); err == nil {
		t.Fatal("write in a permissive directory succeeded")
	}
	if err := os.Chmod(privateDirectory, 0o700); err != nil {
		t.Fatal(err)
	}

	linkedDirectory := filepath.Join(t.TempDir(), "linked-state")
	if err := os.Symlink(privateDirectory, linkedDirectory); err != nil {
		t.Fatal(err)
	}
	linkedPath := DevCodecServerDiscoveryPath(linkedDirectory, 8801)
	if _, _, err := ReadDevCodecServerDiscovery(linkedPath); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("read through a symlinked directory: %v", err)
	}
	if _, err := WriteDevCodecServerDiscovery(linkedPath, discovery); err == nil {
		t.Fatal("write through a symlinked directory succeeded")
	}

	external := DevCodecServerDiscovery{Endpoint: "https://codec.example.com", Token: "token"}
	if _, err := WriteDevCodecServerDiscovery(path, external); err == nil {
		t.Fatal("wrote an external endpoint")
	}
	externalContent, err := json.Marshal(external)
	if err != nil {
		t.Fatal(err)
	}
	if err := writePrivateFile(path, externalContent, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadDevCodecServerDiscovery(path); err == nil || !strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("read an external endpoint: %v", err)
	}

	if err := RemoveDevCodecServerDiscovery(path, content); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("removed a discovery file that another process replaced")
	}
	if err := RemoveDevCodecServerDiscovery(path, externalContent); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("own discovery file was not removed: %v", err)
	}
}
