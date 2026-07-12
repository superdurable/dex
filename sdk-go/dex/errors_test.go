package dex

import (
	"errors"
	"reflect"
	"testing"
)

func TestErrors_IsUndeclaredStateKey(t *testing.T) {
	err := newUndeclaredStateKeyError("count")
	if !errors.Is(err, ErrUndeclaredStateKey) {
		t.Fatal("expected ErrUndeclaredStateKey")
	}
	var detail *UndeclaredStateKeyError
	if !errors.As(err, &detail) || detail.Key != "count" {
		t.Fatalf("As: %+v", detail)
	}
}

func TestErrors_IsUndeclaredChannel(t *testing.T) {
	err := newUndeclaredChannelError("events")
	if !errors.Is(err, ErrUndeclaredChannel) {
		t.Fatal("expected ErrUndeclaredChannel")
	}
	var detail *UndeclaredChannelError
	if !errors.As(err, &detail) || detail.ChannelName != "events" {
		t.Fatalf("As: %+v", detail)
	}
}

func TestErrors_IsFlowNotRegistered(t *testing.T) {
	err := newFlowNotRegisteredError("my.Flow")
	if !errors.Is(err, ErrFlowNotRegistered) {
		t.Fatal("expected ErrFlowNotRegistered")
	}
}

func TestErrors_IsInputCountMismatch(t *testing.T) {
	err := newInputCountMismatchError(1, 3)
	if !errors.Is(err, ErrInputCountMismatch) {
		t.Fatal("expected ErrInputCountMismatch")
	}
}

func TestErrors_IsStartingStepInputMismatch(t *testing.T) {
	err := newStartingStepInputMismatchError("s1", 0, reflect.TypeOf(0), reflect.TypeOf(""))
	if !errors.Is(err, ErrStartingStepInputMismatch) {
		t.Fatal("expected ErrStartingStepInputMismatch")
	}
}

func TestErrors_IsRunNotFound(t *testing.T) {
	err := newRunNotFoundError("run-1")
	if !errors.Is(err, ErrRunNotFound) {
		t.Fatal("expected ErrRunNotFound")
	}
}
