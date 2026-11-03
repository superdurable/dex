// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Package pythonjson writes JSON text byte for byte as Python's json.dumps does.
//
// The Python core writes event payloads, function tool results, and some
// model-visible texts with json.dumps. The Go core writes the same bytes, so
// a stored or streamed value does not depend on the language of the Worker.
package pythonjson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Object is a JSON object whose members keep their order.
type Object []Member

// Member is one key and value of an Object.
type Member struct {
	// Key is the member name.
	Key string
	// Value is the member value: any value Marshal accepts.
	Value any
}

// Options selects the json.dumps arguments to reproduce.
type Options struct {
	// IsCompact uses separators=(",", ":"); otherwise the defaults ", " and ": ".
	IsCompact bool
	// ShouldSortKeys reproduces sort_keys=True.
	ShouldSortKeys bool
	// ShouldEscapeNonASCII reproduces ensure_ascii=True, json.dumps' default.
	ShouldEscapeNonASCII bool
}

// Marshal returns the JSON text json.dumps writes for value.
//
// value may be nil, a bool, a string, any Go integer, a float64, a
// json.Number, a json.RawMessage, an Object, a map with string keys, or a
// slice of those. A json.RawMessage is parsed with its member order kept and
// its numbers read as Python reads them. It returns an error for any other
// type, for a NaN or infinite float, and for invalid raw JSON.
func Marshal(value any, options Options) (string, error) {
	var buffer strings.Builder
	if err := writeValue(&buffer, value, options); err != nil {
		return "", err
	}
	return buffer.String(), nil
}

// ParseOrdered parses JSON text into nil, bool, string, json.Number, []any, and Object values.
//
// Object members keep the order of the text, as Python's json.loads does.
// It returns an error for invalid JSON and for trailing data.
func ParseOrdered(text []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(text))
	decoder.UseNumber()
	value, err := parseValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err == nil {
		return nil, fmt.Errorf("pythonjson: trailing data after the JSON value")
	}
	return value, nil
}

func parseValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("pythonjson: %w", err)
	}
	switch typed := token.(type) {
	case json.Delim:
		if typed == '{' {
			return parseObject(decoder)
		}
		if typed == '[' {
			return parseArray(decoder)
		}
		return nil, fmt.Errorf("pythonjson: unexpected delimiter %q", typed)
	default:
		return typed, nil
	}
}

func parseObject(decoder *json.Decoder) (Object, error) {
	object := Object{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("pythonjson: %w", err)
		}
		key, isString := keyToken.(string)
		if !isString {
			return nil, fmt.Errorf("pythonjson: object key %v is not a string", keyToken)
		}
		value, err := parseValue(decoder)
		if err != nil {
			return nil, err
		}
		// json.loads keeps the last duplicate at the first key's position.
		if index := object.index(key); index >= 0 {
			object[index].Value = value
			continue
		}
		object = append(object, Member{Key: key, Value: value})
	}
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("pythonjson: %w", err)
	}
	return object, nil
}

func parseArray(decoder *json.Decoder) ([]any, error) {
	items := []any{}
	for decoder.More() {
		item, err := parseValue(decoder)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("pythonjson: %w", err)
	}
	return items, nil
}

func (object Object) index(key string) int {
	for index, member := range object {
		if member.Key == key {
			return index
		}
	}
	return -1
}

func writeValue(buffer *strings.Builder, value any, options Options) error {
	switch typed := value.(type) {
	case nil:
		buffer.WriteString("null")
	case bool:
		if typed {
			buffer.WriteString("true")
		} else {
			buffer.WriteString("false")
		}
	case string:
		writeString(buffer, typed, options.ShouldEscapeNonASCII)
	case int:
		buffer.WriteString(strconv.FormatInt(int64(typed), 10))
	case int32:
		buffer.WriteString(strconv.FormatInt(int64(typed), 10))
	case int64:
		buffer.WriteString(strconv.FormatInt(typed, 10))
	case float64:
		text, err := formatFloat(typed)
		if err != nil {
			return err
		}
		buffer.WriteString(text)
	case json.Number:
		text, err := formatNumber(typed)
		if err != nil {
			return err
		}
		buffer.WriteString(text)
	case json.RawMessage:
		parsed, err := ParseOrdered(typed)
		if err != nil {
			return err
		}
		return writeValue(buffer, parsed, options)
	case Object:
		return writeObject(buffer, typed, options)
	case []any:
		return writeArray(buffer, len(typed), func(index int) any { return typed[index] }, options)
	case []string:
		return writeArray(buffer, len(typed), func(index int) any { return typed[index] }, options)
	default:
		return writeReflectedValue(buffer, value, options)
	}
	return nil
}

func writeReflectedValue(buffer *strings.Builder, value any, options Options) error {
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Map:
		if reflected.Type().Key().Kind() != reflect.String {
			return fmt.Errorf("pythonjson: map key type %s is not a string", reflected.Type().Key())
		}
		object := make(Object, 0, reflected.Len())
		iterator := reflected.MapRange()
		for iterator.Next() {
			object = append(object, Member{Key: iterator.Key().String(), Value: iterator.Value().Interface()})
		}
		// A Go map has no order; sorted keys are the only deterministic choice.
		sort.SliceStable(object, func(left, right int) bool { return object[left].Key < object[right].Key })
		return writeObject(buffer, object, options)
	case reflect.Slice, reflect.Array:
		if reflected.Kind() == reflect.Slice && reflected.IsNil() {
			buffer.WriteString("null")
			return nil
		}
		return writeArray(buffer, reflected.Len(), func(index int) any { return reflected.Index(index).Interface() }, options)
	case reflect.String:
		writeString(buffer, reflected.String(), options.ShouldEscapeNonASCII)
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		buffer.WriteString(strconv.FormatInt(reflected.Int(), 10))
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		buffer.WriteString(strconv.FormatUint(reflected.Uint(), 10))
		return nil
	case reflect.Bool:
		return writeValue(buffer, reflected.Bool(), options)
	case reflect.Float32, reflect.Float64:
		return writeValue(buffer, reflected.Float(), options)
	case reflect.Pointer, reflect.Interface:
		if reflected.IsNil() {
			buffer.WriteString("null")
			return nil
		}
		return writeValue(buffer, reflected.Elem().Interface(), options)
	default:
		return fmt.Errorf("pythonjson: cannot write %T as JSON", value)
	}
}

func writeObject(buffer *strings.Builder, object Object, options Options) error {
	members := object
	if options.ShouldSortKeys {
		members = append(Object(nil), object...)
		// Python compares str keys by code point, which UTF-8 byte order preserves.
		sort.SliceStable(members, func(left, right int) bool { return members[left].Key < members[right].Key })
	}
	itemSeparator, keySeparator := separators(options)
	buffer.WriteByte('{')
	for index, member := range members {
		if index > 0 {
			buffer.WriteString(itemSeparator)
		}
		writeString(buffer, member.Key, options.ShouldEscapeNonASCII)
		buffer.WriteString(keySeparator)
		if err := writeValue(buffer, member.Value, options); err != nil {
			return err
		}
	}
	buffer.WriteByte('}')
	return nil
}

func writeArray(buffer *strings.Builder, length int, item func(int) any, options Options) error {
	itemSeparator, _ := separators(options)
	buffer.WriteByte('[')
	for index := range length {
		if index > 0 {
			buffer.WriteString(itemSeparator)
		}
		if err := writeValue(buffer, item(index), options); err != nil {
			return err
		}
	}
	buffer.WriteByte(']')
	return nil
}

func separators(options Options) (itemSeparator string, keySeparator string) {
	if options.IsCompact {
		return ",", ":"
	}
	return ", ", ": "
}

const hexDigits = "0123456789abcdef"

func writeString(buffer *strings.Builder, text string, shouldEscapeNonASCII bool) {
	buffer.WriteByte('"')
	for _, character := range text {
		switch character {
		case '"':
			buffer.WriteString(`\"`)
		case '\\':
			buffer.WriteString(`\\`)
		case '\n':
			buffer.WriteString(`\n`)
		case '\r':
			buffer.WriteString(`\r`)
		case '\t':
			buffer.WriteString(`\t`)
		case '\b':
			buffer.WriteString(`\b`)
		case '\f':
			buffer.WriteString(`\f`)
		default:
			switch {
			case character < 0x20:
				writeUnicodeEscape(buffer, character)
			case character < utf8.RuneSelf || !shouldEscapeNonASCII:
				buffer.WriteRune(character)
			case character > 0xFFFF:
				high, low := surrogatePair(character)
				writeUnicodeEscape(buffer, high)
				writeUnicodeEscape(buffer, low)
			default:
				writeUnicodeEscape(buffer, character)
			}
		}
	}
	buffer.WriteByte('"')
}

func writeUnicodeEscape(buffer *strings.Builder, character rune) {
	buffer.WriteString(`\u`)
	for shift := 12; shift >= 0; shift -= 4 {
		buffer.WriteByte(hexDigits[(character>>shift)&0xF])
	}
}

func surrogatePair(character rune) (rune, rune) {
	offset := character - 0x10000
	return 0xD800 + (offset >> 10), 0xDC00 + (offset & 0x3FF)
}

// formatNumber writes a parsed number as Python's json.loads then json.dumps would.
func formatNumber(number json.Number) (string, error) {
	text := number.String()
	if strings.ContainsAny(text, ".eE") {
		value, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return "", fmt.Errorf("pythonjson: number %s: %w", text, err)
		}
		return formatFloat(value)
	}
	integer, isExact := new(bigInteger).parse(text)
	if !isExact {
		return "", fmt.Errorf("pythonjson: invalid integer %s", text)
	}
	return integer, nil
}

type bigInteger struct{}

// parse returns the canonical decimal of an integer literal of any size, as Python's int has.
func (bigInteger) parse(text string) (string, bool) {
	isNegative := strings.HasPrefix(text, "-")
	digits := strings.TrimPrefix(text, "-")
	if digits == "" || strings.Trim(digits, "0123456789") != "" {
		return "", false
	}
	digits = strings.TrimLeft(digits, "0")
	if digits == "" {
		return "0", true
	}
	if isNegative {
		return "-" + digits, true
	}
	return digits, true
}

// formatFloat returns Python's repr of a float: shortest digits, exponent below 1e-4 or from 1e16.
func formatFloat(value float64) (string, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "", fmt.Errorf("pythonjson: %v is not a JSON number", value)
	}
	scientific := strconv.FormatFloat(value, 'e', -1, 64)
	mantissa, exponentText, _ := strings.Cut(scientific, "e")
	exponent, err := strconv.Atoi(exponentText)
	if err != nil {
		return "", fmt.Errorf("pythonjson: format %v: %w", value, err)
	}
	sign := ""
	if strings.HasPrefix(mantissa, "-") {
		sign, mantissa = "-", mantissa[1:]
	}
	digits := strings.Replace(mantissa, ".", "", 1)
	if exponent < -4 || exponent >= 16 {
		formatted := digits[:1]
		if len(digits) > 1 {
			formatted += "." + digits[1:]
		}
		exponentSign := "+"
		if exponent < 0 {
			exponentSign, exponent = "-", -exponent
		}
		return fmt.Sprintf("%s%se%s%02d", sign, formatted, exponentSign, exponent), nil
	}
	if exponent < 0 {
		return sign + "0." + strings.Repeat("0", -exponent-1) + digits, nil
	}
	if len(digits) <= exponent+1 {
		return sign + digits + strings.Repeat("0", exponent+1-len(digits)) + ".0", nil
	}
	return sign + digits[:exponent+1] + "." + digits[exponent+1:], nil
}

// FormatNumber returns Python's str of the number json.loads reads from a JSON number.
//
// It returns "1" for 1, "1.0" for 1.0, and "1e+16" for 1e16, and an error for
// text that is not a JSON number.
func FormatNumber(number json.Number) (string, error) {
	return formatNumber(number)
}
