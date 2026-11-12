// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package launchcontract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// SchemaViolation is the first rule of a contract schema that a JSON value
// breaks, named like a JSON Schema validator error.
type SchemaViolation struct {
	// Path is the JSON Pointer of the failing instance, such as
	// "/dex/auth/mode"; empty for the root.
	Path string
	// Keyword is the failing JSON Schema keyword, such as "required".
	Keyword string
}

func (violation *SchemaViolation) Error() string {
	return fmt.Sprintf("%s fails %q", displayedPointer(violation.Path), violation.Keyword)
}

func displayedPointer(path string) string {
	if path == "" {
		return "the document"
	}
	return path
}

// The contract schemas end patterns with (?![\s\S]); \z is Go's equivalent.
var (
	harnessNamePattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*\z`)
	lowercaseNamePattern  = regexp.MustCompile(`^[a-z][a-z0-9_]*\z`)
	idempotencyKeyPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\z`)
	sha256HexPattern      = regexp.MustCompile(`^[0-9a-f]{64}\z`)
	sessionIDPattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*-[0-9]{8}-[0-9]{6}-(?:[0-9a-f]{6}|[0-9a-f]{12})\z`)
)

// maximumRequestID is 2^53-1, the largest ID every JSON reader keeps exactly.
const maximumRequestID = 9007199254740991

// decodeJSONValue decodes one JSON value with numbers kept as json.Number.
func decodeJSONValue(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if decoder.More() {
		return nil, fmt.Errorf("more than one JSON value")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return nil, fmt.Errorf("more than one JSON value")
	}
	return value, nil
}

// jsonRules checks one JSON value against hand-written schema rules and keeps
// the first violation.
type jsonRules struct {
	violation *SchemaViolation
}

func (rules *jsonRules) isFailed() bool {
	return rules.violation != nil
}

func (rules *jsonRules) fail(path string, keyword string) {
	if rules.violation == nil {
		rules.violation = &SchemaViolation{Path: path, Keyword: keyword}
	}
}

// object returns value as an object, or records a type violation.
func (rules *jsonRules) object(path string, value any) (map[string]any, bool) {
	if rules.isFailed() {
		return nil, false
	}
	object, isObject := value.(map[string]any)
	if !isObject {
		rules.fail(path, "type")
		return nil, false
	}
	return object, true
}

// required records a violation at path when object lacks one of names.
func (rules *jsonRules) required(path string, object map[string]any, names ...string) bool {
	if rules.isFailed() {
		return false
	}
	for _, name := range names {
		if _, exists := object[name]; !exists {
			rules.fail(path, "required")
			return false
		}
	}
	return true
}

// forbidden records a "not" violation for each present name.
func (rules *jsonRules) forbidden(path string, object map[string]any, names ...string) {
	for _, name := range names {
		if _, exists := object[name]; exists {
			rules.fail(path+"/"+name, "not")
		}
	}
}

func (rules *jsonRules) string(path string, value any, minimumLength int) (string, bool) {
	if rules.isFailed() {
		return "", false
	}
	text, isString := value.(string)
	if !isString {
		rules.fail(path, "type")
		return "", false
	}
	if len([]rune(text)) < minimumLength {
		rules.fail(path, "minLength")
		return "", false
	}
	return text, true
}

func (rules *jsonRules) nullableString(path string, value any) {
	if value == nil {
		return
	}
	rules.string(path, value, 0)
}

func (rules *jsonRules) pattern(path string, value any, pattern *regexp.Regexp) (string, bool) {
	text, isString := rules.string(path, value, 0)
	if !isString {
		return "", false
	}
	if !pattern.MatchString(text) {
		rules.fail(path, "pattern")
		return "", false
	}
	return text, true
}

func (rules *jsonRules) boolean(path string, value any) {
	if rules.isFailed() {
		return
	}
	if _, isBoolean := value.(bool); !isBoolean {
		rules.fail(path, "type")
	}
}

func (rules *jsonRules) enum(path string, value any, allowed ...string) (string, bool) {
	if rules.isFailed() {
		return "", false
	}
	text, isString := value.(string)
	if isString {
		for _, candidate := range allowed {
			if text == candidate {
				return text, true
			}
		}
	}
	rules.fail(path, "enum")
	return "", false
}

func (rules *jsonRules) constant(path string, value any, expected string) {
	if rules.isFailed() {
		return
	}
	if text, isString := value.(string); !isString || text != expected {
		rules.fail(path, "const")
	}
}

// integer returns value as an integer within [minimum, maximum].
func (rules *jsonRules) integer(path string, value any, minimum int64, maximum int64) (int64, bool) {
	if rules.isFailed() {
		return 0, false
	}
	number, isInteger := jsonInteger(value)
	if !isInteger {
		rules.fail(path, "type")
		return 0, false
	}
	if number < minimum {
		rules.fail(path, "minimum")
		return 0, false
	}
	if number > maximum {
		rules.fail(path, "maximum")
		return 0, false
	}
	return number, true
}

func (rules *jsonRules) array(path string, value any) ([]any, bool) {
	if rules.isFailed() {
		return nil, false
	}
	items, isArray := value.([]any)
	if !isArray {
		rules.fail(path, "type")
		return nil, false
	}
	return items, true
}

// jsonInteger reports whether value is a JSON number with an integer value
// that fits int64. JSON Schema counts 1.0 as an integer.
func jsonInteger(value any) (int64, bool) {
	number, isNumber := value.(json.Number)
	if !isNumber {
		return 0, false
	}
	if integer, err := strconv.ParseInt(number.String(), 10, 64); err == nil {
		return integer, true
	}
	floating, err := number.Float64()
	if err != nil || floating != float64(int64(floating)) {
		return 0, false
	}
	if strings.ContainsAny(number.String(), "eE.") && floating >= -9.2e18 && floating <= 9.2e18 {
		return int64(floating), true
	}
	return 0, false
}

func itemPath(path string, index int) string {
	return path + "/" + strconv.Itoa(index)
}
