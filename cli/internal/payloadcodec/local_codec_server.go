// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package payloadcodec

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	// LocalCodecServerHost is the only address that the local codec server listens on.
	LocalCodecServerHost = "127.0.0.1"

	maxCodecRequestBodyBytes = 32 * 1024 * 1024
	maxCodecRequestEntries   = 1000
	// maxDecodeResponsePlaintextBytes and maxDecodeResponseJSONBytes bound one /decode response.
	maxDecodeResponsePlaintextBytes = 64 * 1024 * 1024
	maxDecodeResponseJSONBytes      = 96 * 1024 * 1024
	maxConcurrentDecodeRequests     = 2
)

// LocalCodecServer serves the codec protocol on 127.0.0.1 behind Host checks, a bearer token, and CORS.
type LocalCodecServer struct {
	codec          *LocalCodec
	token          string
	allowedOrigins map[string]bool
	allowedHosts   map[string]bool
	// decodeSlots bounds concurrent /decode requests, each of which may hold up to its response limits.
	decodeSlots chan struct{}
	httpServer  *http.Server
}

// NewLocalCodecServerToken returns a random token for one start of a local codec server.
func NewLocalCodecServerToken() (string, error) {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("generate codec server token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(tokenBytes), nil
}

// NewLocalCodecServer returns a server for listener, which must listen on 127.0.0.1.
// allowedOrigins are the browser origins, such as http://127.0.0.1:8802, that may call it.
func NewLocalCodecServer(codec *LocalCodec, token string, allowedOrigins []string, listener net.Listener) (*LocalCodecServer, error) {
	if codec == nil {
		return nil, errors.New("local codec server requires a codec")
	}
	if token == "" {
		return nil, errors.New("local codec server requires a token")
	}
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok || !address.IP.IsLoopback() {
		return nil, fmt.Errorf("local codec server must listen on %s", LocalCodecServerHost)
	}
	origins := make(map[string]bool, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		canonicalOrigin, err := CanonicalWebOrigin(origin)
		if err != nil {
			return nil, err
		}
		origins[canonicalOrigin] = true
	}
	port := strconv.Itoa(address.Port)
	server := &LocalCodecServer{
		codec:          codec,
		token:          token,
		allowedOrigins: origins,
		allowedHosts: map[string]bool{
			net.JoinHostPort("127.0.0.1", port): true,
			net.JoinHostPort("localhost", port): true,
			net.JoinHostPort("::1", port):       true,
		},
		decodeSlots: make(chan struct{}, maxConcurrentDecodeRequests),
	}
	server.httpServer = &http.Server{
		Handler:           server,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       time.Minute,
		IdleTimeout:       90 * time.Second,
	}
	return server, nil
}

// CanonicalWebOrigin returns origin as scheme://host[:port] in lower case, or an error when it is not an
// http or https origin without a path.
func CanonicalWebOrigin(origin string) (string, error) {
	parsedOrigin, err := url.Parse(origin)
	if err != nil || (parsedOrigin.Scheme != "http" && parsedOrigin.Scheme != "https") || parsedOrigin.Host == "" ||
		(parsedOrigin.Path != "" && parsedOrigin.Path != "/") || parsedOrigin.RawQuery != "" || parsedOrigin.User != nil {
		return "", fmt.Errorf("web origin %q must look like http://127.0.0.1:8802", origin)
	}
	return strings.ToLower(parsedOrigin.Scheme + "://" + parsedOrigin.Host), nil
}

// Serve serves requests on listener until Close.
func (server *LocalCodecServer) Serve(listener net.Listener) error {
	err := server.httpServer.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Close stops the server and its open connections.
func (server *LocalCodecServer) Close() error {
	return server.httpServer.Close()
}

// ServeHTTP checks Host, Origin, and the token, then serves /encode, /decode, and /healthz.
func (server *LocalCodecServer) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if !server.allowedHosts[strings.ToLower(request.Host)] {
		writeCodecError(response, http.StatusForbidden, CategoryForbidden, "Host must be 127.0.0.1 or localhost with this server's port", nil)
		return
	}
	if origin := request.Header.Get("Origin"); origin != "" {
		canonicalOrigin, err := CanonicalWebOrigin(origin)
		if err != nil || !server.allowedOrigins[canonicalOrigin] {
			writeCodecError(response, http.StatusForbidden, CategoryForbidden, "origin is not allowed", nil)
			return
		}
		response.Header().Set("Access-Control-Allow-Origin", origin)
		response.Header().Add("Vary", "Origin")
	}
	isCodecPath := request.URL.Path == "/encode" || request.URL.Path == "/decode"
	if isCodecPath && request.Method == http.MethodOptions && request.Header.Get("Origin") != "" &&
		request.Header.Get("Access-Control-Request-Method") == http.MethodPost {
		response.Header().Set("Access-Control-Allow-Methods", "POST")
		response.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		response.Header().Set("Access-Control-Max-Age", "600")
		response.WriteHeader(http.StatusNoContent)
		return
	}
	// Every request other than a CORS preflight needs the token, including health checks and unknown paths.
	if !server.isAuthorized(request) {
		writeCodecError(response, http.StatusUnauthorized, CategoryUnauthenticated, "missing or invalid bearer token", nil)
		return
	}
	switch {
	case request.URL.Path == "/healthz" && request.Method == http.MethodGet:
		writeCodecJSON(response, http.StatusOK, map[string]string{"status": "ok"})
	case request.URL.Path == "/encode" && request.Method == http.MethodPost:
		server.serveEncode(response, request)
	case request.URL.Path == "/decode" && request.Method == http.MethodPost:
		server.serveDecode(response, request)
	case isCodecPath:
		writeCodecError(response, http.StatusMethodNotAllowed, CategoryMalformed, "use POST", nil)
	default:
		writeCodecError(response, http.StatusNotFound, CategoryMalformed, "unknown path", nil)
	}
}

func (server *LocalCodecServer) isAuthorized(request *http.Request) bool {
	presented, found := strings.CutPrefix(request.Header.Get("Authorization"), "Bearer ")
	return found && subtle.ConstantTimeCompare([]byte(presented), []byte(server.token)) == 1
}

func (server *LocalCodecServer) serveEncode(response http.ResponseWriter, request *http.Request) {
	var body encodeRequest
	payloadContext, ok := readCodecRequest(response, request, &body, func() (codecServerContext, int) { return body.Context, len(body.Values) })
	if !ok {
		return
	}
	values := make([]*dexpb.Value, 0, len(body.Values))
	for index, encoded := range body.Values {
		value := &dexpb.Value{}
		if err := protojson.Unmarshal(encoded, value); err != nil {
			writeCodecError(response, http.StatusBadRequest, CategoryMalformed, "value is not Value JSON", &index)
			return
		}
		values = append(values, value)
	}
	envelopes, err := server.codec.EncodeValues(request.Context(), payloadContext, values)
	if err != nil {
		writeCodecFailure(response, err)
		return
	}
	encoded := make([]string, 0, len(envelopes))
	for _, envelope := range envelopes {
		encoded = append(encoded, base64.StdEncoding.EncodeToString(envelope))
	}
	writeCodecJSON(response, http.StatusOK, encodeResponse{Envelopes: encoded})
}

func (server *LocalCodecServer) serveDecode(response http.ResponseWriter, request *http.Request) {
	// The slot is taken before the body is read, so waiting requests hold no decoded body in memory.
	select {
	case server.decodeSlots <- struct{}{}:
		defer func() { <-server.decodeSlots }()
	case <-request.Context().Done():
		return
	}
	var body decodeRequest
	payloadContext, ok := readCodecRequest(response, request, &body, func() (codecServerContext, int) { return body.Context, len(body.Envelopes) })
	if !ok {
		return
	}
	// Decoded one at a time, keeping only JSON, so one response stays within the response limits.
	encoded := make([]json.RawMessage, 0, len(body.Envelopes))
	plaintextBytes, jsonBytes := 0, 0
	for index, encodedEnvelope := range body.Envelopes {
		remainingPlaintextBytes := min(maxDecodeResponsePlaintextBytes-plaintextBytes, server.codec.limits.MaxPlaintextBytes)
		if remainingPlaintextBytes <= 0 {
			writeDecodeResponseTooLarge(response)
			return
		}
		envelope, err := DecodeEncodedText(encodedEnvelope, server.codec.limits)
		if err != nil {
			writeCodecFailure(response, withIndex(err, index))
			return
		}
		value, decodedPlaintextBytes, err := server.codec.decodeEnvelopeWithinLimit(request.Context(), payloadContext, envelope, remainingPlaintextBytes)
		if err != nil {
			var codecError *CodecError
			if remainingPlaintextBytes < server.codec.limits.MaxPlaintextBytes &&
				errors.As(err, &codecError) && codecError.Category == CategoryLimitExceeded {
				writeDecodeResponseTooLarge(response)
				return
			}
			writeCodecFailure(response, withIndex(err, index))
			return
		}
		plaintextBytes += decodedPlaintextBytes
		// The bound is checked before marshaling, because escaping can expand a small plaintext several-fold.
		if jsonBytes+maxValueJSONBytes(value) > maxDecodeResponseJSONBytes {
			writeDecodeResponseTooLarge(response)
			return
		}
		valueJSON, err := protojson.Marshal(value)
		if err != nil {
			writeCodecError(response, http.StatusInternalServerError, CategoryInternal, "encode value JSON", nil)
			return
		}
		jsonBytes += len(valueJSON)
		encoded = append(encoded, valueJSON)
	}
	writeCodecJSON(response, http.StatusOK, decodeResponse{Values: encoded})
}

// valueJSONOverheadBytes covers field names, braces, quotes, and the optional spaces protojson may add.
const valueJSONOverheadBytes = 64

// maxValueJSONBytes returns an upper bound of the protojson length of value, computed without marshaling.
func maxValueJSONBytes(value *dexpb.Value) int {
	switch kind := value.GetKind().(type) {
	case *dexpb.Value_StringValue:
		return valueJSONOverheadBytes + escapedJSONStringBytes(kind.StringValue)
	case *dexpb.Value_ObjValue:
		payloadBytes := len(kind.ObjValue.GetPayload())
		return valueJSONOverheadBytes + escapedJSONStringBytes(kind.ObjValue.GetEncoding()) + 4*((payloadBytes+2)/3)
	default:
		// Integers, doubles, and Booleans are at most a few dozen bytes.
		return valueJSONOverheadBytes
	}
}

// escapedJSONStringBytes returns the length of text inside quotes after protojson escaping.
func escapedJSONStringBytes(text string) int {
	length := 0
	for index := 0; index < len(text); index++ {
		switch character := text[index]; {
		case character == '"' || character == '\\' || character == '\b' || character == '\f' ||
			character == '\n' || character == '\r' || character == '\t':
			length += 2
		case character < ' ':
			length += 6
		default:
			length++
		}
	}
	return length
}

func writeDecodeResponseTooLarge(response http.ResponseWriter) {
	writeCodecError(response, http.StatusRequestEntityTooLarge, CategoryLimitExceeded, "decoded values exceed the response limit; send fewer envelopes per request", nil)
}

// readCodecRequest decodes the JSON body and checks its size, entry count, and context.
// It writes the error response and returns false when the request is invalid.
func readCodecRequest(
	response http.ResponseWriter,
	request *http.Request,
	body any,
	describe func() (codecServerContext, int),
) (dex.PayloadContext, bool) {
	request.Body = http.MaxBytesReader(response, request.Body, maxCodecRequestBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(body); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeCodecError(response, http.StatusRequestEntityTooLarge, CategoryLimitExceeded, fmt.Sprintf("request body exceeds %d bytes", maxCodecRequestBodyBytes), nil)
			return dex.PayloadContext{}, false
		}
		writeCodecError(response, http.StatusBadRequest, CategoryMalformed, "request is not valid codec server JSON", nil)
		return dex.PayloadContext{}, false
	}
	payloadContext, entries := describe()
	if entries > maxCodecRequestEntries {
		writeCodecError(response, http.StatusRequestEntityTooLarge, CategoryLimitExceeded, fmt.Sprintf("request has more than %d entries", maxCodecRequestEntries), nil)
		return dex.PayloadContext{}, false
	}
	// Flow type may be empty: clients that know only a Flow ID leave it empty.
	if payloadContext.FlowID == "" {
		writeCodecError(response, http.StatusBadRequest, CategoryMalformed, "context.flow_id is required", nil)
		return dex.PayloadContext{}, false
	}
	return dex.PayloadContext{FlowType: payloadContext.FlowType, FlowID: payloadContext.FlowID}, true
}

func withIndex(err error, index int) error {
	var codecError *CodecError
	if errors.As(err, &codecError) {
		return &CodecError{Category: codecError.Category, Message: codecError.Message, Index: index, KeyID: codecError.KeyID}
	}
	return err
}

// writeCodecFailure writes a fixed message per format category; the original error text never leaves.
func writeCodecFailure(response http.ResponseWriter, err error) {
	var codecError *CodecError
	if !errors.As(err, &codecError) {
		writeCodecError(response, http.StatusInternalServerError, CategoryInternal, "codec failure", nil)
		return
	}
	message, known := publicErrorMessage(codecError)
	if !known {
		writeCodecError(response, http.StatusInternalServerError, CategoryInternal, "codec failure", nil)
		return
	}
	var index *int
	if codecError.Index >= 0 {
		index = &codecError.Index
	}
	writeCodecError(response, http.StatusUnprocessableEntity, codecError.Category, message, index)
}

func writeCodecError(response http.ResponseWriter, status int, category string, message string, index *int) {
	var body errorResponse
	body.Error.Category = category
	body.Error.Message = message
	body.Error.Index = index
	writeCodecJSON(response, status, body)
}

func writeCodecJSON(response http.ResponseWriter, status int, body any) {
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(body)
}
