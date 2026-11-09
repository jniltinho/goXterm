// Package require is the subset of testify's require API that the vendored go-rdp RemoteFX tests use:
// the assert checks, stopping the test on failure.
package require

import (
	"testing"

	"rustxterm-go/internal/rdpmit/codec/rfx/internal/assert"
)

func stop(t testing.TB, ok bool) {
	t.Helper()
	if !ok {
		t.FailNow()
	}
}

func Equal(t testing.TB, want, got any, extra ...any) {
	t.Helper()
	stop(t, assert.Equal(t, want, got, extra...))
}

func Len(t testing.TB, v any, n int, extra ...any) {
	t.Helper()
	stop(t, assert.Len(t, v, n, extra...))
}

func NoError(t testing.TB, err error, extra ...any) {
	t.Helper()
	stop(t, assert.NoError(t, err, extra...))
}

func NotNil(t testing.TB, v any, extra ...any) {
	t.Helper()
	stop(t, assert.NotNil(t, v, extra...))
}
