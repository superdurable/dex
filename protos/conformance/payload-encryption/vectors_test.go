// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package main

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

// Every SDK decodes the vectors in its own tests; Go does so in sdk-go/dex.

func TestVectorsFileIsGeneratorOutput(t *testing.T) {
	committed, err := os.ReadFile(vectorsFileName)
	if err != nil {
		t.Fatal(err)
	}
	generated, err := buildVectorsFileBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(committed, generated) {
		t.Fatalf("%s is not the generator output; run `go run .` in this directory and review the diff", vectorsFileName)
	}
}

func TestAdditionalAuthenticatedDataWorkedExample(t *testing.T) {
	// The worked example in protos/payload-encryption.md.
	got := hex.EncodeToString(additionalAuthenticatedData(1, 0, "k1"))
	if want := "6465782f3100310030006b31"; got != want {
		t.Fatalf("AAD = %s, want %s", got, want)
	}
}

func TestKeyFileHoldsAES256Keys(t *testing.T) {
	data, err := os.ReadFile(vectorsFileName)
	if err != nil {
		t.Fatal(err)
	}
	var file vectorFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		t.Fatal(err)
	}
	if _, ok := file.KeyFile.Keys[file.KeyFile.CurrentKeyID]; !ok {
		t.Fatalf("current_key_id %q is not in keys", file.KeyFile.CurrentKeyID)
	}
	for keyID, encoded := range file.KeyFile.Keys {
		key, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil {
			t.Fatalf("key %q: %v", keyID, err)
		}
		if len(key) != 32 {
			t.Fatalf("key %q has %d bytes, want 32", keyID, len(key))
		}
		if hex.EncodeToString(key) != file.KeysHex[keyID] {
			t.Fatalf("key %q: keysHex does not match keyFile", keyID)
		}
	}
}
