// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dexeino

import (
	"net/http"
	"reflect"
)

// genaiPackagePath is the package of Google's genai SDK, whose APIError keeps the HTTP status in Code.
const genaiPackagePath = "google.golang.org/genai"

// ModelAPIError carries the HTTP status of a failed model call where the Dex session reads it.
//
// The Go core reads a model call's HTTP status from an error in the chain that
// has a StatusCode int field, a Response *http.Response field, or, since
// 0.6.16, an HTTPStatusCode int field, the field of go-openai, which the
// OpenAI-compatible clients of eino-ext (openai, qwen, openrouter, deepseek)
// use, and of the Ark SDK. Such an error reaches the session unchanged. The
// adapter wraps an error in ModelAPIError only where that would lose
// something: the genai SDK of the gemini client keeps the status in Code, and
// an error with an HTTPStatusCode field may carry response headers the core
// does not read. A 429 or 5xx then backs off and retries, and another 4xx ends
// the turn at once, as for the Dex model clients. Response is set when the
// original error carried response headers, so a Retry-After header still sets
// the wait. Unwrap returns the original error, so errors.As still finds the
// provider's own error type.
//
// Since 0.6.16.
type ModelAPIError struct {
	// StatusCode is the HTTP status of the model API's answer.
	StatusCode int
	// Response is a response with the status and the original error's headers, or nil.
	Response *http.Response
	cause    error
}

// Error returns the original error's text.
func (modelError *ModelAPIError) Error() string {
	return modelError.cause.Error()
}

// Unwrap returns the original error.
func (modelError *ModelAPIError) Unwrap() error {
	return modelError.cause
}

// exposeModelHTTPStatus wraps a model error whose HTTP status or headers the Go core would not read.
func exposeModelHTTPStatus(err error) error {
	if err == nil {
		return nil
	}
	status := 0
	var headers http.Header
	isUnchanged := false
	visitErrorChain(err, func(current error) bool {
		value, isStruct := structValue(current)
		if !isStruct {
			return false
		}
		if headers == nil {
			headers = responseHeaders(value)
		}
		if coreStatus, isCoreHeaders := coreReadStatus(value); coreStatus != 0 {
			// The core stops at this error, so wrap only to hand it headers it would not read.
			status, isUnchanged = coreStatus, isCoreHeaders || headers == nil
			return true
		}
		if status == 0 {
			status = genaiHTTPStatus(value)
		}
		return false
	})
	if isUnchanged || !isHTTPStatus(status) {
		return err
	}
	exposed := &ModelAPIError{StatusCode: status, cause: err}
	if headers != nil {
		exposed.Response = &http.Response{StatusCode: status, Header: headers}
	}
	return exposed
}

func visitErrorChain(err error, visit func(error) bool) bool {
	for err != nil {
		if visit(err) {
			return true
		}
		switch wrapped := err.(type) {
		case interface{ Unwrap() []error }:
			for _, inner := range wrapped.Unwrap() {
				if visitErrorChain(inner, visit) {
					return true
				}
			}
			return false
		case interface{ Unwrap() error }:
			err = wrapped.Unwrap()
		default:
			return false
		}
	}
	return false
}

func structValue(err error) (reflect.Value, bool) {
	value := reflect.ValueOf(err)
	for value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
		if value.IsNil() {
			return reflect.Value{}, false
		}
		value = value.Elem()
	}
	return value, value.Kind() == reflect.Struct
}

// coreReadStatus returns the HTTP status the Go core reads from one error, or 0, and whether it reads headers there.
//
// It mirrors the core's classifier: Response, then StatusCode, then HTTPStatusCode.
func coreReadStatus(value reflect.Value) (int, bool) {
	status := 0
	isHeaders := false
	if field := value.FieldByName("Response"); field.IsValid() && field.CanInterface() {
		if response, isResponse := field.Interface().(*http.Response); isResponse && response != nil {
			status, isHeaders = response.StatusCode, true
		}
	}
	if field := value.FieldByName("StatusCode"); field.IsValid() && field.CanInt() {
		status = int(field.Int())
	}
	if field := value.FieldByName("HTTPStatusCode"); !isHTTPStatus(status) && field.IsValid() && field.CanInt() {
		status = int(field.Int())
	}
	if !isHTTPStatus(status) {
		return 0, false
	}
	return status, isHeaders
}

func isHTTPStatus(status int) bool {
	return status >= 100 && status <= 599
}

func genaiHTTPStatus(value reflect.Value) int {
	if value.Type().PkgPath() == genaiPackagePath {
		if field := value.FieldByName("Code"); field.IsValid() && field.CanInt() {
			return int(field.Int())
		}
	}
	return 0
}

func responseHeaders(value reflect.Value) http.Header {
	for _, name := range []string{"Header", "Headers", "ResponseHeader"} {
		if field := value.FieldByName(name); field.IsValid() && field.CanInterface() {
			if headers, isHeader := field.Interface().(http.Header); isHeader && headers != nil {
				return headers
			}
		}
	}
	return nil
}
