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
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/superdurable/dex/sdk-go/dex"
)

const payloadKeyBytes = 32

// KMS: replace newPayloadEncryptionKeyProvider with a KMS-backed dex.PayloadEncryptionKeyProvider. See README.md.
func newPayloadEncryptionKeyProvider(config codecServerConfig) (dex.PayloadEncryptionKeyProvider, error) {
	if config.KeyFilePath != "" {
		return dex.NewFilePayloadEncryptionKeyProvider(config.KeyFilePath)
	}
	return newKeyFileJSONPayloadEncryptionKeyProvider([]byte(config.KeysJSON))
}

// keyFileJSONPayloadEncryptionKeyProvider reads the key file format from memory, such as an environment variable.
type keyFileJSONPayloadEncryptionKeyProvider struct {
	currentKeyID string
	keys         map[string][]byte
}

// newKeyFileJSONPayloadEncryptionKeyProvider applies the key file rules of the format spec.
// Errors name key IDs but never contain key bytes.
func newKeyFileJSONPayloadEncryptionKeyProvider(keyFileJSON []byte) (*keyFileJSONPayloadEncryptionKeyProvider, error) {
	var keyFile struct {
		CurrentKeyID string            `json:"current_key_id"`
		Keys         map[string]string `json:"keys"`
	}
	if err := json.Unmarshal(keyFileJSON, &keyFile); err != nil {
		return nil, errors.New("DEX_CODEC_SERVER_KEYS_JSON is not valid key file JSON")
	}
	keys := make(map[string][]byte, len(keyFile.Keys))
	for keyID, encoded := range keyFile.Keys {
		if !isValidPayloadKeyID(keyID) {
			return nil, fmt.Errorf("DEX_CODEC_SERVER_KEYS_JSON: key id %q needs 1 to 64 printable ASCII bytes", keyID)
		}
		key, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil || base64.StdEncoding.EncodeToString(key) != encoded || len(key) != payloadKeyBytes {
			return nil, fmt.Errorf("DEX_CODEC_SERVER_KEYS_JSON: key %q is not 32 bytes of standard padded base64", keyID)
		}
		keys[keyID] = key
	}
	if _, found := keys[keyFile.CurrentKeyID]; !found {
		return nil, fmt.Errorf("DEX_CODEC_SERVER_KEYS_JSON: current_key_id %q is not in keys", keyFile.CurrentKeyID)
	}
	return &keyFileJSONPayloadEncryptionKeyProvider{currentKeyID: keyFile.CurrentKeyID, keys: keys}, nil
}

// CurrentKeyID returns current_key_id for every PayloadContext.
func (provider *keyFileJSONPayloadEncryptionKeyProvider) CurrentKeyID(context.Context, dex.PayloadContext) (string, error) {
	return provider.currentKeyID, nil
}

// Key returns a copy of the key for keyID, or an error when the key is unknown.
func (provider *keyFileJSONPayloadEncryptionKeyProvider) Key(_ context.Context, _ dex.PayloadContext, keyID string) ([]byte, error) {
	key, found := provider.keys[keyID]
	if !found {
		return nil, errors.New("key id is not in DEX_CODEC_SERVER_KEYS_JSON")
	}
	return bytes.Clone(key), nil
}

func isValidPayloadKeyID(keyID string) bool {
	if len(keyID) == 0 || len(keyID) > 64 {
		return false
	}
	for index := range len(keyID) {
		if keyID[index] < 0x21 || keyID[index] > 0x7e {
			return false
		}
	}
	return true
}
