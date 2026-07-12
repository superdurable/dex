package dex

import (
	"testing"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
)

func TestDefaultObjectCodec_RoundTrip(t *testing.T) {
	codec := DefaultObjectCodec()

	encoded, err := codec.EncodeValue(map[string]any{"k": "v"})
	if err != nil {
		t.Fatalf("EncodeValue: %v", err)
	}
	if _, ok := encoded.Kind.(*pb.Value_EncodedObject); !ok {
		t.Fatalf("expected EncodedObject, got %T", encoded.Kind)
	}

	var decoded map[string]any
	if err := codec.DecodeValue(encoded, &decoded); err != nil {
		t.Fatalf("DecodeValue: %v", err)
	}
	if decoded["k"] != "v" {
		t.Fatalf("decoded = %#v", decoded)
	}
}

func TestDefaultObjectCodec_PrimitiveKinds(t *testing.T) {
	codec := DefaultObjectCodec()

	intVal, err := codec.EncodeValue(int64(42))
	if err != nil {
		t.Fatalf("encode int: %v", err)
	}
	var out int64
	if err := codec.DecodeValue(intVal, &out); err != nil || out != 42 {
		t.Fatalf("int round-trip: out=%d err=%v", out, err)
	}

	nullVal, err := codec.EncodeValue(nil)
	if err != nil {
		t.Fatalf("encode nil: %v", err)
	}
	if _, ok := nullVal.Kind.(*pb.Value_NullValue); !ok {
		t.Fatalf("nil should encode as NullValue")
	}
}

func TestRegistry_CustomObjectCodec(t *testing.T) {
	registry := NewRegistryWithOptions(RegistryOptions{
		ObjectCodec: DefaultObjectCodec(),
	})
	if registry.ObjectCodec() == nil {
		t.Fatal("expected non-nil codec from registry")
	}
}

func TestDecodeChannelMessages_FromWireValues(t *testing.T) {
	codec := DefaultObjectCodec()
	wire, err := codec.EncodeValue(int64(42))
	if err != nil {
		t.Fatalf("EncodeValue: %v", err)
	}

	got, err := decodeChannelMessages[int64](codec, []*pb.Value{wire})
	if err != nil {
		t.Fatalf("decodeChannelMessages: %v", err)
	}
	if len(got) != 1 || got[0] != 42 {
		t.Fatalf("decodeChannelMessages = %v, want [42]", got)
	}
}

// A wire value that cannot decode into T must surface an error to the caller,
// not be silently dropped.
func TestDecodeChannelMessages_DecodeErrorPropagates(t *testing.T) {
	codec := DefaultObjectCodec()
	// A bool wire value cannot decode into a string target.
	wire := &pb.Value{Kind: &pb.Value_BoolValue{BoolValue: true}}

	got, err := decodeChannelMessages[string](codec, []*pb.Value{wire})
	if err == nil {
		t.Fatalf("expected decode error, got values %v", got)
	}
	if got != nil {
		t.Fatalf("expected nil slice on error, got %v", got)
	}
}
