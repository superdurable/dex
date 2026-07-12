package dex

import (
	"encoding/json"
	"fmt"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
)

const jsonEncoding = "json"

// ObjectCodec serializes Go values to pb.Value wire form and back.
// Pass a custom implementation via RegistryOptions for encryption,
// compression, or alternate encodings.
type ObjectCodec interface {
	EncodingType() string
	EncodeValue(v any) (*pb.Value, error)
	DecodeValue(v *pb.Value, target any) error
	DecodeValueAny(v *pb.Value) (any, error)
}

// DefaultObjectCodec returns the SDK default ObjectCodec implementation.
func DefaultObjectCodec() ObjectCodec {
	return defaultObjectCodec{}
}

func decodeChannelMessages[T any](codec ObjectCodec, wireValues []*pb.Value) ([]T, error) {
	if len(wireValues) == 0 {
		return nil, nil
	}
	result := make([]T, 0, len(wireValues))
	for index, wireValue := range wireValues {
		var typed T
		if err := codec.DecodeValue(wireValue, &typed); err != nil {
			return nil, fmt.Errorf("dex: decode channel message %d into %T: %w", index, typed, err)
		}
		result = append(result, typed)
	}
	return result, nil
}

type defaultObjectCodec struct{}

func (defaultObjectCodec) EncodingType() string { return jsonEncoding }

// EncodeValue converts a Go value to a proto Value.
func (defaultObjectCodec) EncodeValue(v any) (*pb.Value, error) {
	if v == nil {
		return &pb.Value{Kind: &pb.Value_NullValue{NullValue: pb.NullValue_NULL_VALUE}}, nil
	}

	switch val := v.(type) {
	case int:
		return &pb.Value{Kind: &pb.Value_IntValue{IntValue: int64(val)}}, nil
	case int32:
		return &pb.Value{Kind: &pb.Value_IntValue{IntValue: int64(val)}}, nil
	case int64:
		return &pb.Value{Kind: &pb.Value_IntValue{IntValue: val}}, nil
	case float64:
		return &pb.Value{Kind: &pb.Value_DoubleValue{DoubleValue: val}}, nil
	case float32:
		return &pb.Value{Kind: &pb.Value_DoubleValue{DoubleValue: float64(val)}}, nil
	case bool:
		return &pb.Value{Kind: &pb.Value_BoolValue{BoolValue: val}}, nil
	case string:
		data, err := json.Marshal(val)
		if err != nil {
			return nil, fmt.Errorf("dex: encode string: %w", err)
		}
		return &pb.Value{Kind: &pb.Value_EncodedObject{EncodedObject: &pb.EncodedObject{
			Encoding: jsonEncoding,
			Payload:  data,
		}}}, nil
	default:
		data, err := json.Marshal(val)
		if err != nil {
			return nil, fmt.Errorf("dex: encode value: %w", err)
		}
		return &pb.Value{Kind: &pb.Value_EncodedObject{EncodedObject: &pb.EncodedObject{
			Encoding: jsonEncoding,
			Payload:  data,
		}}}, nil
	}
}

// DecodeValue writes a proto Value into target (target must be a pointer).
func (c defaultObjectCodec) DecodeValue(v *pb.Value, target any) error {
	if v == nil {
		return nil
	}
	switch kind := v.Kind.(type) {
	case *pb.Value_NullValue:
		return nil
	case *pb.Value_IntValue:
		return assignNumeric(kind.IntValue, target)
	case *pb.Value_DoubleValue:
		return assignNumeric(kind.DoubleValue, target)
	case *pb.Value_BoolValue:
		if pointer, ok := target.(*bool); ok {
			*pointer = kind.BoolValue
			return nil
		}
		return fmt.Errorf("dex: cannot decode bool into %T", target)
	case *pb.Value_EncodedObject:
		if kind.EncodedObject.Encoding != c.EncodingType() {
			return fmt.Errorf("dex: unsupported encoding %q", kind.EncodedObject.Encoding)
		}
		return json.Unmarshal(kind.EncodedObject.Payload, target)
	default:
		return fmt.Errorf("dex: unknown value kind %T", v.Kind)
	}
}

// DecodeValueAny decodes a proto Value into an empty-interface tree.
func (c defaultObjectCodec) DecodeValueAny(v *pb.Value) (any, error) {
	raw, err := valueToJSON(c, v)
	if err != nil {
		return nil, err
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

func valueToJSON(codec ObjectCodec, v *pb.Value) (json.RawMessage, error) {
	if v == nil {
		return json.RawMessage("null"), nil
	}
	switch kind := v.Kind.(type) {
	case *pb.Value_NullValue:
		return json.RawMessage("null"), nil
	case *pb.Value_IntValue:
		return json.Marshal(kind.IntValue)
	case *pb.Value_DoubleValue:
		return json.Marshal(kind.DoubleValue)
	case *pb.Value_BoolValue:
		return json.Marshal(kind.BoolValue)
	case *pb.Value_EncodedObject:
		if kind.EncodedObject.Encoding != codec.EncodingType() {
			return nil, fmt.Errorf("dex: unsupported encoding %q", kind.EncodedObject.Encoding)
		}
		return json.RawMessage(kind.EncodedObject.Payload), nil
	default:
		return json.RawMessage("null"), nil
	}
}

func assignNumeric[N int64 | float64](val N, target any) error {
	switch pointer := target.(type) {
	case *int:
		*pointer = int(val)
	case *int32:
		*pointer = int32(val)
	case *int64:
		*pointer = int64(val)
	case *float32:
		*pointer = float32(val)
	case *float64:
		*pointer = float64(val)
	default:
		return json.Unmarshal([]byte(fmt.Sprintf("%v", val)), target)
	}
	return nil
}
