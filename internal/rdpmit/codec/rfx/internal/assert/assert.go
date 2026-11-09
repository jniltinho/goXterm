// Package assert is the small subset of testify's assert API that the vendored go-rdp RemoteFX tests
// use (rustxterm: testify is not a dependency of this module).
package assert

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
)

func fail(t testing.TB, msg string, extra []any) bool {
	t.Helper()
	if len(extra) > 0 {
		msg += ": " + fmt.Sprint(extra...)
	}
	t.Error(msg)
	return false
}

func isNil(v any) bool {
	if v == nil {
		return true
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return r.IsNil()
	}
	return false
}

func number(v any) float64 {
	r := reflect.ValueOf(v)
	switch {
	case r.CanInt():
		return float64(r.Int())
	case r.CanUint():
		return float64(r.Uint())
	case r.CanFloat():
		return r.Float()
	}
	panic(fmt.Sprintf("not a number: %T", v))
}

func Equal(t testing.TB, want, got any, extra ...any) bool {
	t.Helper()
	if reflect.DeepEqual(want, got) {
		return true
	}
	return fail(t, fmt.Sprintf("not equal: want %#v, got %#v", want, got), extra)
}

func NotEqual(t testing.TB, a, b any, extra ...any) bool {
	t.Helper()
	if !reflect.DeepEqual(a, b) {
		return true
	}
	return fail(t, fmt.Sprintf("should differ: %#v", a), extra)
}

func Error(t testing.TB, err error, extra ...any) bool {
	t.Helper()
	if err != nil {
		return true
	}
	return fail(t, "expected an error", extra)
}

func NoError(t testing.TB, err error, extra ...any) bool {
	t.Helper()
	if err == nil {
		return true
	}
	return fail(t, "unexpected error: "+err.Error(), extra)
}

func ErrorIs(t testing.TB, err, target error, extra ...any) bool {
	t.Helper()
	if errors.Is(err, target) {
		return true
	}
	return fail(t, fmt.Sprintf("error %v is not %v", err, target), extra)
}

func True(t testing.TB, v bool, extra ...any) bool {
	t.Helper()
	if v {
		return true
	}
	return fail(t, "expected true", extra)
}

func False(t testing.TB, v bool, extra ...any) bool {
	t.Helper()
	if !v {
		return true
	}
	return fail(t, "expected false", extra)
}

func Nil(t testing.TB, v any, extra ...any) bool {
	t.Helper()
	if isNil(v) {
		return true
	}
	return fail(t, fmt.Sprintf("expected nil, got %#v", v), extra)
}

func NotNil(t testing.TB, v any, extra ...any) bool {
	t.Helper()
	if !isNil(v) {
		return true
	}
	return fail(t, "expected a value, got nil", extra)
}

func Len(t testing.TB, v any, n int, extra ...any) bool {
	t.Helper()
	if l := reflect.ValueOf(v).Len(); l != n {
		return fail(t, fmt.Sprintf("length %d, want %d", l, n), extra)
	}
	return true
}

func Empty(t testing.TB, v any, extra ...any) bool {
	t.Helper()
	r := reflect.ValueOf(v)
	if isNil(v) || r.IsZero() || ((r.Kind() == reflect.Slice || r.Kind() == reflect.Map || r.Kind() == reflect.String) && r.Len() == 0) {
		return true
	}
	return fail(t, fmt.Sprintf("expected empty, got %#v", v), extra)
}

func Greater(t testing.TB, a, b any, extra ...any) bool {
	t.Helper()
	if number(a) > number(b) {
		return true
	}
	return fail(t, fmt.Sprintf("%v is not greater than %v", a, b), extra)
}

func GreaterOrEqual(t testing.TB, a, b any, extra ...any) bool {
	t.Helper()
	if number(a) >= number(b) {
		return true
	}
	return fail(t, fmt.Sprintf("%v is not >= %v", a, b), extra)
}

func LessOrEqual(t testing.TB, a, b any, extra ...any) bool {
	t.Helper()
	if number(a) <= number(b) {
		return true
	}
	return fail(t, fmt.Sprintf("%v is not <= %v", a, b), extra)
}

func InDelta(t testing.TB, want, got any, delta float64, extra ...any) bool {
	t.Helper()
	if math.Abs(number(want)-number(got)) <= delta {
		return true
	}
	return fail(t, fmt.Sprintf("%v and %v differ by more than %v", want, got, delta), extra)
}
