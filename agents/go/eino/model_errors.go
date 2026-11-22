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
// has a StatusCode int field or a Response *http.Response field. Several Eino
// model packages keep the status elsewhere: the OpenAI-compatible clients of
// eino-ext (openai, qwen, openrouter, deepseek through go-openai) and Ark use
// HTTPStatusCode, and the genai SDK of the gemini client uses Code. The
// adapter wraps such an error in ModelAPIError, so a 429 or 5xx backs off and
// retries and another 4xx ends the turn at once, as for the Dex model clients.
// Response is set when the original error carried response headers, so a
// Retry-After header still sets the wait. Unwrap returns the original error,
// so errors.As still finds the provider's own error type. Since 0.6.16.
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

// exposeModelHTTPStatus wraps a model error whose HTTP status the Go core cannot read.
func exposeModelHTTPStatus(err error) error {
	if err == nil {
		return nil
	}
	isReadable := false
	status := 0
	var headers http.Header
	visitErrorChain(err, func(current error) bool {
		value, isStruct := structValue(current)
		if !isStruct {
			return false
		}
		if coreReadsStatus(value) {
			isReadable = true
			return true
		}
		if status == 0 {
			status = foreignHTTPStatus(value)
		}
		if headers == nil {
			headers = responseHeaders(value)
		}
		return false
	})
	if isReadable || status < 100 || status > 599 {
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

// coreReadsStatus mirrors the fields the Go core's model-error classifier reads.
func coreReadsStatus(value reflect.Value) bool {
	if field := value.FieldByName("StatusCode"); field.IsValid() && field.CanInt() {
		status := field.Int()
		return status >= 100 && status <= 599
	}
	if field := value.FieldByName("Response"); field.IsValid() && field.CanInterface() {
		if response, isResponse := field.Interface().(*http.Response); isResponse && response != nil {
			return true
		}
	}
	return false
}

func foreignHTTPStatus(value reflect.Value) int {
	if field := value.FieldByName("HTTPStatusCode"); field.IsValid() && field.CanInt() {
		return int(field.Int())
	}
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
