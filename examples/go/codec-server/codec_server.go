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
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	aesGCMNonceAndTagBytes = 28
	reservedEncodingRoot   = "dex/"
)

// payloadCodecHTTPServer serves the codec server protocol of protos/payload-encryption.md section 11.
type payloadCodecHTTPServer struct {
	authenticator       requestAuthenticator
	codecSelector       callerPayloadCodecSelector
	corsOrigins         corsOriginAllowList
	limits              payloadSizeLimits
	maxRequestBodyBytes int64
	maxItemsPerRequest  int
	// maxDecodedBytesPerRequest bounds the Value JSON one /decode response holds.
	maxDecodedBytesPerRequest int
	logger                    *slog.Logger
}

type payloadContextJSON struct {
	FlowType string `json:"flow_type"`
	FlowID   string `json:"flow_id"`
}

type decodeRequestJSON struct {
	Context   *payloadContextJSON `json:"context"`
	Envelopes []string            `json:"envelopes"`
}

type decodeResponseJSON struct {
	Values []json.RawMessage `json:"values"`
}

type encodeRequestJSON struct {
	Context *payloadContextJSON `json:"context"`
	Values  []json.RawMessage   `json:"values"`
}

type encodeResponseJSON struct {
	Envelopes []string `json:"envelopes"`
}

// codecRequestError is a protocol error. message never holds payload bytes, plaintext, or keys.
type codecRequestError struct {
	status   int
	category string
	message  string
	index    *int
	keyID    string
}

type codecErrorResponseJSON struct {
	Error codecErrorDetailJSON `json:"error"`
}

type codecErrorDetailJSON struct {
	Category string `json:"category"`
	Message  string `json:"message"`
	Index    *int   `json:"index,omitempty"`
}

// requestLogFields collects what the access log may record; payloads and credentials are never added.
type requestLogFields struct {
	caller    string
	itemCount int
	category  string
	keyID     string
	index     *int
}

type requestLogFieldsKey struct{}

func (server *payloadCodecHTTPServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/encode", server.logRequests(server.corsOrigins.wrapEndpoint(http.HandlerFunc(server.serveEncode))))
	mux.Handle("/decode", server.logRequests(server.corsOrigins.wrapEndpoint(http.HandlerFunc(server.serveDecode))))
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
	})
	return mux
}

func (server *payloadCodecHTTPServer) serveDecode(writer http.ResponseWriter, request *http.Request) {
	var body decodeRequestJSON
	codec, payloadContext, requestErr := server.admitRequest(request, &body, func() (bool, int) {
		return body.Envelopes != nil, len(body.Envelopes)
	})
	if requestErr != nil {
		writeCodecRequestError(writer, request, *requestErr)
		return
	}
	values, requestErr := server.decodeEnvelopes(request.Context(), codec, payloadContext, body.Envelopes)
	if requestErr != nil {
		writeCodecRequestError(writer, request, *requestErr)
		return
	}
	writeJSON(writer, http.StatusOK, decodeResponseJSON{Values: values})
}

func (server *payloadCodecHTTPServer) serveEncode(writer http.ResponseWriter, request *http.Request) {
	var body encodeRequestJSON
	codec, payloadContext, requestErr := server.admitRequest(request, &body, func() (bool, int) {
		return body.Values != nil, len(body.Values)
	})
	if requestErr != nil {
		writeCodecRequestError(writer, request, *requestErr)
		return
	}
	envelopes, requestErr := server.encodeValues(request.Context(), codec, payloadContext, body.Values)
	if requestErr != nil {
		writeCodecRequestError(writer, request, *requestErr)
		return
	}
	writeJSON(writer, http.StatusOK, encodeResponseJSON{Envelopes: envelopes})
}

// admitRequest checks the method, authentication, authorization, body size, and item count.
// It fills body and returns the caller's codec and the request's PayloadContext.
func (server *payloadCodecHTTPServer) admitRequest(
	request *http.Request,
	body interface{ contextJSON() *payloadContextJSON },
	describeItems func() (present bool, count int),
) (dex.PayloadCodec, dex.PayloadContext, *codecRequestError) {
	if request.Method != http.MethodPost {
		return nil, dex.PayloadContext{}, malformedRequest("use POST")
	}
	// Requiring JSON also forces a CORS preflight for every cross-origin request.
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return nil, dex.PayloadContext{}, malformedRequest("Content-Type must be application/json")
	}
	caller, err := server.authenticator.authenticateRequest(request)
	if err != nil {
		return nil, dex.PayloadContext{}, &codecRequestError{
			status: http.StatusUnauthorized, category: "unauthenticated", message: errCallerUnauthenticated.Error(),
		}
	}
	if fields := logFieldsOf(request); fields != nil {
		fields.caller = caller.Subject
	}
	codec, err := server.codecSelector.selectPayloadCodec(request.Context(), caller)
	if errors.Is(err, errCallerForbidden) {
		return nil, dex.PayloadContext{}, &codecRequestError{
			status: http.StatusForbidden, category: "forbidden", message: errCallerForbidden.Error(),
		}
	}
	if err != nil {
		server.logger.ErrorContext(request.Context(), "select payload codec failed", "internal_error_code", "codec_selector_failed")
		return nil, dex.PayloadContext{}, internalError()
	}
	content, err := io.ReadAll(http.MaxBytesReader(nil, request.Body, server.maxRequestBodyBytes))
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		return nil, dex.PayloadContext{}, &codecRequestError{
			status: http.StatusRequestEntityTooLarge, category: "limit_exceeded",
			message: "request body exceeds the server's limit",
		}
	}
	if err != nil {
		return nil, dex.PayloadContext{}, malformedRequest("request body could not be read")
	}
	if err := json.Unmarshal(content, body); err != nil {
		return nil, dex.PayloadContext{}, malformedRequest("request body is not valid protocol JSON")
	}
	payloadContext := body.contextJSON()
	// flow_type may be empty: clients that know only a Flow ID send "".
	if payloadContext == nil || payloadContext.FlowID == "" {
		return nil, dex.PayloadContext{}, malformedRequest("context.flow_id is required")
	}
	present, count := describeItems()
	if !present {
		return nil, dex.PayloadContext{}, malformedRequest("the request has no item list")
	}
	if fields := logFieldsOf(request); fields != nil {
		fields.itemCount = count
	}
	if count > server.maxItemsPerRequest {
		return nil, dex.PayloadContext{}, &codecRequestError{
			status: http.StatusRequestEntityTooLarge, category: "limit_exceeded",
			message: "the request has more entries than the server's limit",
		}
	}
	return codec, dex.PayloadContext{FlowType: payloadContext.FlowType, FlowID: payloadContext.FlowID}, nil
}

func (body *decodeRequestJSON) contextJSON() *payloadContextJSON { return body.Context }
func (body *encodeRequestJSON) contextJSON() *payloadContextJSON { return body.Context }

// decodeEnvelopes runs decode steps 3 to 14 of the format spec for each envelope.
func (server *payloadCodecHTTPServer) decodeEnvelopes(
	ctx context.Context,
	codec dex.PayloadCodec,
	payloadContext dex.PayloadContext,
	envelopeTexts []string,
) ([]json.RawMessage, *codecRequestError) {
	// Entries decode one at a time so memory stays within the per-request budget.
	values := make([]json.RawMessage, len(envelopeTexts))
	decodedBytes := 0
	for index, envelopeText := range envelopeTexts {
		valueJSON, requestErr := server.decodeEnvelope(
			ctx, codec, payloadContext, envelopeText, server.maxDecodedBytesPerRequest-decodedBytes)
		if requestErr != nil {
			if requestErr.status == http.StatusUnprocessableEntity {
				requestErr.at(index)
			}
			return nil, requestErr
		}
		decodedBytes += len(valueJSON)
		values[index] = valueJSON
	}
	return values, nil
}

func decodedBudgetExceededError() *codecRequestError {
	return &codecRequestError{
		status: http.StatusRequestEntityTooLarge, category: "limit_exceeded",
		message: "decoded values exceed the server's per-request limit; send fewer envelopes per request",
	}
}

// decodeEnvelope parses, decrypts, and checks one envelope, and returns its Value JSON.
// It returns 413 before building JSON larger than remainingBudgetBytes.
func (server *payloadCodecHTTPServer) decodeEnvelope(
	ctx context.Context,
	codec dex.PayloadCodec,
	payloadContext dex.PayloadContext,
	envelopeText string,
	remainingBudgetBytes int,
) (json.RawMessage, *codecRequestError) {
	envelope, requestErr := server.parseEnvelopeText(envelopeText)
	if requestErr != nil {
		return nil, requestErr
	}
	plaintexts, err := codec.Decode(ctx, payloadContext, []*dexpb.PayloadEnvelope{envelope})
	if err != nil {
		return nil, server.classifyCodecError(ctx, err)
	}
	if len(plaintexts) != 1 {
		server.logger.ErrorContext(ctx, "payload codec failed", "internal_error_code", "decode_result_count_mismatch")
		return nil, internalError()
	}
	keyID := publicKeyIDOrEmpty(envelope.GetKeyId())
	if len(plaintexts[0]) > server.limits.MaxPlaintextBytes {
		return nil, entryError("limit_exceeded", "plaintext exceeds the server's limit", keyID)
	}
	value, requestErr := parseDecodedPlaintext(plaintexts[0], keyID)
	if requestErr != nil {
		return nil, requestErr
	}
	valueJSONLength := valueJSONLength(value)
	if valueJSONLength > remainingBudgetBytes {
		return nil, decodedBudgetExceededError()
	}
	return appendValueJSON(make([]byte, 0, valueJSONLength), value), nil
}

// parseEnvelopeText checks the base64 length and the decoded size before decoding or parsing.
func (server *payloadCodecHTTPServer) parseEnvelopeText(envelopeText string) (*dexpb.PayloadEnvelope, *codecRequestError) {
	if len(envelopeText) > server.limits.MaxEncodedTextBytes {
		return nil, entryError("limit_exceeded", "envelope text exceeds the server's limit", "")
	}
	if len(envelopeText)%4 != 0 {
		return nil, entryError("malformed", "envelope is not canonical padded standard base64", "")
	}
	decodedLength := len(envelopeText) / 4 * 3
	decodedLength -= len(envelopeText) - len(strings.TrimRight(envelopeText, "="))
	if decodedLength > server.limits.MaxEnvelopeBytes {
		return nil, entryError("limit_exceeded", "envelope exceeds the server's limit", "")
	}
	envelopeBytes, err := base64.StdEncoding.Strict().DecodeString(envelopeText)
	if err != nil || base64.StdEncoding.EncodeToString(envelopeBytes) != envelopeText {
		return nil, entryError("malformed", "envelope is not canonical padded standard base64", "")
	}
	envelope := &dexpb.PayloadEnvelope{}
	if err := proto.Unmarshal(envelopeBytes, envelope); err != nil {
		return nil, entryError("malformed", "envelope is not a PayloadEnvelope", "")
	}
	return envelope, nil
}

// decodeCategoryMessages holds fixed client messages; codec and key provider error text is never sent or logged.
var decodeCategoryMessages = map[dex.PayloadErrorCategory]string{
	dex.PayloadErrorMalformed:              "envelope is malformed",
	dex.PayloadErrorUnsupportedMarker:      "envelope format version is not supported",
	dex.PayloadErrorUnsupportedCipher:      "envelope cipher is not supported",
	dex.PayloadErrorUnsupportedCompression: "envelope compression is not supported",
	dex.PayloadErrorUnknownKey:             "envelope key is not available to this server",
	dex.PayloadErrorAuthenticationFailed:   "envelope failed authentication",
	dex.PayloadErrorLimitExceeded:          "envelope exceeds a size limit",
	dex.PayloadErrorInvalidPlaintext:       "decrypted plaintext is not an encryptable Value",
}

func (server *payloadCodecHTTPServer) classifyCodecError(ctx context.Context, err error) *codecRequestError {
	var decodeErr *dex.PayloadDecodeError
	if errors.As(err, &decodeErr) {
		if message, known := decodeCategoryMessages[decodeErr.Category]; known {
			keyID := publicKeyIDOrEmpty(decodeErr.KeyID)
			if keyID != "" {
				message += fmt.Sprintf(" (key id %q)", keyID)
			}
			return entryError(string(decodeErr.Category), message, keyID)
		}
	}
	server.logger.ErrorContext(ctx, "payload codec failed", "internal_error_code", "codec_decode_failed")
	return internalError()
}

// publicKeyIDOrEmpty returns keyID when it is a valid key ID, which envelopes carry in clear.
func publicKeyIDOrEmpty(keyID string) string {
	if isValidPayloadKeyID(keyID) {
		return keyID
	}
	return ""
}

// parseDecodedPlaintext runs decode step 14: the plaintext is a Value of an encryptable kind.
func parseDecodedPlaintext(plaintext []byte, keyID string) (*dexpb.Value, *codecRequestError) {
	value := &dexpb.Value{}
	if err := proto.Unmarshal(plaintext, value); err != nil {
		return nil, entryError("invalid_plaintext", "plaintext is not a Value", keyID)
	}
	if problem := describeUnencryptableValue(value); problem != "" {
		return nil, entryError("invalid_plaintext", problem, keyID)
	}
	return value, nil
}

// describeUnencryptableValue returns why value is never encrypted, or "" for the encryptable kinds.
func describeUnencryptableValue(value *dexpb.Value) string {
	switch kind := value.GetKind().(type) {
	case *dexpb.Value_StringValue, *dexpb.Value_IntValue, *dexpb.Value_DoubleValue, *dexpb.Value_BoolValue:
		return ""
	case *dexpb.Value_ObjValue:
		if strings.HasPrefix(kind.ObjValue.GetEncoding(), reservedEncodingRoot) {
			return "object encodings starting with dex/ are reserved"
		}
		return ""
	case nil:
		return "value has no kind"
	default:
		return "null values and blob references are never encrypted"
	}
}

// encodeValues checks each value's kind and sizes first, so failures carry an index.
func (server *payloadCodecHTTPServer) encodeValues(
	ctx context.Context,
	codec dex.PayloadCodec,
	payloadContext dex.PayloadContext,
	valuesJSON []json.RawMessage,
) ([]string, *codecRequestError) {
	plaintexts := make([][]byte, len(valuesJSON))
	for index, valueJSON := range valuesJSON {
		value := &dexpb.Value{}
		if err := protojson.Unmarshal(valueJSON, value); err != nil {
			return nil, entryError("malformed", "entry is not dex.Value JSON", "").at(index)
		}
		if problem := describeUnencryptableValue(value); problem != "" {
			return nil, entryError("invalid_plaintext", problem, "").at(index)
		}
		plaintext, err := proto.Marshal(value)
		if err != nil {
			server.logger.ErrorContext(ctx, "serialize Value failed", "index", index)
			return nil, internalError()
		}
		if len(plaintext) > server.limits.MaxPlaintextBytes ||
			len(plaintext)+aesGCMNonceAndTagBytes > server.limits.MaxDataBytes {
			return nil, entryError("limit_exceeded", "value exceeds the server's limit", "").at(index)
		}
		plaintexts[index] = plaintext
	}
	envelopes, err := codec.Encode(ctx, payloadContext, plaintexts)
	if err != nil {
		server.logger.ErrorContext(ctx, "payload codec failed", "internal_error_code", "codec_encode_failed")
		return nil, internalError()
	}
	if len(envelopes) != len(plaintexts) {
		server.logger.ErrorContext(ctx, "payload codec returned the wrong number of envelopes")
		return nil, internalError()
	}
	envelopeTexts := make([]string, len(envelopes))
	for index, envelope := range envelopes {
		envelopeBytes, err := proto.Marshal(envelope)
		if err != nil {
			server.logger.ErrorContext(ctx, "serialize PayloadEnvelope failed", "index", index)
			return nil, internalError()
		}
		if len(envelopeBytes) > server.limits.MaxEnvelopeBytes {
			return nil, entryError("limit_exceeded", "envelope exceeds the server's limit", envelope.GetKeyId()).at(index)
		}
		envelopeTexts[index] = base64.StdEncoding.EncodeToString(envelopeBytes)
	}
	return envelopeTexts, nil
}

// logRequests writes one structured access log line per request, without bodies or credentials.
func (server *payloadCodecHTTPServer) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		fields := &requestLogFields{}
		recorder := &statusRecordingResponseWriter{ResponseWriter: writer, status: http.StatusOK}
		next.ServeHTTP(recorder, request.WithContext(context.WithValue(request.Context(), requestLogFieldsKey{}, fields)))
		attributes := []any{
			"method", request.Method,
			"path", request.URL.Path,
			"status", recorder.status,
			"duration_ms", time.Since(started).Milliseconds(),
			"origin", request.Header.Get("Origin"),
			"caller", fields.caller,
			"item_count", fields.itemCount,
		}
		if fields.category != "" {
			attributes = append(attributes, "error_category", fields.category, "key_id", fields.keyID)
		}
		if fields.index != nil {
			attributes = append(attributes, "error_index", *fields.index)
		}
		server.logger.InfoContext(request.Context(), "codec request", attributes...)
	})
}

type statusRecordingResponseWriter struct {
	http.ResponseWriter
	status int
}

func (writer *statusRecordingResponseWriter) WriteHeader(status int) {
	writer.status = status
	writer.ResponseWriter.WriteHeader(status)
}

func logFieldsOf(request *http.Request) *requestLogFields {
	fields, _ := request.Context().Value(requestLogFieldsKey{}).(*requestLogFields)
	return fields
}

func malformedRequest(message string) *codecRequestError {
	return &codecRequestError{status: http.StatusBadRequest, category: "malformed", message: message}
}

func internalError() *codecRequestError {
	return &codecRequestError{status: http.StatusInternalServerError, category: "internal", message: "internal error"}
}

func entryError(category string, message string, keyID string) *codecRequestError {
	return &codecRequestError{status: http.StatusUnprocessableEntity, category: category, message: message, keyID: keyID}
}

func (requestErr *codecRequestError) at(index int) *codecRequestError {
	requestErr.index = &index
	return requestErr
}

func writeCodecRequestError(writer http.ResponseWriter, request *http.Request, requestErr codecRequestError) {
	if fields := logFieldsOf(request); fields != nil {
		fields.category = requestErr.category
		fields.keyID = requestErr.keyID
		fields.index = requestErr.index
	}
	writeJSON(writer, requestErr.status, codecErrorResponseJSON{Error: codecErrorDetailJSON{
		Category: requestErr.category,
		Message:  requestErr.message,
		Index:    requestErr.index,
	}})
}

func writeJSON(writer http.ResponseWriter, status int, body any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(body)
}
