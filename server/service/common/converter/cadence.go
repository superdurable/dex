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

package converter

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"reflect"

	"go.uber.org/cadence/encoded"
	"google.golang.org/protobuf/proto"
)

const (
	cadenceMagic       = "IWFDC"
	cadenceVersion     = uint8(1)
	cadenceHeaderLen   = 5 + 1 + 4 // magic + version + frame_count
	cadenceFrameHdrLen = 1 + 1 + 4 // kind + nil + length

	kindProto = uint8(1)
	kindJSON  = uint8(2)
	kindRaw   = uint8(3)

	nilFlagFalse = uint8(0)
	nilFlagTrue  = uint8(1)

	// Cap a single declared frame length to avoid huge allocations from corrupt input.
	maxCadenceFrameBytes = 64 << 20
)

type cadenceDataConverter struct{}

// NewCadenceDataConverter returns the IWFDC-framed Cadence DataConverter.
func NewCadenceDataConverter() encoded.DataConverter {
	return &cadenceDataConverter{}
}

func (c *cadenceDataConverter) ToData(values ...interface{}) ([]byte, error) {
	// Cadence query handlers with no args encode/decode empty payloads.
	if len(values) == 0 {
		return nil, nil
	}
	if len(values) == 1 {
		if raw, ok := values[0].([]byte); ok {
			return raw, nil
		}
	}

	var buf bytes.Buffer
	buf.WriteString(cadenceMagic)
	if err := buf.WriteByte(byte(cadenceVersion)); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.BigEndian, uint32(len(values))); err != nil {
		return nil, err
	}

	for i, value := range values {
		kind, isNil, payload, err := encodeCadenceValue(value)
		if err != nil {
			return nil, fmt.Errorf("cadence converter: encode arg %d: %w", i, err)
		}
		if err := buf.WriteByte(byte(kind)); err != nil {
			return nil, err
		}
		nilFlag := nilFlagFalse
		if isNil {
			nilFlag = nilFlagTrue
		}
		if err := buf.WriteByte(byte(nilFlag)); err != nil {
			return nil, err
		}
		if err := binary.Write(&buf, binary.BigEndian, uint32(len(payload))); err != nil {
			return nil, err
		}
		if _, err := buf.Write(payload); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func (c *cadenceDataConverter) FromData(input []byte, valuePtrs ...interface{}) error {
	if len(valuePtrs) == 1 && isByteSlicePtr(valuePtrs[0]) {
		reflect.ValueOf(valuePtrs[0]).Elem().SetBytes(input)
		return nil
	}

	// No-arg Cadence queries pass an empty payload.
	if len(input) == 0 {
		if len(valuePtrs) == 0 {
			return nil
		}
		return fmt.Errorf("cadence converter: truncated header (0 bytes)")
	}

	if len(input) < cadenceHeaderLen {
		return fmt.Errorf("cadence converter: truncated header (%d bytes)", len(input))
	}
	if string(input[:5]) != cadenceMagic {
		return fmt.Errorf("cadence converter: bad magic %q", input[:min(5, len(input))])
	}
	version := input[5]
	if version != cadenceVersion {
		return fmt.Errorf("cadence converter: unsupported version %d", version)
	}
	frameCount := binary.BigEndian.Uint32(input[6:10])
	if int(frameCount) != len(valuePtrs) {
		return fmt.Errorf("cadence converter: arity mismatch: got %d frames, want %d", frameCount, len(valuePtrs))
	}

	offset := cadenceHeaderLen
	for i := 0; i < len(valuePtrs); i++ {
		if offset+cadenceFrameHdrLen > len(input) {
			return fmt.Errorf("cadence converter: truncated frame header at arg %d", i)
		}
		kind := input[offset]
		nilFlag := input[offset+1]
		length := binary.BigEndian.Uint32(input[offset+2 : offset+6])
		offset += cadenceFrameHdrLen
		if length > maxCadenceFrameBytes {
			return fmt.Errorf("cadence converter: frame %d length %d exceeds max", i, length)
		}
		remaining := len(input) - offset
		if int(length) > remaining {
			return fmt.Errorf("cadence converter: truncated frame data at arg %d", i)
		}
		payload := input[offset : offset+int(length)]
		offset += int(length)

		if nilFlag != nilFlagFalse && nilFlag != nilFlagTrue {
			return fmt.Errorf("cadence converter: bad nil flag %d at arg %d", nilFlag, i)
		}
		if err := decodeCadenceValue(kind, nilFlag == nilFlagTrue, payload, valuePtrs[i]); err != nil {
			return fmt.Errorf("cadence converter: decode arg %d: %w", i, err)
		}
	}
	if offset != len(input) {
		return fmt.Errorf("cadence converter: trailing %d bytes", len(input)-offset)
	}
	return nil
}

func encodeCadenceValue(value interface{}) (kind uint8, isNil bool, payload []byte, err error) {
	if value == nil {
		return kindJSON, true, nil, nil
	}

	rv := reflect.ValueOf(value)
	if isTypedNil(rv) {
		if _, ok := value.(proto.Message); ok || implementsProtoMessage(rv.Type()) {
			return kindProto, true, nil, nil
		}
		if rv.Kind() == reflect.Slice && rv.Type().Elem().Kind() == reflect.Uint8 {
			return kindRaw, true, nil, nil
		}
		return kindJSON, true, nil, nil
	}

	if raw, ok := value.([]byte); ok {
		return kindRaw, false, raw, nil
	}

	if msg, ok := value.(proto.Message); ok {
		data, marshalErr := proto.Marshal(msg)
		if marshalErr != nil {
			return 0, false, nil, marshalErr
		}
		return kindProto, false, data, nil
	}

	data, marshalErr := json.Marshal(value)
	if marshalErr != nil {
		return 0, false, nil, marshalErr
	}
	return kindJSON, false, data, nil
}

func decodeCadenceValue(kind uint8, isNil bool, payload []byte, valuePtr interface{}) error {
	if valuePtr == nil {
		return fmt.Errorf("nil value pointer")
	}
	rv := reflect.ValueOf(valuePtr)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return fmt.Errorf("value pointer must be a non-nil pointer, got %T", valuePtr)
	}
	target := rv.Elem()

	switch kind {
	case kindProto:
		return decodeProtoFrame(isNil, payload, target)
	case kindJSON:
		return decodeJSONFrame(isNil, payload, target)
	case kindRaw:
		return decodeRawFrame(isNil, payload, target)
	default:
		return fmt.Errorf("unknown kind %d", kind)
	}
}

func decodeProtoFrame(isNil bool, payload []byte, target reflect.Value) error {
	if !target.CanSet() {
		return fmt.Errorf("unsettable target %s", target.Type())
	}
	msgType, ok := protoMessageElemType(target.Type())
	if !ok {
		return fmt.Errorf("proto frame requires proto.Message pointer target, got %s", target.Type())
	}
	if isNil {
		target.Set(reflect.Zero(target.Type()))
		return nil
	}
	msg := reflect.New(msgType).Interface().(proto.Message)
	if err := proto.Unmarshal(payload, msg); err != nil {
		return err
	}
	if target.Kind() == reflect.Ptr {
		target.Set(reflect.ValueOf(msg))
		return nil
	}
	target.Set(reflect.ValueOf(msg).Elem())
	return nil
}

func decodeJSONFrame(isNil bool, payload []byte, target reflect.Value) error {
	if isNil {
		target.Set(reflect.Zero(target.Type()))
		return nil
	}
	return json.Unmarshal(payload, target.Addr().Interface())
}

func decodeRawFrame(isNil bool, payload []byte, target reflect.Value) error {
	if target.Kind() == reflect.Ptr && target.Type().Elem().Kind() == reflect.Slice &&
		target.Type().Elem().Elem().Kind() == reflect.Uint8 {
		if isNil {
			target.Set(reflect.Zero(target.Type()))
			return nil
		}
		cp := append([]byte(nil), payload...)
		target.Set(reflect.ValueOf(&cp))
		return nil
	}
	if target.Kind() == reflect.Slice && target.Type().Elem().Kind() == reflect.Uint8 {
		if isNil {
			target.Set(reflect.Zero(target.Type()))
			return nil
		}
		target.SetBytes(append([]byte(nil), payload...))
		return nil
	}
	return fmt.Errorf("raw frame requires []byte target, got %s", target.Type())
}

func isByteSlicePtr(valuePtr interface{}) bool {
	t := reflect.TypeOf(valuePtr)
	return t == reflect.TypeOf((*[]byte)(nil)) || t == reflect.TypeOf(([]byte)(nil))
}

func isTypedNil(rv reflect.Value) bool {
	switch rv.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map, reflect.Func, reflect.Chan:
		return rv.IsNil()
	default:
		return false
	}
}

func implementsProtoMessage(t reflect.Type) bool {
	return t.Implements(reflect.TypeOf((*proto.Message)(nil)).Elem())
}

func protoMessageElemType(t reflect.Type) (reflect.Type, bool) {
	switch {
	case t.Kind() == reflect.Ptr && implementsProtoMessage(t):
		return t.Elem(), true
	case implementsProtoMessage(reflect.PointerTo(t)):
		return t, true
	default:
		return nil, false
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
