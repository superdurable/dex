package errors

import (
	"fmt"
	"testing"
)

func TestIsRetriableExcludingCASError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "internal",
			err:  NewInternalError("x", fmt.Errorf("underlying")),
			want: true,
		},
		{
			name: "version mismatch CAS",
			err:  NewCASError("version mismatch", nil),
			want: false,
		},
		{
			name: "not found",
			err:  NewNotFoundError("missing", nil),
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IsRetriableExcludingCASError(tc.err); got != tc.want {
				t.Fatalf("IsRetriableExcludingCASError() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsRetriableExcludingCASError_Nil(t *testing.T) {
	t.Parallel()
	if IsRetriableExcludingCASError(nil) {
		t.Fatal("expected false for nil")
	}
}

func TestIsRetriableExcludingCASError_UncategorizedError(t *testing.T) {
	t.Parallel()
	if IsRetriableExcludingCASError(fmt.Errorf("plain")) {
		t.Fatal("expected false for plain error")
	}
}

func TestCategorizedErrorImpl_IsRetriableExcludingCAS(t *testing.T) {
	t.Parallel()
	internal := NewInternalError("x", nil)
	if !internal.IsRetriableExcludingCAS() {
		t.Fatal("internal should be retriable excluding CAS")
	}
	cas := NewCASError("cas", nil)
	if cas.IsRetriableExcludingCAS() {
		t.Fatal("CAS must not be retriable excluding CAS")
	}
}
