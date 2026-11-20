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
	"encoding/base64"
	"math"
	"strconv"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

// Decoded Values skip protojson so their exact JSON size is known before allocating it.

const hexDigits = "0123456789abcdef"

// valueJSONLength returns the exact byte length appendValueJSON writes for value.
// value must hold an encryptable kind; describeUnencryptableValue checks that first.
func valueJSONLength(value *dexpb.Value) int {
	switch kind := value.GetKind().(type) {
	case *dexpb.Value_StringValue:
		return len(`{"stringValue":}`) + quotedJSONStringLength(kind.StringValue)
	case *dexpb.Value_IntValue:
		return len(`{"intValue":""}`) + len(strconv.FormatInt(kind.IntValue, 10))
	case *dexpb.Value_DoubleValue:
		return len(`{"doubleValue":}`) + len(appendDoubleJSON(nil, kind.DoubleValue))
	case *dexpb.Value_BoolValue:
		return len(`{"boolValue":}`) + len(strconv.FormatBool(kind.BoolValue))
	case *dexpb.Value_ObjValue:
		length := len(`{"objValue":{}}`)
		encoding, payload := kind.ObjValue.GetEncoding(), kind.ObjValue.GetPayload()
		if encoding != "" {
			length += len(`"encoding":`) + quotedJSONStringLength(encoding)
		}
		if len(payload) > 0 {
			if encoding != "" {
				length++
			}
			length += len(`"payload":""`) + base64.StdEncoding.EncodedLen(len(payload))
		}
		return length
	default:
		return 0
	}
}

// appendValueJSON appends value in the proto3 JSON mapping of dex.Value.
func appendValueJSON(buffer []byte, value *dexpb.Value) []byte {
	switch kind := value.GetKind().(type) {
	case *dexpb.Value_StringValue:
		buffer = append(buffer, `{"stringValue":`...)
		buffer = appendQuotedJSONString(buffer, kind.StringValue)
	case *dexpb.Value_IntValue:
		buffer = append(buffer, `{"intValue":"`...)
		buffer = strconv.AppendInt(buffer, kind.IntValue, 10)
		buffer = append(buffer, '"')
	case *dexpb.Value_DoubleValue:
		buffer = append(buffer, `{"doubleValue":`...)
		buffer = appendDoubleJSON(buffer, kind.DoubleValue)
	case *dexpb.Value_BoolValue:
		buffer = append(buffer, `{"boolValue":`...)
		buffer = strconv.AppendBool(buffer, kind.BoolValue)
	case *dexpb.Value_ObjValue:
		buffer = append(buffer, `{"objValue":{`...)
		encoding, payload := kind.ObjValue.GetEncoding(), kind.ObjValue.GetPayload()
		if encoding != "" {
			buffer = append(buffer, `"encoding":`...)
			buffer = appendQuotedJSONString(buffer, encoding)
		}
		if len(payload) > 0 {
			if encoding != "" {
				buffer = append(buffer, ',')
			}
			buffer = append(buffer, `"payload":"`...)
			buffer = base64.StdEncoding.AppendEncode(buffer, payload)
			buffer = append(buffer, '"')
		}
		buffer = append(buffer, '}')
	}
	return append(buffer, '}')
}

// appendDoubleJSON writes NaN and infinities as proto3 JSON strings and keeps negative zero as -0.
func appendDoubleJSON(buffer []byte, number float64) []byte {
	switch {
	case math.IsNaN(number):
		return append(buffer, `"NaN"`...)
	case math.IsInf(number, 1):
		return append(buffer, `"Infinity"`...)
	case math.IsInf(number, -1):
		return append(buffer, `"-Infinity"`...)
	}
	return strconv.AppendFloat(buffer, number, 'g', -1, 64)
}

// quotedJSONStringLength counts escapes: proto3 strings are valid UTF-8, so other bytes pass through.
func quotedJSONStringLength(text string) int {
	length := 2
	for index := 0; index < len(text); index++ {
		switch character := text[index]; {
		case character == '"' || character == '\\' || character == '\b' || character == '\f' ||
			character == '\n' || character == '\r' || character == '\t':
			length += 2
		case character < 0x20:
			length += 6
		default:
			length++
		}
	}
	return length
}

func appendQuotedJSONString(buffer []byte, text string) []byte {
	buffer = append(buffer, '"')
	for index := 0; index < len(text); index++ {
		character := text[index]
		switch character {
		case '"', '\\':
			buffer = append(buffer, '\\', character)
		case '\b':
			buffer = append(buffer, '\\', 'b')
		case '\f':
			buffer = append(buffer, '\\', 'f')
		case '\n':
			buffer = append(buffer, '\\', 'n')
		case '\r':
			buffer = append(buffer, '\\', 'r')
		case '\t':
			buffer = append(buffer, '\\', 't')
		default:
			if character < 0x20 {
				buffer = append(buffer, '\\', 'u', '0', '0', hexDigits[character>>4], hexDigits[character&0xf])
			} else {
				buffer = append(buffer, character)
			}
		}
	}
	return append(buffer, '"')
}
