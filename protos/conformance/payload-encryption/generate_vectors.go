// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Command payload-encryption writes vectors.json. Run `go run .` to rewrite it, `go run . -check` to verify it.
package main

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
)

const (
	vectorsFileName = "vectors.json"

	payloadEnvelopeMarker = "dex/1"
	encodedStringPrefix   = "dex-encoded:"

	cipherUnspecified uint64 = 0
	cipherAES256GCM   uint64 = 1
	cipherCustom      uint64 = 2

	compressionNone uint64 = 0
	compressionGzip uint64 = 1

	maxMetadataEntries    = 16
	maxMetadataTotalBytes = 4096
	maxKeyIDBytes         = 64

	// maxEnvelopeBytes is the Server's default 16 MiB Value limit plus 64 KiB of envelope headroom.
	maxEnvelopeBytes    = 16<<20 + 64<<10
	maxEncodedTextBytes = (maxEnvelopeBytes + 2) / 3 * 4
	maxDataBytes        = maxEnvelopeBytes
	maxPlaintextBytes   = 64 << 20

	repetitiveText = "Dex payload encryption compresses repetitive text. "
)

// Error categories name the first decode rule a negative vector breaks.
const (
	errorMalformed              = "malformed"
	errorUnsupportedMarker      = "unsupported_marker"
	errorUnsupportedCipher      = "unsupported_cipher"
	errorUnsupportedCompression = "unsupported_compression"
	errorUnknownKey             = "unknown_key"
	errorAuthenticationFailed   = "authentication_failed"
	errorLimitExceeded          = "limit_exceeded"
	errorInvalidPlaintext       = "invalid_plaintext"
)

func main() {
	check := flag.Bool("check", false, "verify that vectors.json matches the generator output instead of rewriting it")
	flag.Parse()
	generated, err := buildVectorsFileBytes()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *check {
		committed, err := os.ReadFile(vectorsFileName)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if !bytes.Equal(committed, generated) {
			fmt.Fprintf(os.Stderr, "%s is not the generator output; run `go run .` and review the diff\n", vectorsFileName)
			os.Exit(1)
		}
		return
	}
	if err := os.WriteFile(vectorsFileName, generated, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type vectorFile struct {
	Description     string            `json:"description"`
	Marker          string            `json:"marker"`
	StringPrefix    string            `json:"stringPrefix"`
	KeyFile         keyFile           `json:"keyFile"`
	KeysHex         map[string]string `json:"keysHex"`
	Limits          vectorLimits      `json:"limits"`
	ErrorCategories []string          `json:"errorCategories"`
	Passthrough     []passthroughCase `json:"passthrough"`
	Positive        []positiveCase    `json:"positive"`
	Negative        []negativeCase    `json:"negative"`
}

// keyFile uses the key file JSON format that file key providers read.
type keyFile struct {
	CurrentKeyID string            `json:"current_key_id"`
	Keys         map[string]string `json:"keys"`
}

type vectorLimits struct {
	MaxEnvelopeBytes      int `json:"maxEnvelopeBytes"`
	MaxEncodedTextBytes   int `json:"maxEncodedTextBytes"`
	MaxDataBytes          int `json:"maxDataBytes"`
	MaxPlaintextBytes     int `json:"maxPlaintextBytes"`
	MaxKeyIDBytes         int `json:"maxKeyIdBytes"`
	MaxMetadataEntries    int `json:"maxMetadataEntries"`
	MaxMetadataTotalBytes int `json:"maxMetadataTotalBytes"`
}

func defaultVectorLimits() vectorLimits {
	return vectorLimits{
		MaxEnvelopeBytes:      maxEnvelopeBytes,
		MaxEncodedTextBytes:   maxEncodedTextBytes,
		MaxDataBytes:          maxDataBytes,
		MaxPlaintextBytes:     maxPlaintextBytes,
		MaxKeyIDBytes:         maxKeyIDBytes,
		MaxMetadataEntries:    maxMetadataEntries,
		MaxMetadataTotalBytes: maxMetadataTotalBytes,
	}
}

// limitsWith returns the default limits with one change, for a case that lowers a limit.
func limitsWith(change func(limits *vectorLimits)) *vectorLimits {
	limits := defaultVectorLimits()
	change(&limits)
	return &limits
}

// caseInput holds exactly one of Value (proto3 JSON of a dex.Value) or Text (a string field).
type caseInput struct {
	Value json.RawMessage `json:"value,omitempty"`
	Text  *string         `json:"text,omitempty"`
}

type passthroughCase struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Input       caseInput `json:"input"`
}

type envelopeSummary struct {
	Cipher      uint64 `json:"cipher"`
	KeyID       string `json:"keyId"`
	Compression uint64 `json:"compression"`
	NonceHex    string `json:"nonceHex"`
	AADHex      string `json:"aadHex"`
}

type positiveCase struct {
	Name              string          `json:"name"`
	Description       string          `json:"description"`
	Input             caseInput       `json:"input"`
	Envelope          envelopeSummary `json:"envelope"`
	ExpectedPlaintext string          `json:"expectedPlaintext"`
	ExpectedValue     json.RawMessage `json:"expectedValue"`
	ExpectedText      *string         `json:"expectedText,omitempty"`
	// Limits, when set, replaces the default limits for this case.
	Limits *vectorLimits `json:"limits,omitempty"`
}

type negativeCase struct {
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	Input         caseInput `json:"input"`
	ExpectedError string    `json:"expectedError"`
	// Limits, when set, replaces the default limits for this case.
	Limits *vectorLimits `json:"limits,omitempty"`
	// CheckOrder marks a case that breaks several rules and explains which check wins.
	CheckOrder string `json:"checkOrder,omitempty"`
}

// vectorKeys returns fixed keys. k1-alias copies k1, so relabeling k1 breaks only the AAD.
func vectorKeys() map[string][]byte {
	k1 := make([]byte, 32)
	k2 := make([]byte, 32)
	for index := range 32 {
		k1[index] = byte(index)
		k2[index] = byte(0x40 + index)
	}
	return map[string][]byte{"k1": k1, "k2": k2, "k1-alias": bytes.Clone(k1)}
}

func buildVectorsFileBytes() ([]byte, error) {
	file, err := buildVectorsFile()
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(file); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func buildVectorsFile() (vectorFile, error) {
	keys := vectorKeys()
	file := vectorFile{
		Description: "Golden decode vectors for Dex client-side payload encryption, format dex/1. " +
			"Every SDK must decode each positive case to expectedPlaintext and expectedValue, return each passthrough " +
			"input unchanged, and reject each negative case with expectedError. See protos/payload-encryption.md.",
		Marker:       payloadEnvelopeMarker,
		StringPrefix: encodedStringPrefix,
		KeyFile:      keyFile{CurrentKeyID: "k2", Keys: map[string]string{}},
		KeysHex:      map[string]string{},
		Limits:       defaultVectorLimits(),
		ErrorCategories: []string{
			errorMalformed, errorUnsupportedMarker, errorUnsupportedCipher, errorUnsupportedCompression,
			errorUnknownKey, errorAuthenticationFailed, errorLimitExceeded, errorInvalidPlaintext,
		},
	}
	for keyID, key := range keys {
		file.KeyFile.Keys[keyID] = base64.StdEncoding.EncodeToString(key)
		file.KeysHex[keyID] = hex.EncodeToString(key)
	}
	builder := vectorBuilder{keys: keys}
	file.Passthrough = buildPassthroughCases()
	positive, err := builder.buildPositiveCases()
	if err != nil {
		return vectorFile{}, err
	}
	file.Positive = positive
	negative, err := builder.buildNegativeCases()
	if err != nil {
		return vectorFile{}, err
	}
	file.Negative = negative
	limitPositive, limitNegative, err := builder.buildLimitBoundaryCases()
	if err != nil {
		return vectorFile{}, err
	}
	file.Positive = append(file.Positive, limitPositive...)
	file.Negative = append(file.Negative, limitNegative...)
	return file, nil
}

func buildPassthroughCases() []passthroughCase {
	return []passthroughCase{
		{Name: "plaintext-string", Description: "A string Value is not an envelope.", Input: valueInput(stringJSON("hello, dex"))},
		{Name: "plaintext-json-object", Description: "An object with encoding json is not an envelope.", Input: valueInput(objectJSON("json", []byte(`{"orderId":"A-1001"}`)))},
		{Name: "plaintext-int", Description: "An int Value is not an envelope.", Input: valueInput(intJSON(42))},
		{Name: "null", Description: "Null is never encrypted.", Input: valueInput(json.RawMessage(`{"nullValue":null}`))},
		{Name: "object-encoding-dex-without-slash", Description: "Only encodings starting with dex/ are reserved; dex is an ordinary encoding.", Input: valueInput(objectJSON("dex", []byte("payload")))},
		{Name: "text-plain", Description: "Text without the prefix is plaintext.", Input: textInput("payment declined")},
		{Name: "text-empty", Description: "Empty text stays empty.", Input: textInput("")},
		{Name: "text-prefix-without-colon", Description: "The prefix includes the colon.", Input: textInput("dex-encoded")},
		{Name: "text-space-escaped-prefix", Description: "A writer without a codec prepends one space to plaintext that starts with dex-encoded:. " +
			"The prefix must start at the first byte, so readers show this text unchanged, space included.", Input: textInput(" dex-encoded: retry later")},
	}
}

type vectorBuilder struct {
	keys map[string][]byte
}

// envelopeOptions describes one envelope. Zero values mean the reference codec's output for key k1.
type envelopeOptions struct {
	// keyID is the envelope key_id; nil means k1.
	keyID *string
	// encryptionKeyID names the key bytes that seal data; empty means keyID.
	encryptionKeyID string
	// cipher is the envelope cipher; nil means PAYLOAD_CIPHER_AES256_GCM.
	cipher                *uint64
	compression           uint64
	compressPlaintext     bool
	authenticatedKeyID    *string
	authenticatedCompress *uint64
	metadata              map[string][]byte
	unknownFields         []byte
	replaceData           func(data []byte) []byte
}

type builtEnvelope struct {
	bytes   []byte
	data    []byte
	summary envelopeSummary
}

func (builder vectorBuilder) buildEnvelope(caseName string, plaintext []byte, options envelopeOptions) (builtEnvelope, error) {
	keyID := "k1"
	if options.keyID != nil {
		keyID = *options.keyID
	}
	encryptionKeyID := options.encryptionKeyID
	if encryptionKeyID == "" {
		encryptionKeyID = keyID
	}
	cipherValue := cipherAES256GCM
	if options.cipher != nil {
		cipherValue = *options.cipher
	}
	key, ok := builder.keys[encryptionKeyID]
	if !ok {
		return builtEnvelope{}, fmt.Errorf("no vector key %q", encryptionKeyID)
	}
	sealed := plaintext
	if options.compressPlaintext {
		compressed, err := pinnedGzipBytes(plaintext)
		if err != nil {
			return builtEnvelope{}, err
		}
		sealed = compressed
	}
	authenticatedKeyID := keyID
	if options.authenticatedKeyID != nil {
		authenticatedKeyID = *options.authenticatedKeyID
	}
	authenticatedCompression := options.compression
	if options.authenticatedCompress != nil {
		authenticatedCompression = *options.authenticatedCompress
	}
	aad := additionalAuthenticatedData(cipherValue, authenticatedCompression, authenticatedKeyID)
	nonce := vectorNonce(caseName)
	data, err := sealAES256GCM(key, nonce, sealed, aad)
	if err != nil {
		return builtEnvelope{}, err
	}
	if options.replaceData != nil {
		data = options.replaceData(data)
	}
	envelope := marshalPayloadEnvelope(cipherValue, keyID, data, options.compression, options.metadata)
	envelope = append(envelope, options.unknownFields...)
	return builtEnvelope{
		bytes: envelope,
		data:  data,
		summary: envelopeSummary{
			Cipher:      cipherValue,
			KeyID:       keyID,
			Compression: options.compression,
			NonceHex:    hex.EncodeToString(nonce),
			AADHex:      hex.EncodeToString(aad),
		},
	}, nil
}

type positiveSpec struct {
	name          string
	description   string
	plaintext     []byte
	expectedValue json.RawMessage
	options       envelopeOptions
	textFormat    bool
	expectedText  string
}

func (builder vectorBuilder) buildPositiveCases() ([]positiveCase, error) {
	jsonObject := []byte(`{"orderId":"A-1001","amount":42.5,"items":["book","pen"]}`)
	rawObject := []byte{0x00, 0x01, 0x02, 0xfe, 0xff}
	longText := strings.Repeat(repetitiveText, 40)
	errorText := "payment declined for order A-1001"
	unknownEnvelopeField := appendVarintField(nil, 15, 1)
	specs := []positiveSpec{
		{name: "string-ascii", description: "A string Value.", plaintext: stringValue("hello, dex"), expectedValue: stringJSON("hello, dex")},
		{name: "string-unicode", description: "A string Value with multi-byte UTF-8.", plaintext: stringValue("Grüße, 世界 🌍"), expectedValue: stringJSON("Grüße, 世界 🌍")},
		{name: "string-empty", description: "An empty string is still serialized because Value.kind is a oneof.", plaintext: stringValue(""), expectedValue: stringJSON("")},
		{name: "object-json", description: "An object with encoding json.", plaintext: objectValue("json", jsonObject), expectedValue: objectJSON("json", jsonObject)},
		{name: "object-raw", description: "An object with encoding raw and arbitrary bytes.", plaintext: objectValue("raw", rawObject), expectedValue: objectJSON("raw", rawObject)},
		{name: "object-encoding-int", description: "An object whose encoding is the text int must decode as obj_value, not int_value.", plaintext: objectValue("int", []byte("42")), expectedValue: objectJSON("int", []byte("42"))},
		{name: "int-positive", description: "An int Value.", plaintext: intValue(42), expectedValue: intJSON(42)},
		{name: "int-zero", description: "Zero is still serialized because Value.kind is a oneof.", plaintext: intValue(0), expectedValue: intJSON(0)},
		{name: "int-negative", description: "A negative int uses a 10-byte varint.", plaintext: intValue(-7), expectedValue: intJSON(-7)},
		{name: "int-max", description: "The largest int64.", plaintext: intValue(math.MaxInt64), expectedValue: intJSON(math.MaxInt64)},
		{name: "int-min", description: "The smallest int64.", plaintext: intValue(math.MinInt64), expectedValue: intJSON(math.MinInt64)},
		{name: "double-positive", description: "A double Value.", plaintext: doubleValue(3.5), expectedValue: doubleJSON(3.5)},
		{name: "double-negative", description: "A negative double.", plaintext: doubleValue(-2.25), expectedValue: doubleJSON(-2.25)},
		{name: "double-negative-zero", description: "Negative zero keeps its sign bit; compare bits, not ==.", plaintext: doubleValue(math.Copysign(0, -1)), expectedValue: doubleJSON(math.Copysign(0, -1))},
		{name: "double-nan", description: "NaN with bits 0x7ff8000000000000; compare with an is-NaN check, not bits or ==.", plaintext: doubleValue(math.NaN()), expectedValue: doubleJSON(math.NaN())},
		{name: "double-positive-infinity", description: "Positive infinity.", plaintext: doubleValue(math.Inf(1)), expectedValue: doubleJSON(math.Inf(1))},
		{name: "bool-true", description: "A bool Value.", plaintext: boolValue(true), expectedValue: boolJSON(true)},
		{name: "bool-false", description: "False is still serialized because Value.kind is a oneof.", plaintext: boolValue(false), expectedValue: boolJSON(false)},
		{name: "string-gzip", description: "A string compressed with gzip before encryption.", plaintext: stringValue(longText), expectedValue: stringJSON(longText), options: envelopeOptions{compression: compressionGzip, compressPlaintext: true}},
		{name: "key-rotation-current-key", description: "A Value encrypted with the current key k2; the other cases use the older key k1.", plaintext: stringValue("encrypted with k2"), expectedValue: stringJSON("encrypted with k2"), options: envelopeOptions{keyID: pointerTo("k2")}},
		{name: "unknown-envelope-field", description: "An envelope with unknown field 15 decodes; unknown fields are ignored.", plaintext: stringValue("forward compatible"), expectedValue: stringJSON("forward compatible"), options: envelopeOptions{unknownFields: unknownEnvelopeField}},
		{name: "text-error-message", description: "A string field in the dex-encoded: prefix format.", plaintext: stringValue(errorText), expectedValue: stringJSON(errorText), textFormat: true, expectedText: errorText},
	}
	cases := make([]positiveCase, 0, len(specs))
	for _, spec := range specs {
		envelope, err := builder.buildEnvelope(spec.name, spec.plaintext, spec.options)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", spec.name, err)
		}
		vector := positiveCase{
			Name:              spec.name,
			Description:       spec.description,
			Envelope:          envelope.summary,
			ExpectedPlaintext: base64.StdEncoding.EncodeToString(spec.plaintext),
			ExpectedValue:     spec.expectedValue,
		}
		if spec.textFormat {
			vector.Input = textInput(encodedStringPrefix + base64.StdEncoding.EncodeToString(envelope.bytes))
			expectedText := spec.expectedText
			vector.ExpectedText = &expectedText
		} else {
			vector.Input = valueInput(objectJSON(payloadEnvelopeMarker, envelope.bytes))
		}
		cases = append(cases, vector)
	}
	return cases, nil
}

type negativeSpec struct {
	name          string
	description   string
	expectedError string
	plaintext     []byte
	options       envelopeOptions
	textFormat    bool
	checkOrder    string
	// input, when set, replaces the built envelope entirely.
	input *caseInput
}

// customCipherCheckOrder explains why the metadata limit cases cannot break a single rule.
const customCipherCheckOrder = "Also uses PAYLOAD_CIPHER_CUSTOM, which the built-in codec rejects; the metadata limit check runs first. " +
	"No single-rule form exists for the built-in codec, because any metadata breaks its empty-metadata rule."

func (builder vectorBuilder) buildNegativeCases() ([]negativeCase, error) {
	defaultPlaintext := stringValue("secret")
	unknownCompression := uint64(9)
	compressionGzipValue := compressionGzip
	aliasKeyID := "k1"
	oversizedMetadata := map[string][]byte{"blob": bytes.Repeat([]byte{0xab}, maxMetadataTotalBytes+1-len("blob"))}
	tooManyMetadataEntries := map[string][]byte{}
	for index := range maxMetadataEntries + 1 {
		tooManyMetadataEntries[fmt.Sprintf("m%02d", index)] = []byte{byte(index)}
	}
	validEnvelope, err := builder.buildEnvelope("unknown-marker-version", defaultPlaintext, envelopeOptions{})
	if err != nil {
		return nil, err
	}
	invalidBase64 := encodedStringPrefix + "@@@@"
	validTextEnvelope, err := builder.buildEnvelope("text-unpadded-base64", stringValue("secret text"), envelopeOptions{})
	if err != nil {
		return nil, err
	}
	unpaddedBase64 := encodedStringPrefix + base64.RawStdEncoding.EncodeToString(validTextEnvelope.bytes)
	if base64.RawStdEncoding.EncodeToString(validTextEnvelope.bytes) == base64.StdEncoding.EncodeToString(validTextEnvelope.bytes) {
		return nil, fmt.Errorf("text-unpadded-base64 needs an envelope length that is not a multiple of 3")
	}
	specs := []negativeSpec{
		{name: "wrong-tag", description: "The last tag byte is flipped.", expectedError: errorAuthenticationFailed,
			options: envelopeOptions{replaceData: func(data []byte) []byte { data[len(data)-1] ^= 0x01; return data }}},
		{name: "truncated-data-one-byte", description: "The last byte of data is removed.", expectedError: errorAuthenticationFailed,
			options: envelopeOptions{replaceData: func(data []byte) []byte { return data[:len(data)-1] }}},
		{name: "truncated-data-below-minimum", description: "data is shorter than nonce plus tag (28 bytes).", expectedError: errorMalformed,
			options: envelopeOptions{replaceData: func(data []byte) []byte { return data[:27] }}},
		{name: "unknown-key-id", description: "key_id k9 is not in the key file; the tag is valid for k1 bytes.", expectedError: errorUnknownKey,
			options: envelopeOptions{keyID: pointerTo("k9"), encryptionKeyID: "k1"}},
		{name: "unspecified-cipher", description: "cipher is PAYLOAD_CIPHER_UNSPECIFIED (0), authenticated as 0.", expectedError: errorUnsupportedCipher,
			options: envelopeOptions{cipher: pointerTo(cipherUnspecified)}},
		{name: "unknown-cipher", description: "cipher is the unknown value 7, authenticated as 7.", expectedError: errorUnsupportedCipher,
			options: envelopeOptions{cipher: pointerTo(uint64(7))}},
		{name: "custom-cipher-without-custom-codec", description: "PAYLOAD_CIPHER_CUSTOM reaches the built-in AES-GCM codec.", expectedError: errorUnsupportedCipher,
			options: envelopeOptions{cipher: pointerTo(cipherCustom), metadata: map[string][]byte{"codec": []byte("example")}}},
		{name: "unknown-compression", description: "compression is the unknown value 9, authenticated as 9.", expectedError: errorUnsupportedCompression,
			options: envelopeOptions{compression: unknownCompression}},
		{name: "unknown-marker-version", description: "The encoding is dex/2; the payload is a valid dex/1 envelope.", expectedError: errorUnsupportedMarker,
			input: pointerTo(valueInput(objectJSON("dex/2", validEnvelope.bytes)))},
		{name: "metadata-total-too-large", description: "metadata keys and values total maxMetadataTotalBytes + 1 bytes.", expectedError: errorLimitExceeded,
			options: envelopeOptions{cipher: pointerTo(cipherCustom), metadata: oversizedMetadata}, checkOrder: customCipherCheckOrder},
		{name: "metadata-too-many-entries", description: "metadata holds maxMetadataEntries + 1 entries.", expectedError: errorLimitExceeded,
			options: envelopeOptions{cipher: pointerTo(cipherCustom), metadata: tooManyMetadataEntries}, checkOrder: customCipherCheckOrder},
		{name: "metadata-with-builtin-cipher", description: "PAYLOAD_CIPHER_AES256_GCM requires empty metadata.", expectedError: errorMalformed,
			options: envelopeOptions{metadata: map[string][]byte{"note": []byte("x")}}},
		{name: "aad-mismatch-key-id", description: "key_id was changed from k1 to k1-alias, which has the same key bytes.", expectedError: errorAuthenticationFailed,
			options: envelopeOptions{keyID: pointerTo("k1-alias"), authenticatedKeyID: &aliasKeyID}},
		{name: "aad-mismatch-compression", description: "Encrypted without compression, then relabeled as gzip.", expectedError: errorAuthenticationFailed,
			options: envelopeOptions{compression: compressionGzipValue, authenticatedCompress: pointerTo(compressionNone)}},
		{name: "corrupt-gzip", description: "compression is gzip but the authenticated plaintext is not gzip data.", expectedError: errorMalformed,
			options: envelopeOptions{compression: compressionGzip}},
		{name: "envelope-not-protobuf", description: "The payload is not a valid PayloadEnvelope encoding.", expectedError: errorMalformed,
			input: pointerTo(valueInput(objectJSON(payloadEnvelopeMarker, []byte{0xff, 0xff, 0xff})))},
		{name: "empty-payload", description: "An empty payload is an envelope with an empty key_id.", expectedError: errorMalformed,
			input:      pointerTo(valueInput(json.RawMessage(`{"objValue":{"encoding":"dex/1"}}`))),
			checkOrder: "Also has cipher 0 and no data; the key_id check runs first. empty-key-id covers the key_id rule alone."},
		{name: "empty-key-id", description: "key_id is empty.", expectedError: errorMalformed,
			options: envelopeOptions{keyID: pointerTo(""), encryptionKeyID: "k1"}},
		{name: "key-id-too-long", description: "key_id has 65 bytes.", expectedError: errorMalformed,
			options: envelopeOptions{keyID: pointerTo(strings.Repeat("k", maxKeyIDBytes+1)), encryptionKeyID: "k1"}},
		{name: "key-id-with-space", description: "key_id contains a space, which is outside 0x21 to 0x7e.", expectedError: errorMalformed,
			options: envelopeOptions{keyID: pointerTo("k 1"), encryptionKeyID: "k1"}},
		{name: "plaintext-null-value", description: "The authenticated plaintext is a null Value, which is never encrypted.", expectedError: errorInvalidPlaintext,
			plaintext: nullValue()},
		{name: "plaintext-blob-reference", description: "The authenticated plaintext is a blob reference, which is never encrypted.", expectedError: errorInvalidPlaintext,
			plaintext: blobReferenceValue("blob-1")},
		{name: "plaintext-nested-envelope", description: "The authenticated plaintext is itself a dex/1 object; envelopes do not nest.", expectedError: errorInvalidPlaintext,
			plaintext: objectValue(payloadEnvelopeMarker, validEnvelope.bytes)},
		{name: "plaintext-not-a-value", description: "The authenticated plaintext is not a valid Value encoding.", expectedError: errorInvalidPlaintext,
			plaintext: []byte{0xff, 0xff, 0xff}},
		{name: "text-plaintext-not-string", description: "Prefix-format text whose authenticated plaintext is an int Value.", expectedError: errorInvalidPlaintext,
			plaintext: intValue(42), textFormat: true},
		{name: "text-invalid-base64", description: "The text after the prefix is not base64.", expectedError: errorMalformed,
			input: pointerTo(textInput(invalidBase64))},
		{name: "text-unpadded-base64", description: "The base64 after the prefix omits its padding.", expectedError: errorMalformed,
			input: pointerTo(textInput(unpaddedBase64))},
	}
	cases := make([]negativeCase, 0, len(specs))
	for _, spec := range specs {
		vector := negativeCase{Name: spec.name, Description: spec.description, ExpectedError: spec.expectedError, CheckOrder: spec.checkOrder}
		if spec.input != nil {
			vector.Input = *spec.input
			cases = append(cases, vector)
			continue
		}
		plaintext := spec.plaintext
		if plaintext == nil {
			plaintext = defaultPlaintext
		}
		envelope, err := builder.buildEnvelope(spec.name, plaintext, spec.options)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", spec.name, err)
		}
		if spec.textFormat {
			vector.Input = textInput(encodedStringPrefix + base64.StdEncoding.EncodeToString(envelope.bytes))
		} else {
			vector.Input = valueInput(objectJSON(payloadEnvelopeMarker, envelope.bytes))
		}
		cases = append(cases, vector)
	}
	return cases, nil
}

// buildLimitBoundaryCases lowers one size limit per case: at the limit decodes, one byte over fails.
func (builder vectorBuilder) buildLimitBoundaryCases() ([]positiveCase, []negativeCase, error) {
	boundaryText := "limit boundary"
	plaintext := stringValue(boundaryText)
	envelope, err := builder.buildEnvelope("limit-boundary", plaintext, envelopeOptions{})
	if err != nil {
		return nil, nil, err
	}
	gzipText := strings.Repeat(repetitiveText, 40)
	gzipPlaintext := stringValue(gzipText)
	gzipEnvelope, err := builder.buildEnvelope("limit-boundary-gzip", gzipPlaintext, envelopeOptions{compression: compressionGzip, compressPlaintext: true})
	if err != nil {
		return nil, nil, err
	}
	wrongTagEnvelope, err := builder.buildEnvelope("limit-data-checked-before-decryption", plaintext, envelopeOptions{
		replaceData: func(data []byte) []byte { data[len(data)-1] ^= 0x01; return data },
	})
	if err != nil {
		return nil, nil, err
	}
	objectInput := valueInput(objectJSON(payloadEnvelopeMarker, envelope.bytes))
	encodedEnvelope := base64.StdEncoding.EncodeToString(envelope.bytes)
	textInputAtLimit := textInput(encodedStringPrefix + encodedEnvelope)
	gzipInput := valueInput(objectJSON(payloadEnvelopeMarker, gzipEnvelope.bytes))
	notProtobuf := bytes.Repeat([]byte{0xff}, 40)
	notBase64 := strings.Repeat("@", 8)

	positiveAt := func(name string, description string, input caseInput, built builtEnvelope, plaintext []byte, text string, limits *vectorLimits) positiveCase {
		vector := positiveCase{
			Name:              name,
			Description:       description,
			Input:             input,
			Envelope:          built.summary,
			ExpectedPlaintext: base64.StdEncoding.EncodeToString(plaintext),
			ExpectedValue:     stringJSON(text),
			Limits:            limits,
		}
		if input.Text != nil {
			vector.ExpectedText = &text
		}
		return vector
	}
	positive := []positiveCase{
		positiveAt("limit-envelope-at-maximum", "The object payload is exactly maxEnvelopeBytes long.", objectInput, envelope, plaintext, boundaryText,
			limitsWith(func(limits *vectorLimits) { limits.MaxEnvelopeBytes = len(envelope.bytes) })),
		positiveAt("limit-encoded-text-at-maximum", "The base64 after the prefix is exactly maxEncodedTextBytes long.", textInputAtLimit, envelope, plaintext, boundaryText,
			limitsWith(func(limits *vectorLimits) { limits.MaxEncodedTextBytes = len(encodedEnvelope) })),
		positiveAt("limit-data-at-maximum", "data is exactly maxDataBytes long.", objectInput, envelope, plaintext, boundaryText,
			limitsWith(func(limits *vectorLimits) { limits.MaxDataBytes = len(envelope.data) })),
		positiveAt("limit-plaintext-at-maximum", "The decrypted plaintext is exactly maxPlaintextBytes long.", objectInput, envelope, plaintext, boundaryText,
			limitsWith(func(limits *vectorLimits) { limits.MaxPlaintextBytes = len(plaintext) })),
		positiveAt("limit-decompressed-plaintext-at-maximum", "The decompressed plaintext is exactly maxPlaintextBytes long.", gzipInput, gzipEnvelope, gzipPlaintext, gzipText,
			limitsWith(func(limits *vectorLimits) { limits.MaxPlaintextBytes = len(gzipPlaintext) })),
	}
	negative := []negativeCase{
		{Name: "limit-envelope-over-maximum", Description: "The object payload is maxEnvelopeBytes + 1 long.", Input: objectInput, ExpectedError: errorLimitExceeded,
			Limits: limitsWith(func(limits *vectorLimits) { limits.MaxEnvelopeBytes = len(envelope.bytes) - 1 })},
		{Name: "limit-encoded-text-over-maximum", Description: "The base64 after the prefix is maxEncodedTextBytes + 1 long.", Input: textInputAtLimit, ExpectedError: errorLimitExceeded,
			Limits: limitsWith(func(limits *vectorLimits) { limits.MaxEncodedTextBytes = len(encodedEnvelope) - 1 })},
		{Name: "limit-data-over-maximum", Description: "data is maxDataBytes + 1 long.", Input: objectInput, ExpectedError: errorLimitExceeded,
			Limits: limitsWith(func(limits *vectorLimits) { limits.MaxDataBytes = len(envelope.data) - 1 })},
		{Name: "limit-plaintext-over-maximum", Description: "The decrypted plaintext is maxPlaintextBytes + 1 long.", Input: objectInput, ExpectedError: errorLimitExceeded,
			Limits: limitsWith(func(limits *vectorLimits) { limits.MaxPlaintextBytes = len(plaintext) - 1 })},
		{Name: "limit-decompressed-plaintext-over-maximum", Description: "The decompressed plaintext is maxPlaintextBytes + 1 long.", Input: gzipInput, ExpectedError: errorLimitExceeded,
			Limits: limitsWith(func(limits *vectorLimits) { limits.MaxPlaintextBytes = len(gzipPlaintext) - 1 })},
		{Name: "limit-envelope-checked-before-parsing", Description: "An over-limit payload that is not protobuf.", Input: valueInput(objectJSON(payloadEnvelopeMarker, notProtobuf)), ExpectedError: errorLimitExceeded,
			Limits:     limitsWith(func(limits *vectorLimits) { limits.MaxEnvelopeBytes = len(notProtobuf) - 1 }),
			CheckOrder: "Also not protobuf; the payload length check runs before parsing."},
		{Name: "limit-encoded-text-checked-before-base64", Description: "Over-limit text after the prefix that is not base64.", Input: textInput(encodedStringPrefix + notBase64), ExpectedError: errorLimitExceeded,
			Limits:     limitsWith(func(limits *vectorLimits) { limits.MaxEncodedTextBytes = len(notBase64) - 1 }),
			CheckOrder: "Also not base64; the text length check runs before base64 decoding."},
		{Name: "limit-data-checked-before-decryption", Description: "Over-limit data whose tag is also wrong.", Input: valueInput(objectJSON(payloadEnvelopeMarker, wrongTagEnvelope.bytes)), ExpectedError: errorLimitExceeded,
			Limits:     limitsWith(func(limits *vectorLimits) { limits.MaxDataBytes = len(wrongTagEnvelope.data) - 1 }),
			CheckOrder: "Also has a wrong tag; the data length check runs before decryption."},
	}
	return positive, negative, nil
}

func pointerTo[T any](value T) *T {
	return &value
}

// additionalAuthenticatedData returns "dex/1" 0x00 cipher 0x00 compression 0x00 keyID, with
// both enum values written as ASCII decimal digits.
func additionalAuthenticatedData(cipherValue uint64, compression uint64, keyID string) []byte {
	aad := []byte(payloadEnvelopeMarker)
	aad = append(aad, 0x00)
	aad = strconv.AppendUint(aad, cipherValue, 10)
	aad = append(aad, 0x00)
	aad = strconv.AppendUint(aad, compression, 10)
	aad = append(aad, 0x00)
	return append(aad, keyID...)
}

// vectorNonce derives a fixed nonce per case. Only this generator may use fixed nonces.
func vectorNonce(caseName string) []byte {
	digest := sha256.Sum256([]byte("dex payload encryption vector nonce: " + caseName))
	return digest[:12]
}

// sealAES256GCM returns nonce, then ciphertext, then the 16-byte tag.
func sealAES256GCM(key []byte, nonce []byte, plaintext []byte, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return aead.Seal(bytes.Clone(nonce), nonce, plaintext, aad), nil
}

// stringGzipCompressedHex pins the string-gzip case's gzip bytes, so a Go deflate change cannot alter the vector.
const stringGzipCompressedHex = "1f8b08000000000000ffeccbc10985301004d012fefd9fa602abb09110e710d064d95d24e9dca3750853" +
	"c0fb3fbf9d1356d639ca01f6eacbb28d8e3a2e734630e03466cb7613c9991b444444444444443e4ade000000ffff0e21ff71fb070000"

// pinnedGzipBytes returns the pinned gzip bytes, or an error when they do not decompress to plaintext.
func pinnedGzipBytes(plaintext []byte) ([]byte, error) {
	compressed, err := hex.DecodeString(stringGzipCompressedHex)
	if err != nil {
		return nil, err
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	decompressed, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(decompressed, plaintext) {
		return nil, fmt.Errorf("pinned gzip bytes do not decompress to the case plaintext")
	}
	return compressed, nil
}

// Protobuf encoding by hand. Value.kind is a oneof, so its fields are written even when zero.

func appendTag(buffer []byte, fieldNumber uint64, wireType uint64) []byte {
	return binary.AppendUvarint(buffer, fieldNumber<<3|wireType)
}

func appendVarintField(buffer []byte, fieldNumber uint64, value uint64) []byte {
	return binary.AppendUvarint(appendTag(buffer, fieldNumber, 0), value)
}

func appendBytesField(buffer []byte, fieldNumber uint64, value []byte) []byte {
	buffer = binary.AppendUvarint(appendTag(buffer, fieldNumber, 2), uint64(len(value)))
	return append(buffer, value...)
}

func stringValue(text string) []byte {
	return appendBytesField(nil, 3, []byte(text))
}

func objectValue(encoding string, payload []byte) []byte {
	var object []byte
	if encoding != "" {
		object = appendBytesField(object, 1, []byte(encoding))
	}
	if len(payload) > 0 {
		object = appendBytesField(object, 2, payload)
	}
	return appendBytesField(nil, 4, object)
}

func intValue(value int64) []byte {
	return appendVarintField(nil, 5, uint64(value))
}

func doubleValue(value float64) []byte {
	return binary.LittleEndian.AppendUint64(appendTag(nil, 6, 1), math.Float64bits(value))
}

func boolValue(value bool) []byte {
	if value {
		return appendVarintField(nil, 7, 1)
	}
	return appendVarintField(nil, 7, 0)
}

func nullValue() []byte {
	return appendVarintField(nil, 8, 0)
}

func blobReferenceValue(reference string) []byte {
	return appendBytesField(nil, 2, []byte(reference))
}

// marshalPayloadEnvelope writes fields in number order, omits proto3 defaults, and sorts metadata keys.
func marshalPayloadEnvelope(cipherValue uint64, keyID string, data []byte, compression uint64, metadata map[string][]byte) []byte {
	var envelope []byte
	if cipherValue != 0 {
		envelope = appendVarintField(envelope, 1, cipherValue)
	}
	if keyID != "" {
		envelope = appendBytesField(envelope, 2, []byte(keyID))
	}
	if len(data) > 0 {
		envelope = appendBytesField(envelope, 3, data)
	}
	if compression != 0 {
		envelope = appendVarintField(envelope, 4, compression)
	}
	metadataKeys := make([]string, 0, len(metadata))
	for key := range metadata {
		metadataKeys = append(metadataKeys, key)
	}
	sort.Strings(metadataKeys)
	for _, key := range metadataKeys {
		entry := appendBytesField(nil, 1, []byte(key))
		entry = appendBytesField(entry, 2, metadata[key])
		envelope = appendBytesField(envelope, 5, entry)
	}
	return envelope
}

// Proto3 JSON of dex.Value, written by hand to match the canonical mapping.

func valueInput(value json.RawMessage) caseInput {
	return caseInput{Value: value}
}

func textInput(text string) caseInput {
	return caseInput{Text: &text}
}

func mustJSON(value any) json.RawMessage {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		panic(err)
	}
	return json.RawMessage(bytes.TrimSpace(output.Bytes()))
}

func stringJSON(text string) json.RawMessage {
	return json.RawMessage(`{"stringValue":` + string(mustJSON(text)) + `}`)
}

func objectJSON(encoding string, payload []byte) json.RawMessage {
	fields := []string{}
	if encoding != "" {
		fields = append(fields, `"encoding":`+string(mustJSON(encoding)))
	}
	if len(payload) > 0 {
		fields = append(fields, `"payload":`+string(mustJSON(base64.StdEncoding.EncodeToString(payload))))
	}
	return json.RawMessage(`{"objValue":{` + strings.Join(fields, ",") + `}}`)
}

func intJSON(value int64) json.RawMessage {
	return json.RawMessage(`{"intValue":"` + strconv.FormatInt(value, 10) + `"}`)
}

func doubleJSON(value float64) json.RawMessage {
	switch {
	case math.IsNaN(value):
		return json.RawMessage(`{"doubleValue":"NaN"}`)
	case math.IsInf(value, 1):
		return json.RawMessage(`{"doubleValue":"Infinity"}`)
	case math.IsInf(value, -1):
		return json.RawMessage(`{"doubleValue":"-Infinity"}`)
	}
	return json.RawMessage(`{"doubleValue":` + strconv.FormatFloat(value, 'g', -1, 64) + `}`)
}

func boolJSON(value bool) json.RawMessage {
	return json.RawMessage(`{"boolValue":` + strconv.FormatBool(value) + `}`)
}
