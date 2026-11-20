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
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/superdurable/dex/sdk-go/dex"
)

var (
	errCallerUnauthenticated = errors.New("credentials are missing or invalid")
	errCallerForbidden       = errors.New("the caller may not use payload keys")
)

// callerIdentity is who an authenticator proved the caller to be.
type callerIdentity struct {
	Subject string
}

// requestAuthenticator authenticates every /encode and /decode request; any error answers 401.
// Implement it for your identity provider.
type requestAuthenticator interface {
	authenticateRequest(request *http.Request) (callerIdentity, error)
}

// bearerTokenRequestAuthenticator accepts one shared secret in "Authorization: Bearer <token>".
type bearerTokenRequestAuthenticator struct {
	tokenDigest [sha256.Size]byte
}

func newBearerTokenRequestAuthenticator(token string) bearerTokenRequestAuthenticator {
	return bearerTokenRequestAuthenticator{tokenDigest: sha256.Sum256([]byte(token))}
}

func (authenticator bearerTokenRequestAuthenticator) authenticateRequest(request *http.Request) (callerIdentity, error) {
	scheme, token, found := strings.Cut(request.Header.Get("Authorization"), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return callerIdentity{}, errCallerUnauthenticated
	}
	// Comparing fixed-size digests keeps the comparison constant-time for any token length.
	digest := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare(digest[:], authenticator.tokenDigest[:]) != 1 {
		return callerIdentity{}, errCallerUnauthenticated
	}
	return callerIdentity{Subject: "bearer-token"}, nil
}

// trustedProxyHeaderRequestAuthenticator trusts a user header set by an SSO proxy.
// Safe only when that proxy is the sole path in.
type trustedProxyHeaderRequestAuthenticator struct {
	headerName string
}

func (authenticator trustedProxyHeaderRequestAuthenticator) authenticateRequest(request *http.Request) (callerIdentity, error) {
	values := request.Header.Values(authenticator.headerName)
	if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		return callerIdentity{}, errCallerUnauthenticated
	}
	return callerIdentity{Subject: strings.TrimSpace(values[0])}, nil
}

func newRequestAuthenticator(config codecServerConfig) (requestAuthenticator, error) {
	switch config.AuthMode {
	case authModeBearerToken:
		return newBearerTokenRequestAuthenticator(config.BearerToken), nil
	case authModeTrustedProxyHeader:
		return trustedProxyHeaderRequestAuthenticator{headerName: config.TrustedIdentityHeader}, nil
	default:
		return nil, fmt.Errorf("unknown auth mode %q", config.AuthMode)
	}
}

// callerPayloadCodecSelector picks keys by authenticated caller, never by the browser-controlled request context.
// errCallerForbidden answers 403.
type callerPayloadCodecSelector interface {
	selectPayloadCodec(ctx context.Context, caller callerIdentity) (dex.PayloadCodec, error)
}

// singleTenantPayloadCodecSelector lets every authenticated caller use one codec.
type singleTenantPayloadCodecSelector struct {
	codec dex.PayloadCodec
}

func (selector singleTenantPayloadCodecSelector) selectPayloadCodec(context.Context, callerIdentity) (dex.PayloadCodec, error) {
	return selector.codec, nil
}
