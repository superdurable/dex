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
	"net/http"
	"strings"
)

// corsOriginAllowList answers CORS for the configured Dex Web origins only.
type corsOriginAllowList struct {
	origins          map[string]struct{}
	allowCredentials bool
}

func newCORSOriginAllowList(origins []string, allowCredentials bool) corsOriginAllowList {
	allowList := corsOriginAllowList{origins: make(map[string]struct{}, len(origins)), allowCredentials: allowCredentials}
	for _, origin := range origins {
		allowList.origins[origin] = struct{}{}
	}
	return allowList
}

// wrapEndpoint runs before authentication: preflights carry no credentials.
// Requests without Origin come from non-browser clients and pass.
func (allowList corsOriginAllowList) wrapEndpoint(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		origin := request.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(writer, request)
			return
		}
		writer.Header().Add("Vary", "Origin")
		if _, allowed := allowList.origins[strings.ToLower(origin)]; !allowed {
			writeCodecRequestError(writer, request, codecRequestError{
				status: http.StatusForbidden, category: "forbidden", message: "origin is not allowed",
			})
			return
		}
		headers := writer.Header()
		headers.Set("Access-Control-Allow-Origin", origin)
		if allowList.allowCredentials {
			headers.Set("Access-Control-Allow-Credentials", "true")
		}
		if request.Method == http.MethodOptions && request.Header.Get("Access-Control-Request-Method") != "" {
			headers.Set("Access-Control-Allow-Methods", "POST")
			headers.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			headers.Set("Access-Control-Max-Age", "600")
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(writer, request)
	})
}
